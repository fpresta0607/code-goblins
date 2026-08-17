package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// State is one service's verdict.
type State string

const (
	// StateGreen means the probe passed, or every declared variable resolved
	// for a service that declares no probe.
	StateGreen State = "green"
	// StateMissing means a credential the service needs is nowhere: not in
	// the environment, not in the store.
	StateMissing State = "missing"
	// StateExpired means the credential exists but the service rejected it.
	StateExpired State = "expired"
	// StateUnverified means the credential resolved but the probe tool is not
	// installed, so nothing could confirm it. That is a tooling gap, not an
	// authentication failure: the value is still what the project reads, so
	// it is injected and reported rather than counted as a fault.
	StateUnverified State = "no-tool"
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
	// Sources maps each resolved variable to where it came from, so a report
	// can show provenance without showing secrets.
	Sources map[string]string
	// Detail is the short reason behind a non-green state.
	Detail string
	// Fixed records what --fix did that changed this service's state.
	Fixed string
	// URL is where a human authenticates when nothing automatic worked.
	URL string
}

// Green reports whether this service was positively verified.
func (s Status) Green() bool { return s.State == StateGreen }

// Usable reports whether the credential should reach a goblin's pane. An
// unverified credential is still the value the project reads; withholding it
// because the probe tool is absent would break work that would otherwise
// succeed.
func (s Status) Usable() bool { return s.State == StateGreen || s.State == StateUnverified }

// Report is the whole preflight for one project.
type Report struct {
	Project  string
	Manifest string
	Store    string
	Statuses []Status
}

// Blocking returns the services that are neither green nor deliberately
// skipped: the ones a goblin would stall on.
func (r Report) Blocking() []Status {
	var blocking []Status
	for _, status := range r.Statuses {
		if status.State == StateMissing || status.State == StateExpired {
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
		if repair && (status.State == StateMissing || status.State == StateExpired) {
			status = c.repair(ctx, service, status)
		}
		report.Statuses = append(report.Statuses, status)
	}
	return report, nil
}

// evaluate resolves a service's credentials and runs its probe.
func (c Checker) evaluate(ctx context.Context, service Service) Status {
	status := Status{
		Service: service.Name,
		Method:  service.Method,
		Sources: map[string]string{},
		URL:     service.URL,
	}
	resolved, missing, err := Resolve(c.Store, service.Env)
	if err != nil {
		status.State = StateMissing
		status.Detail = err.Error()
		return status
	}
	for name, value := range resolved {
		status.Sources[name] = value.Source
	}
	status.Missing = missing

	// For an env service the variable is the credential, so an absent one is
	// the whole answer and running a probe would only report someone else's
	// ambient login as this project's. For a cli or oauth service the tool
	// holds its own login and the variables are the bonus that makes direct
	// API access possible, so the probe still decides - unless the probe
	// itself needs one of the missing values, in which case running it would
	// pass an unsubstituted $NAME and report a confusing failure.
	if len(missing) > 0 && (service.Method == MethodEnv || len(service.Probe) == 0 || referencesAny(service.Probe, missing)) {
		status.State = StateMissing
		status.Detail = "not in the environment or the credential store: " + strings.Join(missing, ", ")
		if service.Optional {
			status.State = StateSkipped
			status.Detail = "optional; " + status.Detail
		}
		return status
	}

	if len(service.Probe) == 0 {
		status.State = StateGreen
		status.Detail = "all declared variables resolved; no probe declared"
		return status
	}

	result, err := c.exec(ctx, service.Probe, resolved)
	switch {
	case errors.Is(err, exec.ErrNotFound):
		if len(missing) > 0 {
			status.State = StateMissing
			status.Detail = "not in the environment or the credential store: " + strings.Join(missing, ", ") + "; " + service.Probe[0] + " is not installed"
		} else {
			// The credential is fine; the tool that would confirm it is absent.
			// Calling that "expired" would send the Overlord hunting for a
			// credential problem that does not exist.
			status.State = StateUnverified
			status.Detail = "cannot verify: " + service.Probe[0] + " is not installed"
		}
	case err != nil:
		status.State = StateExpired
		status.Detail = "probe could not run: " + err.Error()
	case result.ExitCode == 0:
		status.State = StateGreen
		status.Detail = "probe passed"
		if len(missing) > 0 {
			// The tool is authenticated, but nothing can be exported to the
			// goblin's pane, so direct API or database access still is not
			// available. Say so instead of reporting a plain green.
			status.Detail += "; not exportable: " + strings.Join(missing, ", ") + " unset (run --fix to adopt)"
		}
	default:
		status.State = StateExpired
		status.Detail = fmt.Sprintf("probe exited %d: %s", result.ExitCode, firstLine(result.Stderr, result.Stdout))
	}
	if (status.State == StateMissing || status.State == StateExpired) && service.Optional {
		status.State = StateSkipped
		status.Detail = "optional; " + status.Detail
	}
	return status
}

// repair attempts the autonomous fixes for one service and re-probes.
func (c Checker) repair(ctx context.Context, service Service, status Status) Status {
	var attempts []string

	if len(service.Login) > 0 && len(status.Missing) == 0 {
		resolved, _, err := Resolve(c.Store, service.Env)
		if err == nil {
			result, runErr := c.exec(ctx, service.Login, resolved)
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
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = ProbeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
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

func firstLine(streams ...[]byte) string {
	for _, stream := range streams {
		text := strings.TrimSpace(string(stream))
		if text == "" {
			continue
		}
		line, _, _ := strings.Cut(text, "\n")
		return strings.TrimSpace(line)
	}
	return ""
}
