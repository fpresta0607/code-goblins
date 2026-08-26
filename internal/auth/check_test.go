package auth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// fakeRunner records what was run and replays a scripted result per command
// name, so probe and login behaviour is exercised without real services.
type fakeRunner struct {
	results map[string]execx.Result
	errs    map[string]error
	calls   []execx.Request
	// ignoresEveryPath makes `git check-ignore` answer the way it does for a
	// project that ignores every path asked about: each one echoed back on
	// stdout, which is how check-ignore reports the ones it matched.
	ignoresEveryPath bool
	// ignoredPaths is the subset check-ignore matches when only some paths
	// are ignored. Real check-ignore exits zero when ANY path matched and
	// echoes only the matches, so this is the shape that distinguishes
	// reading the echoed set from trusting the exit code.
	ignoredPaths []string
	// perFileExit answers a single-path `check-ignore --quiet` per path, so a
	// batch that fails can be followed by honest per-file answers.
	perFileExit map[string]int
}

func (r *fakeRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.calls = append(r.calls, req)
	if err, ok := r.errs[req.Name]; ok {
		return execx.Result{}, err
	}
	paths := checkIgnorePaths(req.Args)
	if req.Name == "git" && len(paths) == 1 && r.perFileExit != nil {
		if exit, known := r.perFileExit[paths[0]]; known {
			return execx.Result{ExitCode: exit}, nil
		}
	}
	if result, ok := r.results[req.Name]; ok {
		if req.Name == "git" && len(paths) > 0 {
			switch {
			case r.ignoresEveryPath:
				result.Stdout = []byte(strings.Join(paths, "\n") + "\n")
			case len(r.ignoredPaths) > 0 && result.ExitCode == 0:
				var matched []string
				for _, path := range paths {
					if slices.Contains(r.ignoredPaths, path) {
						matched = append(matched, path)
					}
				}
				result.Stdout = []byte(strings.Join(matched, "\n") + "\n")
			}
		}
		return result, nil
	}
	return execx.Result{ExitCode: 127, Stderr: []byte("command not found")}, nil
}

// checkIgnorePaths is the pathnames a `git check-ignore` invocation asks
// about, past whatever global options and the end-of-options separator.
func checkIgnorePaths(args []string) []string {
	for index, arg := range args {
		if arg == "check-ignore" {
			paths := args[index+1:]
			for len(paths) > 0 && strings.HasPrefix(paths[0], "-") {
				paths = paths[1:]
			}
			return paths
		}
	}
	return nil
}

func (r *fakeRunner) call(name string) (execx.Request, bool) {
	for _, req := range r.calls {
		if req.Name == name {
			return req, true
		}
	}
	return execx.Request{}, false
}

// memoryStore is a Store with no filesystem or vault behind it. It is keyed
// by the printed form of a Key, so a test can spell a shared entry "NAME" and
// a scoped one "project/NAME" exactly as an operator would.
type memoryStore struct {
	values map[string]string
	setErr error
}

func newMemoryStore(values map[string]string) *memoryStore {
	if values == nil {
		values = map[string]string{}
	}
	return &memoryStore{values: values}
}

func (s *memoryStore) Get(key Key) (string, bool, error) {
	value, ok := s.values[key.String()]
	return value, ok, nil
}

func (s *memoryStore) Set(key Key, value string) error {
	if s.setErr != nil {
		return s.setErr
	}
	s.values[key.String()] = value
	return nil
}

func (s *memoryStore) Keys() ([]Key, error) {
	var keys []Key
	for key := range s.values {
		project, name, scoped := strings.Cut(key, "/")
		if !scoped {
			keys = append(keys, Shared(key))
			continue
		}
		keys = append(keys, Key{Project: project, Name: name})
	}
	sortKeys(keys)
	return keys, nil
}

func (s *memoryStore) Describe() string { return "memory store" }

// clearEnv removes names from the process environment for one test, so a
// developer's own credentials cannot make a test pass.
func clearEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}

func TestCheckReportsGreenMissingAndUnauthorizedPerService(t *testing.T) {
	clearEnv(t, "STRIPE_SECRET_KEY", "QDRANT_URL", "SENTRY_DSN")
	manifest := Manifest{
		Project: "precisiondocs",
		Services: []Service{
			{Name: "stripe", Method: MethodCLI, Env: []string{"STRIPE_SECRET_KEY"}, Probe: []string{"stripe", "balance", "retrieve"}},
			{Name: "qdrant", Method: MethodEnv, Env: []string{"QDRANT_URL"}},
			{Name: "sentry", Method: MethodEnv, Env: []string{"SENTRY_DSN"}},
		},
	}
	store := newMemoryStore(map[string]string{
		"precisiondocs/STRIPE_SECRET_KEY": "sk_test_value_long_enough",
		"precisiondocs/QDRANT_URL":        "http://127.0.0.1:6333",
	})
	runner := &fakeRunner{results: map[string]execx.Result{
		"stripe": {ExitCode: 1, Stderr: []byte("Invalid API Key provided\nsecond line")},
	}}

	report, err := Checker{Store: store, Runner: runner, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(report.Statuses) != 3 {
		t.Fatalf("statuses = %d, want 3", len(report.Statuses))
	}
	byName := map[string]Status{}
	for _, status := range report.Statuses {
		byName[status.Service] = status
	}
	// "Invalid API Key" is the service refusing the credential. It is not
	// evidence the credential expired, and printing expired would send the
	// Overlord to a re-authentication that is not the fix.
	if got := byName["stripe"].State; got != StateUnauthorized {
		t.Errorf("stripe state = %q, want %q", got, StateUnauthorized)
	}
	if !strings.Contains(byName["stripe"].Detail, "Invalid API Key") {
		t.Errorf("stripe detail = %q, want the probe's first stderr line", byName["stripe"].Detail)
	}
	if got := byName["qdrant"].State; got != StateGreen {
		t.Errorf("qdrant state = %q, want %q (resolved, no probe declared)", got, StateGreen)
	}
	if got := byName["sentry"].State; got != StateMissing {
		t.Errorf("sentry state = %q, want %q", got, StateMissing)
	}
	if got := byName["sentry"].Missing; len(got) != 1 || got[0] != "SENTRY_DSN" {
		t.Errorf("sentry missing = %v, want [SENTRY_DSN]", got)
	}
	if report.OK() {
		t.Error("report.OK() = true with two blocking services")
	}
	if len(report.Blocking()) != 2 {
		t.Errorf("blocking = %d, want 2", len(report.Blocking()))
	}
}

func TestCheckRunsProbeWithResolvedCredentialInEnvironment(t *testing.T) {
	clearEnv(t, "QDRANT_API_KEY")
	manifest := Manifest{Services: []Service{{
		Name:   "qdrant",
		Method: MethodCLI,
		Env:    []string{"QDRANT_API_KEY"},
		Probe:  []string{"curl", "-H", "api-key: $QDRANT_API_KEY", "http://q/healthz"},
	}}}
	store := newMemoryStore(map[string]string{"precisiondocs/QDRANT_API_KEY": "stored-secret-value"})
	runner := &fakeRunner{results: map[string]execx.Result{"curl": {ExitCode: 0}}}

	report, err := Checker{Store: store, Runner: runner, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !report.Statuses[0].Green() {
		t.Fatalf("state = %q, want green", report.Statuses[0].State)
	}
	call, ok := runner.call("curl")
	if !ok {
		t.Fatal("probe was never run")
	}
	// $NAME in a probe argument is substituted, so a manifest never has to
	// hold the secret itself.
	if !contains(call.Args, "api-key: stored-secret-value") {
		t.Errorf("probe args = %v, want the resolved credential substituted", call.Args)
	}
	if !contains(call.Env, "QDRANT_API_KEY=stored-secret-value") {
		t.Errorf("probe env lacks the resolved credential")
	}
	if got := hit(report.Statuses[0], "QDRANT_API_KEY"); got != "store/precisiondocs" {
		t.Errorf("resolution hit = %q, want the project scope to answer", got)
	}
}

// hit names the candidate that answered for one variable, which is what makes
// "which value did I actually get" answerable without printing it.
func hit(status Status, name string) string {
	for _, candidate := range status.Resolution[name] {
		if candidate.Hit {
			return candidate.Source
		}
	}
	return ""
}

func TestCheckPrefersProcessEnvironmentOverStore(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://from-environment")
	manifest := Manifest{Services: []Service{{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}}}}
	store := newMemoryStore(map[string]string{"precisiondocs/DATABASE_URL": "postgres://from-store"})

	report, err := Checker{Store: store, Runner: &fakeRunner{}, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := hit(report.Statuses[0], "DATABASE_URL"); got != "env" {
		t.Errorf("resolution hit = %q, want the process environment to win", got)
	}
}

func TestCheckKeepsOptionalServicesOutOfBlocking(t *testing.T) {
	clearEnv(t, "OPTIONAL_KEY", "REQUIRED_KEY")
	manifest := Manifest{Services: []Service{
		{Name: "nice-to-have", Method: MethodEnv, Env: []string{"OPTIONAL_KEY"}, Optional: true},
		{Name: "must-have", Method: MethodEnv, Env: []string{"REQUIRED_KEY"}},
	}}

	report, err := Checker{Store: newMemoryStore(nil), Runner: &fakeRunner{}}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := report.Statuses[0].State; got != StateSkipped {
		t.Errorf("optional state = %q, want %q", got, StateSkipped)
	}
	blocking := report.Blocking()
	if len(blocking) != 1 || blocking[0].Service != "must-have" {
		t.Errorf("blocking = %v, want only must-have", blocking)
	}
}

func TestCheckReportsAProbeThatCannotRun(t *testing.T) {
	clearEnv(t, "FLY_API_TOKEN")
	manifest := Manifest{Services: []Service{{
		Name: "fly", Method: MethodCLI, Env: []string{"FLY_API_TOKEN"}, Shared: true, Probe: []string{"flyctl", "auth", "whoami"},
	}}}
	store := newMemoryStore(map[string]string{"FLY_API_TOKEN": "token-value-long-enough"})
	runner := &fakeRunner{errs: map[string]error{"flyctl": errors.New("executable file not found in %PATH%")}}

	report, err := Checker{Store: store, Runner: runner, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	status := report.Statuses[0]
	// A probe that could not start establishes only that the check failed.
	if status.State != StateFailed {
		t.Errorf("state = %q, want %q", status.State, StateFailed)
	}
	if !strings.Contains(status.Detail, "probe could not run") {
		t.Errorf("detail = %q, want it to say the probe could not run", status.Detail)
	}
}

func TestCheckSeparatesAnUninstalledProbeToolFromAnExpiredCredential(t *testing.T) {
	clearEnv(t, "FLY_API_TOKEN")
	manifest := Manifest{Services: []Service{{
		Name: "fly", Method: MethodCLI, Env: []string{"FLY_API_TOKEN"}, Shared: true, Probe: []string{"flyctl", "auth", "whoami"},
	}}}
	store := newMemoryStore(map[string]string{"FLY_API_TOKEN": "token-value-long-enough"})
	runner := &fakeRunner{errs: map[string]error{"flyctl": exec.ErrNotFound}}

	report, err := Checker{Store: store, Runner: runner, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	status := report.Statuses[0]
	if status.State != StateUnverified {
		t.Fatalf("state = %q, want %q: the credential is fine, the tool is absent", status.State, StateUnverified)
	}
	if !strings.Contains(status.Detail, "flyctl is not installed") {
		t.Errorf("detail = %q, want it to name the missing tool", status.Detail)
	}
	// It is not a credential fault, so it must not appear in the sign-in
	// request the Overlord is asked to answer.
	if len(report.Blocking()) != 0 {
		t.Errorf("blocking = %v, want an uninstalled probe tool not to block", report.Blocking())
	}
	// The value is still what the project reads, so the goblin gets it.
	env, err := InjectEnv(store, "precisiondocs", manifest, report)
	if err != nil {
		t.Fatalf("InjectEnv: %v", err)
	}
	if env["FLY_API_TOKEN"] != "token-value-long-enough" {
		t.Errorf("env = %v, want the present credential injected anyway", env)
	}
}

func TestCheckReportsAMissingCredentialWhenTheProbeToolIsAlsoAbsent(t *testing.T) {
	clearEnv(t, "FLY_API_TOKEN")
	manifest := Manifest{Project: "precisiondocs", Services: []Service{{
		Name: "fly", Method: MethodCLI, Env: []string{"FLY_API_TOKEN"}, Probe: []string{"flyctl", "auth", "whoami"},
	}}}
	runner := &fakeRunner{errs: map[string]error{"flyctl": exec.ErrNotFound}}

	report, err := Checker{Store: newMemoryStore(nil), Runner: runner, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	status := report.Statuses[0]
	if status.State != StateMissing {
		t.Fatalf("state = %q, want %q: the credential is missing even though the probe tool is absent too", status.State, StateMissing)
	}
	if !strings.Contains(status.Detail, "FLY_API_TOKEN") {
		t.Errorf("detail = %q, want the missing variable named", status.Detail)
	}
	blocking := report.Blocking()
	if len(blocking) != 1 || blocking[0].Service != "fly" {
		t.Fatalf("blocking = %v, want the missing credential to block", blocking)
	}
	var out strings.Builder
	if err := WriteLoginRequest(&out, report); err != nil {
		t.Fatalf("WriteLoginRequest: %v", err)
	}
	if !strings.Contains(out.String(), "fly") || !strings.Contains(out.String(), "FLY_API_TOKEN") {
		t.Errorf("login request = %q, want it to name the fly service and FLY_API_TOKEN", out.String())
	}
}

func TestCheckDoesNotProbeAnEnvServiceWhoseVariableIsMissing(t *testing.T) {
	clearEnv(t, "QDRANT_URL")
	// For an env service the variable is the credential, so a probe could
	// only report someone else's ambient login as this project's.
	manifest := Manifest{Services: []Service{{
		Name: "qdrant", Method: MethodEnv, Env: []string{"QDRANT_URL"}, Probe: []string{"curl", "$QDRANT_URL/healthz"},
	}}}
	runner := &fakeRunner{results: map[string]execx.Result{"curl": {ExitCode: 0}}}

	report, err := Checker{Store: newMemoryStore(nil), Runner: runner}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.Statuses[0].State != StateMissing {
		t.Errorf("state = %q, want %q", report.Statuses[0].State, StateMissing)
	}
	if _, ran := runner.call("curl"); ran {
		t.Error("probed an env service whose variable is the credential")
	}
}

func TestCheckTrustsACLIToolsOwnLoginWhenTheVariableIsUnset(t *testing.T) {
	clearEnv(t, "GITHUB_TOKEN")
	// gh keeps its own credential. The variable is what makes direct API
	// access possible for a goblin, not what makes gh work.
	manifest := Manifest{Services: []Service{{
		Name: "github", Method: MethodCLI, Env: []string{"GITHUB_TOKEN"}, Probe: []string{"gh", "auth", "status"},
	}}}
	runner := &fakeRunner{results: map[string]execx.Result{"gh": {ExitCode: 0}}}

	report, err := Checker{Store: newMemoryStore(nil), Runner: runner}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	status := report.Statuses[0]
	if status.State != StateGreen {
		t.Fatalf("state = %q, want %q: the tool is authenticated", status.State, StateGreen)
	}
	// Green is not the whole truth: nothing can reach the goblin's pane yet.
	if !strings.Contains(status.Detail, "not exportable") || !strings.Contains(status.Detail, "GITHUB_TOKEN") {
		t.Errorf("detail = %q, want it to say the variable is still unset", status.Detail)
	}
	if len(report.Blocking()) != 0 {
		t.Errorf("blocking = %v, want an authenticated tool not to block", report.Blocking())
	}
}

func TestCheckDoesNotRunAProbeThatNeedsTheMissingCredential(t *testing.T) {
	clearEnv(t, "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET")
	// Running this would pass a literal "$STRIPE_SECRET_KEY" to the CLI and
	// report its complaint about the key format as the diagnosis.
	manifest := Manifest{Services: []Service{{
		Name:   "stripe",
		Method: MethodCLI,
		Env:    []string{"STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET"},
		Probe:  []string{"stripe", "balance", "retrieve", "--api-key", "$STRIPE_SECRET_KEY"},
	}}}
	runner := &fakeRunner{results: map[string]execx.Result{"stripe": {ExitCode: 1, Stderr: []byte("the CLI only supports using a secret key")}}}

	report, err := Checker{Store: newMemoryStore(nil), Runner: runner}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	status := report.Statuses[0]
	if status.State != StateMissing {
		t.Fatalf("state = %q, want %q", status.State, StateMissing)
	}
	if !strings.Contains(status.Detail, "STRIPE_SECRET_KEY") {
		t.Errorf("detail = %q, want the missing variable named", status.Detail)
	}
	if _, ran := runner.call("stripe"); ran {
		t.Error("ran a probe that reads the very credential that is missing")
	}
}

func TestCheckReportsACLIToolThatIsNotLoggedIn(t *testing.T) {
	clearEnv(t, "VERCEL_TOKEN")
	manifest := Manifest{Services: []Service{{
		Name: "vercel", Method: MethodCLI, Env: []string{"VERCEL_TOKEN"}, Probe: []string{"vercel", "whoami"},
	}}}
	runner := &fakeRunner{results: map[string]execx.Result{"vercel": {ExitCode: 1, Stderr: []byte("Error: not authenticated")}}}

	report, err := Checker{Store: newMemoryStore(nil), Runner: runner}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.Statuses[0].State != StateUnauthorized {
		t.Errorf("state = %q, want %q", report.Statuses[0].State, StateUnauthorized)
	}
	if len(report.Blocking()) != 1 {
		t.Errorf("blocking = %v, want the unauthenticated tool to block", report.Blocking())
	}
}

func TestFixRunsTheNonInteractiveLoginAndReProbes(t *testing.T) {
	clearEnv(t, "STRIPE_SECRET_KEY")
	manifest := Manifest{Services: []Service{{
		Name:   "stripe",
		Method: MethodCLI,
		Env:    []string{"STRIPE_SECRET_KEY"},
		Probe:  []string{"stripe", "balance", "retrieve"},
		Login:  []string{"stripe", "login", "--api-key", "$STRIPE_SECRET_KEY"},
	}}}
	store := newMemoryStore(map[string]string{"precisiondocs/STRIPE_SECRET_KEY": "sk_test_value_long_enough"})
	// The probe fails first, the login succeeds, and the re-probe passes.
	runner := &probeThenPassRunner{}

	report, err := Checker{Store: store, Runner: runner, Project: "precisiondocs"}.Fix(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	status := report.Statuses[0]
	if status.State != StateGreen {
		t.Fatalf("state = %q, want %q after a successful login", status.State, StateGreen)
	}
	if !strings.Contains(status.Fixed, "login") {
		t.Errorf("Fixed = %q, want it to record the login", status.Fixed)
	}
	if !contains(runner.loginArgs, "sk_test_value_long_enough") {
		t.Errorf("login args = %v, want the stored key substituted", runner.loginArgs)
	}
}

// probeThenPassRunner fails the first probe, accepts the login, and passes
// every probe after that.
type probeThenPassRunner struct {
	probes    int
	loginArgs []string
}

func (r *probeThenPassRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	if len(req.Args) > 0 && req.Args[0] == "login" {
		r.loginArgs = req.Args
		return execx.Result{ExitCode: 0}, nil
	}
	r.probes++
	if r.probes == 1 {
		return execx.Result{ExitCode: 1, Stderr: []byte("API key expired")}, nil
	}
	return execx.Result{ExitCode: 0}, nil
}

func TestFixDrivesTheBrowserOnlyForOAuthServices(t *testing.T) {
	clearEnv(t, "VERCEL_TOKEN")
	manifest := Manifest{Services: []Service{
		{Name: "vercel", Method: MethodOAuth, Env: []string{"VERCEL_TOKEN"}, URL: "https://vercel.com/login", Confirm: []string{"Authorize"}},
		{Name: "plainenv", Method: MethodEnv, Env: []string{"MISSING_PLAIN"}, URL: "https://example.test"},
	}}
	browser := &fakeBrowser{note: "clicked \"Authorize\""}

	_, err := Checker{Store: newMemoryStore(nil), Runner: &fakeRunner{}, Browser: browser}.Fix(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if browser.calls != 1 {
		t.Fatalf("browser calls = %d, want exactly the one oauth service", browser.calls)
	}
	if browser.lastURL != "https://vercel.com/login" {
		t.Errorf("browser url = %q, want the oauth service's url", browser.lastURL)
	}
}

type fakeBrowser struct {
	calls   int
	lastURL string
	note    string
	err     error
}

func (b *fakeBrowser) Confirm(_ context.Context, url string, _ []string) (string, error) {
	b.calls++
	b.lastURL = url
	return b.note, b.err
}

func TestInjectEnvCarriesOnlyGreenServices(t *testing.T) {
	clearEnv(t, "GREEN_KEY", "RED_KEY")
	manifest := Manifest{Services: []Service{
		{Name: "green-one", Method: MethodEnv, Env: []string{"GREEN_KEY"}},
		{Name: "red-one", Method: MethodEnv, Env: []string{"RED_KEY"}},
	}}
	store := newMemoryStore(map[string]string{"precisiondocs/GREEN_KEY": "green-value"})

	report, err := Checker{Store: store, Runner: &fakeRunner{}, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	env, err := InjectEnv(store, "precisiondocs", manifest, report)
	if err != nil {
		t.Fatalf("InjectEnv: %v", err)
	}
	if env["GREEN_KEY"] != "green-value" {
		t.Errorf("GREEN_KEY = %q, want the resolved value", env["GREEN_KEY"])
	}
	// A half-working credential in the pane is worse than none: the goblin
	// would use it and fail on first call instead of asking.
	if _, present := env["RED_KEY"]; present {
		t.Error("a red service contributed to the injected environment")
	}
}

func TestInjectEnvExcludesAServiceWhoseProbeFailed(t *testing.T) {
	clearEnv(t, "SUPABASE_SERVICE_KEY")
	manifest := Manifest{Services: []Service{{
		Name: "supabase", Method: MethodCLI, Env: []string{"SUPABASE_SERVICE_KEY"}, Probe: []string{"supabase", "projects", "list"},
	}}}
	store := newMemoryStore(map[string]string{"precisiondocs/SUPABASE_SERVICE_KEY": "service-key-value-long"})
	runner := &fakeRunner{results: map[string]execx.Result{"supabase": {ExitCode: 1, Stderr: []byte("unauthorized")}}}

	report, err := Checker{Store: store, Runner: runner, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	env, err := InjectEnv(store, "precisiondocs", manifest, report)
	if err != nil {
		t.Fatalf("InjectEnv: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("env = %v, want nothing injected for a rejected credential", env)
	}
}

func TestWarningLineNamesEveryBlockingService(t *testing.T) {
	report := Report{Statuses: []Status{
		{Service: "stripe", State: StateExpired},
		{Service: "qdrant", State: StateGreen},
		{Service: "sentry", State: StateMissing},
		{Service: "optional", State: StateSkipped},
	}}
	line := WarningLine("projects/precisiondocs", report)
	for _, want := range []string{"1/4", "precisiondocs", "stripe (expired)", "sentry (missing)", "--fix"} {
		if !strings.Contains(line, want) {
			t.Errorf("warning %q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "optional") {
		t.Errorf("warning %q names a deliberately skipped service as blocking", line)
	}
}

func TestWarningLineOnACleanPreflight(t *testing.T) {
	report := Report{Statuses: []Status{{Service: "github", State: StateGreen}}}
	line := WarningLine("projects/homescout", report)
	if !strings.Contains(line, "1/1 services green") || strings.Contains(line, "BLOCKING") {
		t.Errorf("warning = %q, want a clean summary", line)
	}
}

func TestWriteTableNeverPrintsASecret(t *testing.T) {
	clearEnv(t, "SECRET_TOKEN")
	manifest := Manifest{Project: "p", Services: []Service{{Name: "svc", Method: MethodEnv, Env: []string{"SECRET_TOKEN"}}}}
	store := newMemoryStore(map[string]string{"p/SECRET_TOKEN": "super-secret-value-42"})

	report, err := Checker{Store: store, Runner: &fakeRunner{}, Project: "p"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	var out strings.Builder
	if err := WriteTable(&out, report); err != nil {
		t.Fatalf("WriteTable: %v", err)
	}
	if strings.Contains(out.String(), "super-secret-value-42") {
		t.Fatalf("table disclosed the credential:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "store/p HIT") {
		t.Errorf("table lost the resolution line:\n%s", out.String())
	}
}

func TestWriteLoginRequestConsolidatesEveryBlockingService(t *testing.T) {
	reports := []Report{
		{Project: "precisiondocs", Statuses: []Status{
			{Service: "stripe", State: StateMissing, Missing: []string{"STRIPE_SECRET_KEY"}, Detail: "not stored", URL: "https://dashboard.stripe.com/apikeys"},
			{Service: "qdrant", State: StateGreen},
		}},
		{Project: "clock-in", Statuses: []Status{
			{Service: "neon", State: StateExpired, Detail: "probe exited 1", URL: "https://console.neon.tech"},
		}},
	}
	var out strings.Builder
	if err := WriteLoginRequest(&out, reports...); err != nil {
		t.Fatalf("WriteLoginRequest: %v", err)
	}
	text := out.String()
	for _, want := range []string{"2 services", "precisiondocs / stripe", "clock-in / neon", "cfo auth store --project precisiondocs STRIPE_SECRET_KEY", "https://console.neon.tech"} {
		if !strings.Contains(text, want) {
			t.Errorf("login request lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "qdrant") {
		t.Errorf("login request asked about a green service:\n%s", text)
	}
}

func TestWriteLoginRequestStaysSilentWhenNothingIsBlocking(t *testing.T) {
	var out strings.Builder
	if err := WriteLoginRequest(&out, Report{Statuses: []Status{{Service: "a", State: StateGreen}}}); err != nil {
		t.Fatalf("WriteLoginRequest: %v", err)
	}
	if out.String() != "" {
		t.Errorf("login request = %q, want nothing to ask", out.String())
	}
}

func TestRedactKeepsShortSecretsFullyHidden(t *testing.T) {
	cases := map[string]string{
		"":                      "",
		"short":                 "***",
		"sk_live_0123456789abc": "sk_l***bc",
	}
	for value, want := range cases {
		if got := Redact(value); got != want {
			t.Errorf("Redact(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestExpandMatchesLongestNameAndNeverRescansAValue(t *testing.T) {
	resolved := map[string]Resolved{
		"FOO":     {Name: "FOO", Value: "foo-value", From: "store/p"},
		"FOO_BAR": {Name: "FOO_BAR", Value: "bar-value", From: "store/p"},
		"OTHER":   {Name: "OTHER", Value: "$FOO", From: "store/p"},
	}
	cases := []struct {
		arg  string
		want string
	}{
		{"$FOO_BAR", "bar-value"},
		{"$FOO", "foo-value"},
		{"$FOO_BAZ", "$FOO_BAZ"},
		{"$OTHER", "$FOO"},
		{"$FOO and $FOO_BAR", "foo-value and bar-value"},
	}
	for _, tc := range cases {
		if got := expand(tc.arg, resolved); got != tc.want {
			t.Errorf("expand(%q) = %q, want %q", tc.arg, got, tc.want)
		}
	}
}

func TestSpawnPreflightIsSilentForAProjectWithNoManifest(t *testing.T) {
	dataDir := t.TempDir()
	result, err := SpawnPreflight{DataDir: dataDir, Runner: &fakeRunner{}}.Preflight(context.Background(), filepath.Join("projects", "nothing-declared"))
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(result.Env) != 0 || result.Warning != "" || result.Refusal != "" {
		t.Errorf("preflight = %+v, want a project with no manifest to be silent", result)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeManifest(t *testing.T, dataDir, project string, manifest Manifest) string {
	t.Helper()
	path := ManifestPath(dataDir, project)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A harness billing key is never injected, whatever a manifest declares. On
// 2026-08-25 a manifest declared the app's production ANTHROPIC_API_KEY, the
// spawn handed it to every goblin, the harness took it over the subscription,
// and the gate ran the key to its spend limit. The manifest is the attack
// surface, so the guard sits below it.
func TestInjectEnvNeverCarriesAHarnessBillingKey(t *testing.T) {
	clearEnv(t, "ANTHROPIC_API_KEY", "anthropic_api_key", "OPENAI_API_KEY", "DATABASE_URL")
	manifest := Manifest{Services: []Service{
		{Name: "anthropic", Method: MethodEnv, Env: []string{"ANTHROPIC_API_KEY"}},
		{Name: "anthropic-lower", Method: MethodEnv, Env: []string{"anthropic_api_key"}},
		{Name: "openai", Method: MethodEnv, Env: []string{"OPENAI_API_KEY"}},
		{Name: "neon", Method: MethodEnv, Env: []string{"DATABASE_URL"}},
	}}
	store := newMemoryStore(map[string]string{
		"precisiondocs/ANTHROPIC_API_KEY": "sk-ant-api03-production-key-value",
		"precisiondocs/anthropic_api_key": "sk-ant-api03-lowercase-key-value",
		"precisiondocs/OPENAI_API_KEY":    "sk-openai-production-key-value",
		"precisiondocs/DATABASE_URL":      "postgres://ep-clockin-cool-morning/db",
	})

	report, err := Checker{Store: store, Runner: &fakeRunner{}, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	env, err := InjectEnv(store, "precisiondocs", manifest, report)
	if err != nil {
		t.Fatalf("InjectEnv: %v", err)
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "anthropic_api_key", "OPENAI_API_KEY"} {
		if _, present := env[name]; present {
			t.Errorf("%s was injected; a harness billing key must never reach a goblin", name)
		}
	}
	// The guard is precise: an ordinary project credential still flows.
	if env["DATABASE_URL"] == "" {
		t.Error("DATABASE_URL was withheld; the billing-key guard must not swallow ordinary credentials")
	}
}

func TestIsHarnessBillingKeyIsCaseInsensitive(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "anthropic_api_key", "Anthropic_Api_Key", "OPENAI_API_KEY", "GEMINI_API_KEY"} {
		if !IsHarnessBillingKey(name) {
			t.Errorf("IsHarnessBillingKey(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"DATABASE_URL", "FLY_API_TOKEN", "GITHUB_TOKEN", "STRIPE_SECRET_KEY", "ANTHROPIC_MODEL"} {
		if IsHarnessBillingKey(name) {
			t.Errorf("IsHarnessBillingKey(%q) = true, want false", name)
		}
	}
}
