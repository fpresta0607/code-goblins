package auth

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// argvRunner replays a result per full command line, so a service whose
// probe and identity check are the same binary can be scripted separately.
type argvRunner struct {
	results map[string]execx.Result
	calls   []string
}

func (r *argvRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	line := strings.Join(append([]string{req.Name}, req.Args...), " ")
	r.calls = append(r.calls, line)
	if result, ok := r.results[line]; ok {
		return result, nil
	}
	return execx.Result{ExitCode: 127, Stderr: []byte("command not found")}, nil
}

// postgres is the service at the centre of the incident: two projects, one
// declared name, and no way for a liveness probe to tell the databases apart.
func postgresService(expectHost string) Service {
	service := Service{Name: "postgres", Method: MethodEnv, Env: []string{"DATABASE_URL"}}
	if expectHost != "" {
		service.Identity = &Identity{
			Var:    "DATABASE_URL",
			Expect: expectHost,
			Note:   "DATABASE_URL points at this project's branch",
		}
	}
	return service
}

func TestTwoProjectsDeclaringTheSameNameGetDifferentValues(t *testing.T) {
	clearEnv(t, "DATABASE_URL")
	// One flat slot for five projects is what handed a goblin an unrelated
	// project's Neon branch. The scope is part of the key now.
	store := newMemoryStore(map[string]string{
		"precisiondocs/DATABASE_URL": "postgres://ep-precisiondocs/appdb",
		"clock-in/DATABASE_URL":      "postgres://ep-clockin/neondb",
	})
	manifest := Manifest{Services: []Service{postgresService("")}}

	values := map[string]string{}
	for _, project := range []string{"precisiondocs", "clock-in"} {
		report, err := Checker{Store: store, Runner: &fakeRunner{}, Project: project}.Check(context.Background(), manifest)
		if err != nil {
			t.Fatalf("Check(%s): %v", project, err)
		}
		if !report.Statuses[0].Green() {
			t.Fatalf("%s state = %q, want green", project, report.Statuses[0].State)
		}
		env, err := InjectEnv(store, project, manifest, report)
		if err != nil {
			t.Fatalf("InjectEnv(%s): %v", project, err)
		}
		values[project] = env["DATABASE_URL"]
	}
	if values["precisiondocs"] != "postgres://ep-precisiondocs/appdb" {
		t.Errorf("precisiondocs DATABASE_URL = %q, want its own value", values["precisiondocs"])
	}
	if values["clock-in"] != "postgres://ep-clockin/neondb" {
		t.Errorf("clock-in DATABASE_URL = %q, want its own value", values["clock-in"])
	}
	if values["precisiondocs"] == values["clock-in"] {
		t.Fatal("two projects declaring DATABASE_URL resolved to the same credential")
	}
}

func TestASharedValueIsRefusedAndReportedForAServiceNotDeclaredShared(t *testing.T) {
	clearEnv(t, "DATABASE_URL")
	// This is every credential stored before namespacing: it sits in the
	// shared scope and nothing records which project it belongs to.
	store := newMemoryStore(map[string]string{"DATABASE_URL": "postgres://whose-is-this"})
	manifest := Manifest{Project: "precisiondocs", Services: []Service{postgresService("")}}

	report, err := Checker{Store: store, Runner: &fakeRunner{}, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	status := report.Statuses[0]
	if status.State != StateMissing {
		t.Fatalf("state = %q, want %q: a shared value nobody claimed must not be guessed into a project", status.State, StateMissing)
	}
	if !strings.Contains(status.Detail, "not declared shared") {
		t.Errorf("detail = %q, want it to report the shared value rather than stay silent about it", status.Detail)
	}
	if !strings.Contains(status.Detail, "cfo auth copy DATABASE_URL --to precisiondocs") {
		t.Errorf("detail = %q, want the command that moves it into a scope without re-entering it", status.Detail)
	}
	env, err := InjectEnv(store, "precisiondocs", manifest, report)
	if err != nil {
		t.Fatalf("InjectEnv: %v", err)
	}
	if _, present := env["DATABASE_URL"]; present {
		t.Error("an unclaimed shared credential reached the pane environment")
	}
}

func TestASharedServiceStillReadsTheSharedScope(t *testing.T) {
	clearEnv(t, "GITHUB_TOKEN")
	// A credential that genuinely is one value everywhere stays stored once.
	store := newMemoryStore(map[string]string{"GITHUB_TOKEN": "gho_one_value_everywhere"})
	manifest := Manifest{Services: []Service{
		{Name: "github", Method: MethodCLI, Env: []string{"GITHUB_TOKEN"}, Shared: true},
	}}

	report, err := Checker{Store: store, Runner: &fakeRunner{}, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !report.Statuses[0].Green() {
		t.Fatalf("state = %q, want green from the shared scope", report.Statuses[0].State)
	}
	if got := hit(report.Statuses[0], "GITHUB_TOKEN"); got != "store/shared" {
		t.Errorf("resolution hit = %q, want the shared scope named", got)
	}
	chain := report.Statuses[0].Resolution["GITHUB_TOKEN"]
	if len(chain) != 3 || chain[0].Source != "env" || chain[1].Source != "store/precisiondocs" {
		t.Errorf("chain = %+v, want env then the project scope then shared", chain)
	}
}

func TestAProjectScopedValueWinsOverTheSharedOne(t *testing.T) {
	clearEnv(t, "GITHUB_TOKEN")
	store := newMemoryStore(map[string]string{
		"GITHUB_TOKEN":               "gho_shared",
		"precisiondocs/GITHUB_TOKEN": "gho_this_project",
	})
	manifest := Manifest{Services: []Service{
		{Name: "github", Method: MethodCLI, Env: []string{"GITHUB_TOKEN"}, Shared: true},
	}}

	report, err := Checker{Store: store, Runner: &fakeRunner{}, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := hit(report.Statuses[0], "GITHUB_TOKEN"); got != "store/precisiondocs" {
		t.Errorf("resolution hit = %q, want the project scope to win over shared", got)
	}
	env, err := InjectEnv(store, "precisiondocs", manifest, report)
	if err != nil {
		t.Fatalf("InjectEnv: %v", err)
	}
	if env["GITHUB_TOKEN"] != "gho_this_project" {
		t.Errorf("GITHUB_TOKEN = %q, want the project-scoped value", env["GITHUB_TOKEN"])
	}
}

func TestPostgresIdentitySeparatesTheRightInstanceFromAnInstance(t *testing.T) {
	clearEnv(t, "DATABASE_URL")
	manifest := Manifest{Services: []Service{postgresService("ep-precisiondocs-quiet-sun")}}

	cases := []struct {
		name  string
		value string
		want  State
	}{
		{"this project's branch", "postgres://u:p@ep-precisiondocs-quiet-sun.aws.neon.tech/appdb", StateGreen},
		// The exact shape of the incident: a live Postgres belonging to
		// somebody else, which every liveness probe reports green.
		{"an unrelated project's branch", "postgres://u:p@ep-steep-river-axsvclpp.aws.neon.tech/neondb", StateWrongTarget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryStore(map[string]string{"precisiondocs/DATABASE_URL": tc.value})
			report, err := Checker{Store: store, Runner: &fakeRunner{}, Project: "precisiondocs"}.Check(context.Background(), manifest)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			status := report.Statuses[0]
			if status.State != tc.want {
				t.Fatalf("state = %q, want %q (detail %q)", status.State, tc.want, status.Detail)
			}
			env, err := InjectEnv(store, "precisiondocs", manifest, report)
			if err != nil {
				t.Fatalf("InjectEnv: %v", err)
			}
			_, injected := env["DATABASE_URL"]
			if injected != (tc.want == StateGreen) {
				t.Errorf("injected = %v for %q; a credential pointing at another project's database must never reach a pane", injected, tc.want)
			}
			// The report proves the target without ever printing the value.
			if strings.Contains(status.Detail, tc.value) {
				t.Errorf("detail disclosed the connection string: %q", status.Detail)
			}
		})
	}
}

func TestIdentityStillDecidesWhenTheProbeToolIsNotInstalled(t *testing.T) {
	clearEnv(t, "DATABASE_URL")
	// psql is absent on this host, which is exactly why the var form of the
	// identity check exists: it needs no client tool, so a missing binary
	// must not be what lets an unrelated project's database through.
	service := postgresService("ep-precisiondocs-quiet-sun")
	service.Probe = []string{"psql", "$DATABASE_URL", "-c", "select 1"}
	manifest := Manifest{Services: []Service{service}}

	cases := []struct {
		name     string
		value    string
		want     State
		injected bool
	}{
		{
			"another project's branch",
			"postgres://u:p@ep-steep-river-axsvclpp.aws.neon.tech/neondb",
			StateWrongTarget,
			false,
		},
		{
			// The target is proven, the transport never was: the honest word
			// is still unverified, and the value is still what the project
			// reads.
			"this project's branch with nothing to prove it answers",
			"postgres://u:p@ep-precisiondocs-quiet-sun.aws.neon.tech/appdb",
			StateUnverified,
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryStore(map[string]string{"precisiondocs/DATABASE_URL": tc.value})
			runner := &fakeRunner{errs: map[string]error{"psql": exec.ErrNotFound}}
			report, err := Checker{Store: store, Runner: runner, Project: "precisiondocs"}.Check(context.Background(), manifest)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			status := report.Statuses[0]
			if status.State != tc.want {
				t.Fatalf("state = %q, want %q (detail %q)", status.State, tc.want, status.Detail)
			}
			if !strings.Contains(status.Detail, "psql is not installed") {
				t.Errorf("detail = %q, want the absent probe tool still reported", status.Detail)
			}
			env, err := InjectEnv(store, "precisiondocs", manifest, report)
			if err != nil {
				t.Fatalf("InjectEnv: %v", err)
			}
			if _, injected := env["DATABASE_URL"]; injected != tc.injected {
				t.Errorf("injected = %v, want %v: a credential naming another project's database must never reach a pane", injected, tc.injected)
			}
			if strings.Contains(status.Detail, tc.value) {
				t.Errorf("detail disclosed the connection string: %q", status.Detail)
			}
		})
	}
}

func TestSupabaseIdentityMatchesTheDeclaredProjectRef(t *testing.T) {
	clearEnv(t, "SUPABASE_URL", "SUPABASE_SERVICE_KEY")
	manifest := Manifest{Services: []Service{{
		Name:   "supabase",
		Method: MethodEnv,
		Env:    []string{"SUPABASE_URL", "SUPABASE_SERVICE_KEY"},
		Probe:  []string{"curl", "-fsS", "-H", "apikey: $SUPABASE_SERVICE_KEY", "$SUPABASE_URL/rest/v1/"},
		Identity: &Identity{
			Var:    "SUPABASE_URL",
			Expect: "abcdefghijklmnop.supabase.co",
			Note:   "SUPABASE_URL names this project's ref",
		},
	}}}
	runner := &fakeRunner{results: map[string]execx.Result{"curl": {ExitCode: 0}}}
	store := newMemoryStore(map[string]string{
		"precisiondocs/SUPABASE_URL":         "https://zzzzzzzzzzzzzzzz.supabase.co",
		"precisiondocs/SUPABASE_SERVICE_KEY": "service-key-long-enough",
	})

	report, err := Checker{Store: store, Runner: runner, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	status := report.Statuses[0]
	// The probe passed: a Supabase answered. It was not this project's.
	if status.State != StateWrongTarget {
		t.Fatalf("state = %q, want %q (detail %q)", status.State, StateWrongTarget, status.Detail)
	}
	if _, ran := runner.call("curl"); !ran {
		t.Error("the liveness probe never ran, so wrong_target was not established on top of a working transport")
	}
}

func TestFlyIdentityConfirmsTheDeclaredAppIsInTheAccount(t *testing.T) {
	clearEnv(t, "FLY_API_TOKEN")
	service := Service{
		Name:   "fly",
		Method: MethodOAuth,
		Env:    []string{"FLY_API_TOKEN"},
		Shared: true,
		Probe:  []string{"flyctl", "auth", "whoami"},
		Identity: &Identity{
			Command: []string{"flyctl", "apps", "list"},
			Expect:  "precisiondocs-api",
			Note:    "the token's account owns precisiondocs-api",
		},
	}
	manifest := Manifest{Services: []Service{service}}
	store := newMemoryStore(map[string]string{"FLY_API_TOKEN": "FlyV1_token_long_enough"})

	cases := []struct {
		name string
		apps string
		want State
	}{
		{"the declared app is listed", "NAME\nprecisiondocs-api\nprecisiondocs-web\n", StateGreen},
		// A valid token for the wrong Fly organization: whoami passes and the
		// deploy would land somewhere else entirely.
		{"another account's apps", "NAME\nsomebody-elses-api\n", StateWrongTarget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &argvRunner{results: map[string]execx.Result{
				"flyctl auth whoami": {ExitCode: 0, Stdout: []byte("someone@example.test\n")},
				"flyctl apps list":   {ExitCode: 0, Stdout: []byte(tc.apps)},
			}}
			report, err := Checker{Store: store, Runner: runner, Project: "precisiondocs"}.Check(context.Background(), manifest)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if got := report.Statuses[0].State; got != tc.want {
				t.Fatalf("state = %q, want %q (detail %q)", got, tc.want, report.Statuses[0].Detail)
			}
		})
	}
}

func TestAServiceWithNoIdentityCheckSaysItVerifiedOnlyLiveness(t *testing.T) {
	clearEnv(t, "REDIS_URL")
	manifest := Manifest{Services: []Service{{
		Name: "redis", Method: MethodEnv, Env: []string{"REDIS_URL"},
		Probe: []string{"redis-cli", "ping"},
	}}}
	store := newMemoryStore(map[string]string{"precisiondocs/REDIS_URL": "redis://127.0.0.1:6379"})
	runner := &fakeRunner{results: map[string]execx.Result{"redis-cli": {ExitCode: 0}}}

	report, err := Checker{Store: store, Runner: runner, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	status := report.Statuses[0]
	if !status.Green() {
		t.Fatalf("state = %q, want green", status.State)
	}
	// Green must not read as "this is the right Redis" when nothing checked.
	if !strings.Contains(status.Detail, "identity not verified") {
		t.Errorf("detail = %q, want it to say identity was not verified", status.Detail)
	}
	if status.Identity != "" {
		t.Errorf("Identity = %q, want nothing claimed", status.Identity)
	}
}

func TestExpiredIsNeverPrintedWithoutEvidenceOfExpiry(t *testing.T) {
	clearEnv(t, "SERVICE_TOKEN")
	manifest := Manifest{Services: []Service{{
		Name: "svc", Method: MethodCLI, Env: []string{"SERVICE_TOKEN"}, Shared: true,
		Probe: []string{"svctl", "whoami"},
	}}}
	store := newMemoryStore(map[string]string{"SERVICE_TOKEN": "token-value-long-enough"})

	cases := []struct {
		output string
		want   State
	}{
		{"Error: token expired 3 days ago", StateExpired},
		{"your session has ended, sign in again", StateExpired},
		// Everything below is a different fact with a different fix, and
		// none of it is evidence of expiry.
		{"Error: 401 Unauthorized", StateUnauthorized},
		{"Invalid API Key provided", StateUnauthorized},
		{"Error: not logged in", StateUnauthorized},
		{"permission denied for schema public", StateUnauthorized},
		{"dial tcp 10.0.0.1:5432: connect: connection refused", StateUnreachable},
		{"lookup db.example.test: no such host", StateUnreachable},
		{"context deadline exceeded: i/o timeout", StateUnreachable},
		// No evidence at all: say the check failed, invent nothing.
		{"", StateFailed},
		{"unexpected end of JSON input", StateFailed},
		{"Error: something went wrong", StateFailed},
	}
	for _, tc := range cases {
		t.Run(tc.output, func(t *testing.T) {
			runner := &fakeRunner{results: map[string]execx.Result{
				"svctl": {ExitCode: 1, Stderr: []byte(tc.output)},
			}}
			report, err := Checker{Store: store, Runner: runner, Project: "precisiondocs"}.Check(context.Background(), manifest)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			got := report.Statuses[0].State
			if got != tc.want {
				t.Fatalf("state = %q, want %q for probe output %q", got, tc.want, tc.output)
			}
			if got == StateExpired && tc.want != StateExpired {
				t.Fatalf("printed expired without evidence of expiry")
			}
		})
	}
}

func TestADeclaredAliasSatisfiesADeclaredName(t *testing.T) {
	clearEnv(t, "FLY_API_TOKEN", "FLY_PROD_API_TOKEN")
	// The store holds FLY_PROD_API_TOKEN; precisiondocs declares
	// FLY_API_TOKEN. That mismatch was reported as `fly (expired)`, which
	// sent the Overlord to a login that could not have helped.
	store := newMemoryStore(map[string]string{"FLY_PROD_API_TOKEN": "FlyV1_prod_token_value"})
	manifest := Manifest{Services: []Service{{
		Name:    "fly",
		Method:  MethodOAuth,
		Env:     []string{"FLY_API_TOKEN"},
		Shared:  true,
		Aliases: map[string][]string{"FLY_API_TOKEN": {"FLY_PROD_API_TOKEN"}},
	}}}

	report, err := Checker{Store: store, Runner: &fakeRunner{}, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	status := report.Statuses[0]
	if !status.Green() {
		t.Fatalf("state = %q (%s), want green: the alias is declared", status.State, status.Detail)
	}
	env, err := InjectEnv(store, "precisiondocs", manifest, report)
	if err != nil {
		t.Fatalf("InjectEnv: %v", err)
	}
	if env["FLY_API_TOKEN"] != "FlyV1_prod_token_value" {
		t.Errorf("FLY_API_TOKEN = %q, want the aliased value exported under the declared name", env["FLY_API_TOKEN"])
	}
	var out strings.Builder
	if err := WriteTable(&out, report); err != nil {
		t.Fatalf("WriteTable: %v", err)
	}
	// The alias must be visible, or the next reader repeats the hunt.
	if !strings.Contains(out.String(), "store/shared(FLY_PROD_API_TOKEN) HIT") {
		t.Errorf("table does not show which stored name answered:\n%s", out.String())
	}
}

func TestAnUndeclaredNearMissIsNeverFuzzyMatched(t *testing.T) {
	clearEnv(t, "FLY_API_TOKEN", "FLY_PROD_API_TOKEN")
	store := newMemoryStore(map[string]string{"FLY_PROD_API_TOKEN": "FlyV1_prod_token_value"})
	manifest := Manifest{Services: []Service{{
		Name: "fly", Method: MethodOAuth, Env: []string{"FLY_API_TOKEN"}, Shared: true,
	}}}

	report, err := Checker{Store: store, Runner: &fakeRunner{}, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.Statuses[0].State != StateMissing {
		t.Fatalf("state = %q, want %q: resemblance is not a declaration", report.Statuses[0].State, StateMissing)
	}
}

func TestRefusalNamesEveryBlockingServiceAndItsExactFixCommand(t *testing.T) {
	report := Report{Project: "precisiondocs", Statuses: []Status{
		{Service: "postgres", State: StateMissing, Missing: []string{"DATABASE_URL"}, Detail: "did not resolve: DATABASE_URL"},
		{Service: "qdrant", State: StateGreen},
		{Service: "supabase", State: StateWrongTarget, Detail: "wrong target: SUPABASE_URL does not name abc.supabase.co"},
		{Service: "sentry", State: StateSkipped},
	}}
	refusal := RefusalLines("projects/precisiondocs", report)
	for _, want := range []string{
		"2 blocking service(s) for precisiondocs",
		"--yolo",
		"postgres (missing)",
		"cfo auth store --project precisiondocs DATABASE_URL",
		"supabase (wrong_target)",
		"cfo auth projects/precisiondocs --fix",
	} {
		if !strings.Contains(refusal, want) {
			t.Errorf("refusal lacks %q:\n%s", want, refusal)
		}
	}
	if strings.Contains(refusal, "qdrant") || strings.Contains(refusal, "sentry") {
		t.Errorf("refusal named a service that is not blocking:\n%s", refusal)
	}
	if RefusalLines("projects/precisiondocs", Report{Statuses: []Status{{State: StateGreen}}}) != "" {
		t.Error("a clean preflight produced a refusal")
	}
}

func TestWarningLineCountsUnverifiedSeparatelyFromGreen(t *testing.T) {
	report := Report{Statuses: []Status{
		{Service: "github", State: StateGreen},
		{Service: "fly", State: StateUnverified},
	}}
	line := WarningLine("projects/precisiondocs", report)
	if !strings.Contains(line, "1/2 services green") || !strings.Contains(line, "1 unverified") {
		t.Errorf("warning = %q, want green and unverified counted apart", line)
	}
}

func TestDiscoverAdoptsTheTokenFlyctlAlreadyHolds(t *testing.T) {
	clearEnv(t, "FLY_API_TOKEN")
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".fly"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := "access_token: FlyV1 fm2_already_signed_in\nwire_guard_state: {}\n"
	if err := os.WriteFile(filepath.Join(home, ".fly", "config.yml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "precisiondocs")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Services: []Service{{
		Name: "fly", Method: MethodOAuth, Env: []string{"FLY_API_TOKEN"},
	}}}
	store := newMemoryStore(nil)

	adopted, err := Discover(context.Background(), store, gitIgnoresEverything(), manifest, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if store.values["precisiondocs/FLY_API_TOKEN"] != "FlyV1 fm2_already_signed_in" {
		t.Fatalf("FLY_API_TOKEN = %q, want the token flyctl already holds adopted into the project scope", store.values["precisiondocs/FLY_API_TOKEN"])
	}
	if len(adopted) != 1 || !strings.Contains(adopted[0].Origin, "config.yml") {
		t.Errorf("adopted = %+v, want one entry crediting the flyctl config", adopted)
	}
}

func TestValidateRefusesAnIdentityCheckThatCannotEstablishAnything(t *testing.T) {
	cases := map[string]Manifest{
		"no expect": {Services: []Service{{
			Name: "svc", Method: MethodEnv, Env: []string{"TOKEN"},
			Identity: &Identity{Var: "TOKEN"},
		}}},
		"neither command nor var": {Services: []Service{{
			Name: "svc", Method: MethodEnv, Env: []string{"TOKEN"},
			Identity: &Identity{Expect: "x"},
		}}},
		"both command and var": {Services: []Service{{
			Name: "svc", Method: MethodEnv, Env: []string{"TOKEN"},
			Identity: &Identity{Var: "TOKEN", Command: []string{"svctl"}, Expect: "x"},
		}}},
		"var the service does not declare": {Services: []Service{{
			Name: "svc", Method: MethodEnv, Env: []string{"TOKEN"},
			Identity: &Identity{Var: "OTHER_TOKEN", Expect: "x"},
		}}},
		"alias for an undeclared name": {Services: []Service{{
			Name: "svc", Method: MethodEnv, Env: []string{"TOKEN"},
			Aliases: map[string][]string{"OTHER": {"TOKEN"}},
		}}},
	}
	for name, manifest := range cases {
		if err := manifest.Validate(); err == nil {
			t.Errorf("Validate(%s) = nil, want a refusal", name)
		}
	}
}

func TestValidProjectNameRejectsAnythingThatCouldEscapeAScope(t *testing.T) {
	// A checkout is allowed to be called what it is called: refusing an
	// ordinary dotted or spaced directory name would abort the spawn instead
	// of refusing a credential.
	for _, name := range []string{"precisiondocs", "clock-in", "code_goblins", "a1", "docs.example.com", "Retire 91"} {
		if !ValidProjectName(name) {
			t.Errorf("ValidProjectName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "..", ".", "a/b", `a\b`, "a:b", ".hidden", "a.", "a ", " a"} {
		if ValidProjectName(name) {
			t.Errorf("ValidProjectName(%q) = true, want false", name)
		}
	}
}

func TestAToolsOwnWarningIsNotTakenAsEvidence(t *testing.T) {
	clearEnv(t, "FLY_API_TOKEN")
	manifest := Manifest{Services: []Service{{
		Name: "fly", Method: MethodOAuth, Env: []string{"FLY_API_TOKEN"}, Shared: true,
		Probe: []string{"flyctl", "auth", "whoami"},
	}}}
	store := newMemoryStore(map[string]string{"FLY_API_TOKEN": "FlyV1_token_long_enough"})
	// flyctl prints a metrics 401 on every invocation, before its real
	// complaint. Reading the warning as the verdict would name the wrong
	// fault and quote the wrong line back to the operator.
	runner := &fakeRunner{results: map[string]execx.Result{"flyctl": {
		ExitCode: 1,
		Stderr:   []byte("Warning: Metrics send issue: metrics send failed with status 401\nError: dial tcp 66.51.120.1:443: i/o timeout\n"),
	}}}

	report, err := Checker{Store: store, Runner: runner, Project: "precisiondocs"}.Check(context.Background(), manifest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	status := report.Statuses[0]
	if status.State != StateUnreachable {
		t.Fatalf("state = %q, want %q from the line that actually failed", status.State, StateUnreachable)
	}
	if !strings.Contains(status.Detail, "i/o timeout") || strings.Contains(status.Detail, "Metrics send issue") {
		t.Errorf("detail = %q, want the real error rather than the tool's standing warning", status.Detail)
	}
}
