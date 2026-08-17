package auth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
}

func (r *fakeRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.calls = append(r.calls, req)
	if err, ok := r.errs[req.Name]; ok {
		return execx.Result{}, err
	}
	if result, ok := r.results[req.Name]; ok {
		return result, nil
	}
	return execx.Result{ExitCode: 127, Stderr: []byte("command not found")}, nil
}

func (r *fakeRunner) call(name string) (execx.Request, bool) {
	for _, req := range r.calls {
		if req.Name == name {
			return req, true
		}
	}
	return execx.Request{}, false
}

// memoryStore is a Store with no filesystem or vault behind it.
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

func (s *memoryStore) Get(key string) (string, bool, error) {
	value, ok := s.values[key]
	return value, ok, nil
}

func (s *memoryStore) Set(key, value string) error {
	if s.setErr != nil {
		return s.setErr
	}
	s.values[key] = value
	return nil
}

func (s *memoryStore) Keys() ([]string, error) {
	var keys []string
	for key := range s.values {
		keys = append(keys, key)
	}
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

func TestCheckReportsGreenMissingAndExpiredPerService(t *testing.T) {
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
		"STRIPE_SECRET_KEY": "sk_test_value_long_enough",
		"QDRANT_URL":        "http://127.0.0.1:6333",
	})
	runner := &fakeRunner{results: map[string]execx.Result{
		"stripe": {ExitCode: 1, Stderr: []byte("Invalid API Key provided\nsecond line")},
	}}

	report, err := Checker{Store: store, Runner: runner}.Check(context.Background(), manifest)
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
	if got := byName["stripe"].State; got != StateExpired {
		t.Errorf("stripe state = %q, want %q", got, StateExpired)
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
	store := newMemoryStore(map[string]string{"QDRANT_API_KEY": "stored-secret-value"})
	runner := &fakeRunner{results: map[string]execx.Result{"curl": {ExitCode: 0}}}

	report, err := Checker{Store: store, Runner: runner}.Check(context.Background(), manifest)
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
	if report.Statuses[0].Sources["QDRANT_API_KEY"] != "store" {
		t.Errorf("source = %q, want %q", report.Statuses[0].Sources["QDRANT_API_KEY"], "store")
	}
}

func TestCheckPrefersProcessEnvironmentOverStore(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://from-environment")
	manifest := Manifest{Services: []Service{{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}}}}
	store := newMemoryStore(map[string]string{"DATABASE_URL": "postgres://from-store"})

	report, err := Checker{Store: store, Runner: &fakeRunner{}}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := report.Statuses[0].Sources["DATABASE_URL"]; got != "environment" {
		t.Errorf("source = %q, want the process environment to win", got)
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
		Name: "fly", Method: MethodCLI, Env: []string{"FLY_API_TOKEN"}, Probe: []string{"flyctl", "auth", "whoami"},
	}}}
	store := newMemoryStore(map[string]string{"FLY_API_TOKEN": "token-value-long-enough"})
	runner := &fakeRunner{errs: map[string]error{"flyctl": errors.New("executable file not found in %PATH%")}}

	report, err := Checker{Store: store, Runner: runner}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	status := report.Statuses[0]
	if status.State != StateExpired {
		t.Errorf("state = %q, want %q", status.State, StateExpired)
	}
	if !strings.Contains(status.Detail, "probe could not run") {
		t.Errorf("detail = %q, want it to say the probe could not run", status.Detail)
	}
}

func TestCheckSeparatesAnUninstalledProbeToolFromAnExpiredCredential(t *testing.T) {
	clearEnv(t, "FLY_API_TOKEN")
	manifest := Manifest{Services: []Service{{
		Name: "fly", Method: MethodCLI, Env: []string{"FLY_API_TOKEN"}, Probe: []string{"flyctl", "auth", "whoami"},
	}}}
	store := newMemoryStore(map[string]string{"FLY_API_TOKEN": "token-value-long-enough"})
	runner := &fakeRunner{errs: map[string]error{"flyctl": exec.ErrNotFound}}

	report, err := Checker{Store: store, Runner: runner}.Check(context.Background(), manifest)
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
	env, err := InjectEnv(store, manifest, report)
	if err != nil {
		t.Fatalf("InjectEnv: %v", err)
	}
	if env["FLY_API_TOKEN"] != "token-value-long-enough" {
		t.Errorf("env = %v, want the present credential injected anyway", env)
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
	if report.Statuses[0].State != StateExpired {
		t.Errorf("state = %q, want %q", report.Statuses[0].State, StateExpired)
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
	store := newMemoryStore(map[string]string{"STRIPE_SECRET_KEY": "sk_test_value_long_enough"})
	// The probe fails first, the login succeeds, and the re-probe passes.
	runner := &probeThenPassRunner{}

	report, err := Checker{Store: store, Runner: runner}.Fix(context.Background(), manifest)
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
		return execx.Result{ExitCode: 1, Stderr: []byte("expired")}, nil
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
	store := newMemoryStore(map[string]string{"GREEN_KEY": "green-value"})

	report, err := Checker{Store: store, Runner: &fakeRunner{}}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	env, err := InjectEnv(store, manifest, report)
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
	store := newMemoryStore(map[string]string{"SUPABASE_SERVICE_KEY": "service-key-value-long"})
	runner := &fakeRunner{results: map[string]execx.Result{"supabase": {ExitCode: 1, Stderr: []byte("unauthorized")}}}

	report, err := Checker{Store: store, Runner: runner}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	env, err := InjectEnv(store, manifest, report)
	if err != nil {
		t.Fatalf("InjectEnv: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("env = %v, want nothing injected for an expired credential", env)
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
	store := newMemoryStore(map[string]string{"SECRET_TOKEN": "super-secret-value-42"})

	report, err := Checker{Store: store, Runner: &fakeRunner{}}.Check(context.Background(), manifest)
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
	if !strings.Contains(out.String(), "SECRET_TOKEN from store") {
		t.Errorf("table lost the provenance line:\n%s", out.String())
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
	for _, want := range []string{"2 services", "precisiondocs / stripe", "clock-in / neon", "cfo auth store STRIPE_SECRET_KEY", "https://console.neon.tech"} {
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

func TestSpawnPreflightIsSilentForAProjectWithNoManifest(t *testing.T) {
	dataDir := t.TempDir()
	env, warning, err := SpawnPreflight{DataDir: dataDir, Runner: &fakeRunner{}}.Preflight(context.Background(), filepath.Join("projects", "nothing-declared"))
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(env) != 0 || warning != "" {
		t.Errorf("preflight = (%v, %q), want a project with no manifest to be silent", env, warning)
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
