package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// State is one service's verdict. Each value is a different fact with a
// different fix, so none of them may be printed on evidence that establishes
// only a weaker one.
type State string

const (
	// StateGreen means the credential resolved and everything the manifest
	// declared as checkable passed, identity included.
	StateGreen State = "green"
	// StateMissing means a credential the service needs is nowhere: not in
	// the environment, not in this project's scope, not in a shared scope
	// the service is allowed to read.
	StateMissing State = "missing"
	// StateWrongTarget means the credential works and points somewhere else.
	// It is the state that separates "a Postgres answered" from "this
	// project's Postgres answered".
	StateWrongTarget State = "wrong_target"
	// StateUnauthorized means the service answered and rejected the
	// credential, without saying the credential expired.
	StateUnauthorized State = "unauthorized"
	// StateExpired means the service said the credential expired. It is
	// never printed on anything weaker, because it sends a reader to a
	// re-authentication that may not be the problem.
	StateExpired State = "expired"
	// StateUnreachable means nothing answered: a refused connection, an
	// unresolvable host, a timeout.
	StateUnreachable State = "unreachable"
	// StateFailed means the check failed and did not say why. It is the
	// honest floor: a non-zero exit on its own establishes only that.
	StateFailed State = "failed"
	// StateUnverified means the credential resolved but nothing could
	// confirm it: the probe tool is not installed, or the check could not
	// run. That is a tooling gap, not an authentication failure, so the
	// value is still injected and reported rather than counted as a fault.
	StateUnverified State = "unverified"
	// StateSkipped means an optional service is unconfigured, which is a
	// choice rather than a fault.
	StateSkipped State = "skipped"
)

// ProbeTimeout bounds one probe or login command. A preflight that hangs is
// worse than one that reports a service as unreachable.
const ProbeTimeout = 20 * time.Second

// Status is one service's preflight result.
type Status struct {
	Service string
	Method  string
	State   State
	// Missing lists the environment variables that did not resolve.
	Missing []string
	// Resolution records the ordered candidates behind every declared
	// variable, so a report can show how a value was reached - or why a
	// stored one was refused - without showing the value.
	Resolution map[string][]Candidate
	// Detail is the short reason behind this state.
	Detail string
	// Identity names what an identity check proved, or is empty when the
	// service declared none and was therefore only checked for liveness.
	Identity string
	// Fixed records what --fix did that changed this service's state.
	Fixed string
	// URL is where a human authenticates when nothing automatic worked.
	URL string
	// Optional mirrors the manifest, so a report can say why a red service
	// is not blocking.
	Optional bool
}

// Green reports whether this service was positively verified.
func (s Status) Green() bool { return s.State == StateGreen }

// Usable reports whether the credential should reach a goblin's pane. An
// unverified credential is still the value the project reads; withholding it
// because the probe tool is absent would break work that would otherwise
// succeed. A wrong target is never usable: injecting it is the incident.
func (s Status) Usable() bool { return s.State == StateGreen || s.State == StateUnverified }

// Report is the whole preflight for one project.
type Report struct {
	Project  string
	Manifest string
	Store    string
	Statuses []Status
}

// Blocking returns the services that are neither verified, deliberately
// skipped, nor merely unconfirmable: the ones a goblin would stall on or, in
// the wrong_target case, silently damage something with.
func (r Report) Blocking() []Status {
	var blocking []Status
	for _, status := range r.Statuses {
		switch status.State {
		case StateMissing, StateWrongTarget, StateUnauthorized, StateExpired, StateUnreachable, StateFailed:
			blocking = append(blocking, status)
		}
	}
	return blocking
}

// OK reports whether nothing is blocking.
func (r Report) OK() bool { return len(r.Blocking()) == 0 }

// Checker runs a project's preflight. Its collaborators are injected so the
// probe and fix logic is testable without real services.
type Checker struct {
	Store   Store
	Runner  execx.Runner
	Browser Browser
	// Project scopes every credential lookup. An empty project reads only
	// the shared scope.
	Project string
	// Timeout bounds one probe or login command; zero means ProbeTimeout.
	Timeout time.Duration
}

// Check probes every service and reports without changing anything.
func (c Checker) Check(ctx context.Context, manifest Manifest) (Report, error) {
	return c.run(ctx, manifest, false)
}

// Fix probes every service and repairs what it can autonomously: a stored
// credential is hydrated by resolution alone, a non-interactive CLI login is
// run with the stored key, and an OAuth page whose browser session is already
// signed in is confirmed through chrome-devtools-axi.
func (c Checker) Fix(ctx context.Context, manifest Manifest) (Report, error) {
	return c.run(ctx, manifest, true)
}

func (c Checker) run(ctx context.Context, manifest Manifest, repair bool) (Report, error) {
	report := Report{Project: manifest.Project, Manifest: manifest.Path}
	if c.Store != nil {
		report.Store = c.Store.Describe()
	}
	for _, service := range manifest.Services {
		status := c.evaluate(ctx, service)
		if repair && isBlockingState(status.State) {
			status = c.repair(ctx, service, status)
		}
		report.Statuses = append(report.Statuses, status)
	}
	return report, nil
}

func isBlockingState(state State) bool {
	switch state {
	case StateMissing, StateWrongTarget, StateUnauthorized, StateExpired, StateUnreachable, StateFailed:
		return true
	}
	return false
}

// resolver reads credentials in this checker's project scope.
func (c Checker) resolver() Resolver {
	return Resolver{Store: c.Store, Project: c.Project}
}

// evaluate resolves a service's credentials, proves the transport with its
// probe, then proves the target with its identity check. Each step may only
// downgrade the verdict to what it actually established.
func (c Checker) evaluate(ctx context.Context, service Service) Status {
	status := Status{
		Service:  service.Name,
		Method:   service.Method,
		URL:      service.URL,
		Optional: service.Optional,
	}
	resolution, err := c.resolver().Resolve(service)
	if err != nil {
		status.State = StateMissing
		status.Detail = err.Error()
		return status
	}
	status.Resolution = resolution.Chains
	status.Missing = resolution.Missing

	// For an env service the variable is the credential, so an absent one is
	// the whole answer and running a probe would only report someone else's
	// ambient login as this project's. For a cli or oauth service the tool
	// holds its own login and the variables are the bonus that makes direct
	// API access possible, so the probe still decides - unless the probe
	// itself needs one of the missing values, in which case running it would
	// pass an unsubstituted $NAME and report a confusing failure.
	if len(resolution.Missing) > 0 && (service.Method == MethodEnv || len(service.Probe) == 0 || referencesAny(service.Probe, resolution.Missing)) {
		status.State = StateMissing
		status.Detail = missingDetail(resolution)
		return optionalize(status)
	}

	probeState := StateGreen
	if len(service.Probe) > 0 {
		state, detail := c.runProbe(ctx, service, resolution)
		probeState = state
		status.State = state
		status.Detail = detail
		// A probe that established a failure has settled the service: there
		// is nothing left to identify. A probe that could not run has settled
		// nothing, and the identity check may need no tool at all - which is
		// precisely the case a wrong target hides in.
		if state != StateGreen && state != StateUnverified {
			return optionalize(status)
		}
	} else {
		status.State = StateGreen
		status.Detail = "all declared variables resolved; no probe declared"
	}

	if len(resolution.Missing) > 0 {
		// The tool is authenticated, but nothing can be exported to the
		// goblin's pane, so direct API or database access still is not
		// available. Say so instead of reporting a plain green.
		status.Detail += "; not exportable: " + strings.Join(resolution.Missing, ", ") + " unset (run --fix to adopt)"
	}

	if service.Identity == nil {
		// Never imply more than was verified: a reachable service is not a
		// verified one, and that gap is what reported a stranger's database
		// green.
		status.Detail += "; liveness only, identity not verified"
		return optionalize(status)
	}
	state, detail := c.runIdentity(ctx, *service.Identity, resolution)
	status.Detail += "; " + detail
	switch {
	case state != StateGreen:
		status.State = state
	case probeState == StateGreen:
		status.State = StateGreen
		status.Identity = service.Identity.Describe()
	default:
		// The target is proven and the transport never was, so the weaker
		// word stands - but the identity that was confirmed is still recorded
		// rather than thrown away.
		status.State = probeState
		status.Identity = service.Identity.Describe()
	}
	return optionalize(status)
}

// optionalize moves a red optional service out of the blocking column: a
// project that runs without it has made a choice, not hit a fault.
func optionalize(status Status) Status {
	if !status.Optional || !isBlockingState(status.State) {
		return status
	}
	status.State = StateSkipped
	status.Detail = "optional; " + status.Detail
	return status
}

func missingDetail(resolution Resolution) string {
	detail := "did not resolve: " + strings.Join(resolution.Missing, ", ")
	for _, name := range resolution.Missing {
		for _, candidate := range resolution.Chains[name] {
			if candidate.Note != "" {
				detail += "; " + name + " " + candidate.Note
			}
		}
	}
	return detail
}

// runProbe establishes what a probe can establish: that something answered
// and accepted the credential. It never claims a target.
func (c Checker) runProbe(ctx context.Context, service Service, resolution Resolution) (State, string) {
	result, err := c.exec(ctx, service.Probe, resolution.Values)
	switch {
	case errors.Is(err, exec.ErrNotFound):
		if len(resolution.Missing) > 0 {
			return StateMissing, missingDetail(resolution) + "; " + service.Probe[0] + " is not installed"
		}
		// The credential is fine; the tool that would confirm it is absent.
		// Calling that "expired" would send the Overlord hunting for a
		// credential problem that does not exist.
		return StateUnverified, "cannot verify: " + service.Probe[0] + " is not installed"
	case errors.Is(err, context.DeadlineExceeded):
		return StateUnreachable, fmt.Sprintf("probe did not answer within %s", c.timeout())
	case err != nil:
		return StateFailed, "probe could not run: " + err.Error()
	case result.ExitCode == 0:
		return StateGreen, "probe passed"
	default:
		state := classifyFailure(result)
		return state, fmt.Sprintf("probe exited %d: %s", result.ExitCode, firstLine(result.Stderr, result.Stdout))
	}
}

// runIdentity proves the credential points at this project's instance. The
// var form needs no tool installed, which is why a connection string that
// names a different host is caught even on a machine with no client.
func (c Checker) runIdentity(ctx context.Context, identity Identity, resolution Resolution) (State, string) {
	if identity.Var != "" {
		resolved, ok := resolution.Values[identity.Var]
		if !ok {
			return StateUnverified, "identity unverified: " + identity.Var + " did not resolve"
		}
		if !strings.Contains(strings.ToLower(resolved.Value), strings.ToLower(identity.Expect)) {
			// The value itself is never printed; only the expectation it
			// failed, which the manifest already states in plain text.
			return StateWrongTarget, "wrong target: " + identity.Var + " does not name " + identity.Expect
		}
		return StateGreen, "identity confirmed: " + identity.Describe()
	}

	result, err := c.exec(ctx, identity.Command, resolution.Values)
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return StateUnverified, "identity unverified: " + identity.Command[0] + " is not installed"
	case errors.Is(err, context.DeadlineExceeded):
		return StateUnreachable, fmt.Sprintf("identity check did not answer within %s", c.timeout())
	case err != nil:
		return StateUnverified, "identity unverified: check could not run: " + err.Error()
	case result.ExitCode != 0:
		state := classifyFailure(result)
		return state, fmt.Sprintf("identity check exited %d: %s", result.ExitCode, firstLine(result.Stderr, result.Stdout))
	}
	output := strings.ToLower(string(result.Stdout) + "\n" + string(result.Stderr))
	if !strings.Contains(output, strings.ToLower(identity.Expect)) {
		return StateWrongTarget, "wrong target: " + identity.Command[0] + " does not report " + identity.Expect
	}
	return StateGreen, "identity confirmed: " + identity.Describe()
}

// failureEvidence maps the words a tool actually prints onto the fact they
// establish. Order matters: expiry is the most specific claim and the one a
// report may never assert without it.
var failureEvidence = []struct {
	state   State
	pattern *regexp.Regexp
}{
	{StateExpired, regexp.MustCompile(`(?i)expir(ed|es|ing|y|ation)|token is no longer valid|session has ended|past its lifetime`)},
	{StateUnauthorized, regexp.MustCompile(`(?i)\b(401|403|unauthorized|forbidden|authentication failed|bad credentials|access denied|permission denied|not logged in|not authenticated|invalid[ -](api[ -])?(key|token|credentials?|authentication))\b`)},
	{StateUnreachable, regexp.MustCompile(`(?i)connection refused|connection reset|could ?n[o']t connect|could not connect|no such host|name or service not known|network is unreachable|host is unreachable|i/o timeout|timed out|dial tcp|getaddrinfo|eai_again|econnrefused|enotfound`)},
}

// classifyFailure names the fact a failed command established, and nothing
// more. A non-zero exit with unhelpful output establishes only that the check
// failed, so that is what it reports. A tool's own warnings are excluded from
// the evidence: flyctl's metrics endpoint returns 401 on every invocation, and
// reading that as "the credential was rejected" would be a verdict drawn from
// a line that has nothing to do with the check.
func classifyFailure(result execx.Result) State {
	output := evidenceText(result.Stdout, result.Stderr)
	for _, evidence := range failureEvidence {
		if evidence.pattern.MatchString(output) {
			return evidence.state
		}
	}
	return StateFailed
}

func evidenceText(streams ...[]byte) string {
	var lines []string
	for _, stream := range streams {
		for _, line := range strings.Split(string(stream), "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" && !isToolWarning(trimmed) {
				lines = append(lines, trimmed)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// repair attempts the autonomous fixes for one service and re-probes.
func (c Checker) repair(ctx context.Context, service Service, status Status) Status {
	var attempts []string

	if len(service.Login) > 0 && len(status.Missing) == 0 {
		resolution, err := c.resolver().Resolve(service)
		if err == nil {
			result, runErr := c.exec(ctx, service.Login, resolution.Values)
			switch {
			case runErr != nil:
				attempts = append(attempts, "login could not run: "+runErr.Error())
			case result.ExitCode == 0:
				attempts = append(attempts, "ran "+service.Login[0]+" login")
			default:
				attempts = append(attempts, fmt.Sprintf("login exited %d", result.ExitCode))
			}
		}
	}

	if service.Method == MethodOAuth && c.Browser != nil && service.URL != "" {
		note, err := c.Browser.Confirm(ctx, service.URL, service.Confirm)
		if err != nil {
			attempts = append(attempts, "browser: "+err.Error())
		} else if note != "" {
			attempts = append(attempts, "browser: "+note)
		}
	}

	if len(attempts) == 0 {
		status.Fixed = ""
		return status
	}
	repaired := c.evaluate(ctx, service)
	repaired.Fixed = strings.Join(attempts, "; ")
	return repaired
}

func (c Checker) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return ProbeTimeout
}

// exec runs one manifest command with the resolved credentials in its
// environment. Arguments may reference a resolved value as $NAME, so a login
// command can pass a stored key without the manifest ever holding it.
func (c Checker) exec(ctx context.Context, argv []string, resolved map[string]Resolved) (execx.Result, error) {
	if c.Runner == nil {
		return execx.Result{}, fmt.Errorf("auth: no command runner configured")
	}
	if len(argv) == 0 {
		return execx.Result{}, fmt.Errorf("auth: empty command")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	args := make([]string, 0, len(argv)-1)
	for _, arg := range argv[1:] {
		args = append(args, expand(arg, resolved))
	}
	return c.Runner.Run(ctx, execx.Request{
		Name: argv[0],
		Args: args,
		Env:  Environ(resolved),
	})
}

// referencesAny reports whether a command reads any of the named variables as
// $NAME.
func referencesAny(argv []string, names []string) bool {
	for _, arg := range argv {
		for _, name := range names {
			if strings.Contains(arg, "$"+name) {
				return true
			}
		}
	}
	return false
}

// expand substitutes $NAME references with resolved values. An unknown name
// is left as written so a probe fails visibly rather than silently running
// with an empty argument.
func expand(arg string, resolved map[string]Resolved) string {
	if !strings.Contains(arg, "$") {
		return arg
	}
	names := make([]string, 0, len(resolved))
	for name := range resolved {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })

	var b strings.Builder
	for i := 0; i < len(arg); {
		if arg[i] != '$' {
			b.WriteByte(arg[i])
			i++
			continue
		}
		matched := false
		for _, name := range names {
			if !strings.HasPrefix(arg[i+1:], name) {
				continue
			}
			next := i + 1 + len(name)
			if next < len(arg) && isNameChar(arg[next]) {
				continue
			}
			b.WriteString(resolved[name].Value)
			i = next
			matched = true
			break
		}
		if !matched {
			b.WriteByte(arg[i])
			i++
		}
	}
	return b.String()
}

func isNameChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// Environ returns the current process environment with the resolved
// credentials applied, which is what a probe or login command needs to see.
func Environ(resolved map[string]Resolved) []string {
	overrides := make(map[string]string, len(resolved))
	for name, value := range resolved {
		overrides[name] = value.Value
	}
	environ := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if found {
			if _, replaced := overrides[name]; replaced {
				continue
			}
		}
		environ = append(environ, entry)
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		environ = append(environ, name+"="+overrides[name])
	}
	return environ
}

// firstLine picks the line a reader should act on. A tool's own warnings are
// skipped when there is a real line behind them: flyctl prints a metrics 401
// before its actual complaint, and reporting the warning sends the reader
// after a fault that is not the one that failed the check.
func firstLine(streams ...[]byte) string {
	fallback := ""
	for _, stream := range streams {
		for _, line := range strings.Split(string(stream), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if isToolWarning(line) {
				if fallback == "" {
					fallback = line
				}
				continue
			}
			return line
		}
	}
	return fallback
}

func isToolWarning(line string) bool {
	lower := strings.ToLower(line)
	return strings.HasPrefix(lower, "warning:") || strings.HasPrefix(lower, "warn:") || strings.HasPrefix(lower, "note:")
}
