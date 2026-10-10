package auth

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// standIns is a project whose scope holds a value for every service its
// manifest declares, one more no service declares, and a shared value. Every
// value is a stand-in, and every name is one no real terminal sets, so a test
// can never read a credential out of the terminal that runs it.
type standIns struct {
	dataDir string
	project string
	store   Store
}

const (
	standInDatabase = "STANDIN_DATABASE_URL"
	standInPayments = "STANDIN_PAYMENTS_KEY"
	standInWebhook  = "STANDIN_PAYMENTS_WEBHOOK"
	standInErrors   = "STANDIN_ERRORS_DSN"
	standInSource   = "STANDIN_SOURCE_TOKEN"
	standInStray    = "STANDIN_STRAY_KEY"
)

func newStandIns(t *testing.T, services ...Service) standIns {
	t.Helper()
	clearEnv(t, standInDatabase, standInPayments, standInWebhook, standInErrors, standInSource, standInStray)
	t.Setenv(StoreDirEnv, t.TempDir())
	dataDir := t.TempDir()
	project := filepath.Join(t.TempDir(), "ledger")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if services == nil {
		services = []Service{
			{Name: "database", Method: MethodEnv, Env: []string{standInDatabase}},
			{Name: "payments", Method: MethodEnv, Env: []string{standInPayments, standInWebhook}},
			{Name: "errors", Method: MethodEnv, Env: []string{standInErrors}, Optional: true},
			{Name: "source", Method: MethodEnv, Env: []string{standInSource}, Shared: true, Default: true},
		}
	}
	if len(services) > 0 {
		writeManifest(t, dataDir, "ledger", Manifest{Project: "ledger", Services: services})
	}
	store, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[Key]string{
		Scoped("ledger", standInDatabase): "stand-in-database",
		Scoped("ledger", standInPayments): "stand-in-payments",
		Scoped("ledger", standInWebhook):  "stand-in-webhook",
		Scoped("ledger", standInErrors):   "stand-in-errors",
		Scoped("ledger", standInStray):    "stand-in-stray",
		Shared(standInSource):             "stand-in-source",
	} {
		if err := store.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	return standIns{dataDir: dataDir, project: project, store: store}
}

func (s standIns) preflight(t *testing.T, need Need) Result {
	t.Helper()
	result, err := SpawnPreflight{DataDir: s.dataDir, Runner: gitIgnoresEverything()}.Preflight(context.Background(), s.project, need)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	return result
}

// carried is the names a terminal would carry, sorted. A failure prints names
// and never a value, stand-in or not.
func carried(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// holdsNoValue fails when text holds any value of the stand-in store.
func holdsNoValue(t *testing.T, what, text string) {
	t.Helper()
	for _, value := range []string{"stand-in-database", "stand-in-payments", "stand-in-webhook", "stand-in-errors", "stand-in-stray", "stand-in-source"} {
		if strings.Contains(text, value) {
			t.Errorf("%s holds a credential value", what)
		}
	}
}

func TestATaskThatNamesOneServiceCarriesThatOneAndNoOther(t *testing.T) {
	result := newStandIns(t).preflight(t, Need{Services: []string{"database"}})

	if got := carried(result.Env); !slices.Equal(got, []string{standInDatabase}) {
		t.Errorf("the terminal carries %v, want only %s", got, standInDatabase)
	}
	if !slices.Equal(result.Grant.Services, []string{"database"}) {
		t.Errorf("granted %v, want database alone", result.Grant.Services)
	}
	if !slices.Equal(result.Grant.Withheld, []string{"payments", "errors", "source"}) {
		t.Errorf("withheld %v, want every other service in the manifest's order", result.Grant.Withheld)
	}
}

func TestATaskThatNamesNoneCarriesNone(t *testing.T) {
	result := newStandIns(t).preflight(t, Need{})

	if got := carried(result.Env); len(got) != 0 {
		t.Errorf("the terminal carries %v, want nothing: not even the service the manifest gives by default", got)
	}
	if len(result.Grant.Services) != 0 {
		t.Errorf("granted %v, want none", result.Grant.Services)
	}
	if result.Refusal != "" {
		t.Errorf("refusal = %q, want a task that names none never held up by a service it does not carry", result.Refusal)
	}
	if !strings.Contains(result.Warning, "auth: no service's credentials for ledger") || strings.Contains(result.Warning, "0/0") {
		t.Errorf("warning = %q, want it said plainly that the task carries none", result.Warning)
	}
}

func TestABriefThatNamesNoServicesCarriesOnlyTheDefaultOnes(t *testing.T) {
	result := newStandIns(t).preflight(t, Need{IsUnstated: true})

	if got := carried(result.Env); !slices.Equal(got, []string{standInSource}) {
		t.Errorf("the terminal carries %v, want only %s, which the one default service declares", got, standInSource)
	}
	if !slices.Equal(result.Grant.Services, []string{"source"}) {
		t.Errorf("granted %v, want the default service alone", result.Grant.Services)
	}
	if !slices.Equal(result.Grant.Withheld, []string{"database", "payments", "errors"}) {
		t.Errorf("withheld %v, want every service the manifest does not mark default", result.Grant.Withheld)
	}
}

func TestAStoredNameNoServiceDeclaresReachesNoTerminal(t *testing.T) {
	everything := Need{Services: []string{"database", "payments", "errors", "source"}}
	result := newStandIns(t).preflight(t, everything)

	if got := carried(result.Env); slices.Contains(got, standInStray) {
		t.Errorf("the terminal carries %v, want no name that no service declares", got)
	}
	if !slices.Equal(result.Undeclared, []string{standInStray}) {
		t.Errorf("undeclared = %v, want the one stored name no service declares, by name", result.Undeclared)
	}
}

func TestAProjectWithNoManifestGivesItsStoredNamesToNoTerminal(t *testing.T) {
	result := newStandIns(t, []Service{}...).preflight(t, Need{IsUnstated: true})

	if got := carried(result.Env); len(got) != 0 {
		t.Errorf("the terminal carries %v, want nothing: no manifest declares a service to name", got)
	}
	if len(result.Undeclared) != 5 {
		t.Errorf("undeclared = %v, want the five names stored in the project's scope", result.Undeclared)
	}
}

func TestANeedNamingAServiceTheManifestDoesNotDeclareIsNotGranted(t *testing.T) {
	result := newStandIns(t).preflight(t, Need{Services: []string{"database", "billing"}})

	if !slices.Equal(result.Grant.Unknown, []string{"billing"}) {
		t.Errorf("unknown = %v, want the one name the manifest does not declare", result.Grant.Unknown)
	}
	if got := carried(result.Env); !slices.Equal(got, []string{standInDatabase}) {
		t.Errorf("the terminal carries %v, want the declared service's name and nothing for the unknown one", got)
	}
}

// A refresh rebuilds a terminal's credential script from the store alone. It
// used to write every name stored in the project's scope, so a service taken
// out of the manifest kept reaching every terminal as a name no service
// declares.
func TestARefreshWritesOnlyWhatTheTaskCarries(t *testing.T) {
	standIn := newStandIns(t)
	manifest, err := LoadManifest(standIn.dataDir, standIn.project)
	if err != nil {
		t.Fatal(err)
	}

	env, err := StoredEnv(standIn.store, "ledger", manifest.Only([]string{"payments"}))

	if err != nil {
		t.Fatalf("StoredEnv: %v", err)
	}
	if got := carried(env); !slices.Equal(got, []string{standInPayments, standInWebhook}) {
		t.Errorf("a refresh writes %v, want only the two names payments declares", got)
	}
}

func TestAServiceTakenOutOfTheManifestStopsReachingATerminal(t *testing.T) {
	standIn := newStandIns(t,
		Service{Name: "database", Method: MethodEnv, Env: []string{standInDatabase}},
	)
	manifest, err := LoadManifest(standIn.dataDir, standIn.project)
	if err != nil {
		t.Fatal(err)
	}

	env, err := StoredEnv(standIn.store, "ledger", manifest.Only([]string{"database"}))

	if err != nil {
		t.Fatalf("StoredEnv: %v", err)
	}
	if got := carried(env); !slices.Equal(got, []string{standInDatabase}) {
		t.Errorf("a refresh writes %v, want %s alone: payments and errors are stored but no longer declared", got, standInDatabase)
	}
}

// An identity check's verdict is about the value, so a name one service
// proved wrong is refused for a sibling that declares it too, even when the
// task names only the sibling.
func TestAServiceTheTaskDoesNotNameStillRefusesANameItProvedWrong(t *testing.T) {
	standIn := newStandIns(t,
		Service{Name: "database", Method: MethodEnv, Env: []string{standInDatabase},
			Identity: &Identity{Var: standInDatabase, Expect: "another-instance"}},
		Service{Name: "reports", Method: MethodEnv, Env: []string{standInDatabase}},
		Service{Name: "errors", Method: MethodEnv, Env: []string{standInErrors}},
	)

	result := standIn.preflight(t, Need{Services: []string{"reports"}})

	if got := carried(result.Env); len(got) != 0 {
		t.Errorf("the terminal carries %v, want nothing: database proved the value names another instance", got)
	}
	if !strings.Contains(result.Refusal, "database") {
		t.Errorf("refusal = %q, want the service that proved it named", result.Refusal)
	}
}

// Only the services whose verdict can change what the task carries are
// probed, so a task is never held up, and no credential is used, for a
// service it does not name.
func TestOnlyTheServicesATaskCarriesAreProbed(t *testing.T) {
	standIn := newStandIns(t,
		Service{Name: "database", Method: MethodEnv, Env: []string{standInDatabase}, Probe: []string{"probe-database"}},
		Service{Name: "payments", Method: MethodEnv, Env: []string{standInPayments}, Probe: []string{"probe-payments"}},
	)
	runner := gitIgnoresEverything()
	runner.results["probe-database"] = execx.Result{ExitCode: 0}
	runner.results["probe-payments"] = execx.Result{ExitCode: 1, Stderr: []byte("401 unauthorized")}

	result, err := SpawnPreflight{DataDir: standIn.dataDir, Runner: runner}.Preflight(context.Background(), standIn.project, Need{Services: []string{"database"}})

	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if _, isProbed := runner.call("probe-payments"); isProbed {
		t.Error("payments was probed for a task that does not name it")
	}
	if _, isProbed := runner.call("probe-database"); !isProbed {
		t.Error("database was not probed for the task that names it")
	}
	if result.Refusal != "" {
		t.Errorf("refusal = %q, want a red service the task does not name to hold nothing up", result.Refusal)
	}
}

// The store is one road into a terminal and the user's own environment is
// another: a terminal starts from the variables Windows gives the user's
// processes. The preflight names every variable of a withheld service, so the
// launch can leave it out wherever it came from.
func TestThePreflightNamesEveryVariableATaskMustNotFindInItsTerminal(t *testing.T) {
	result := newStandIns(t).preflight(t, Need{Services: []string{"database"}})

	want := []string{standInErrors, standInPayments, standInWebhook, standInSource, standInStray}
	if !slices.Equal(result.WithheldNames, want) {
		t.Errorf("withheld names = %v, want %v: every name of a withheld service and the undeclared stored one", result.WithheldNames, want)
	}
}

func TestANameAServiceTheTaskCarriesDeclaresIsNeverWithheld(t *testing.T) {
	standIn := newStandIns(t,
		Service{Name: "database", Method: MethodEnv, Env: []string{standInDatabase}},
		Service{Name: "reports", Method: MethodEnv, Env: []string{standInDatabase, standInErrors}, Aliases: map[string][]string{standInErrors: {standInSource}}},
	)

	result := standIn.preflight(t, Need{Services: []string{"database"}})

	want := []string{standInErrors, standInPayments, standInWebhook, standInSource, standInStray}
	if !slices.Equal(result.WithheldNames, want) {
		t.Errorf("withheld names = %v, want %v: reports' own name and its alias, the undeclared stored names, and never the name database declares too", result.WithheldNames, want)
	}
}

func TestTheWithheldLineNamesServicesAndTheGrantCommandAndNoValue(t *testing.T) {
	result := newStandIns(t).preflight(t, Need{IsUnstated: true})

	line := WithheldLine("task-7", "ledger", result)

	for _, want := range []string{"database", "payments", "errors", "cfo auth grant task-7 <service>", standInStray} {
		if !strings.Contains(line, want) {
			t.Errorf("line = %q, want %q in it", line, want)
		}
	}
	if strings.ContainsAny(line, ";\n") {
		t.Errorf("line = %q, want one line without a semicolon", line)
	}
	holdsNoValue(t, "the withheld line", line+result.Warning+result.Refusal)
}

func TestTheWithheldLineIsEmptyWhenNothingIsWithheld(t *testing.T) {
	result := Result{Grant: Grant{Services: []string{"database"}}}

	if line := WithheldLine("task-7", "ledger", result); line != "" {
		t.Errorf("line = %q, want nothing to say when the task carries all there is", line)
	}
}

func TestNeedFromBrief(t *testing.T) {
	cases := []struct {
		name  string
		brief string
		want  Need
	}{
		{"names services", "## Authentication\n\ncredentials: database, payments\n\n## Delivery\n", Need{Services: []string{"database", "payments"}}},
		{"says none", "## Authentication\n\ncredentials: none\n", Need{}},
		{"none in any case", "## Authentication\r\n\r\nCredentials: None\r\n", Need{}},
		{"names one twice", "## Authentication\ncredentials: database, database\n", Need{Services: []string{"database"}}},
		{"the older form", "## Authentication\n\nServices this task needs are declared in data\\projects\\ledger\\auth.json.\nRun `cfo auth ledger --fix` before dispatch; add any service the task needs that the manifest does not list yet.\n", Need{IsUnstated: true}},
		{"no section at all", "# Brief\n\n## Task\n\nBuild it.\n", Need{IsUnstated: true}},
		{"a line in another section is not read", "## Task\n\ncredentials: payments\n\n## Authentication\n\nUse the project's configured authentication preflight before dispatch.\n", Need{IsUnstated: true}},
		{"a services line under Delivery is not read", "## Authentication\n\ncredentials: database\n\n## Delivery\n\nservices: needed\n", Need{Services: []string{"database"}}},
		{"names MCP servers beside its services", "## Authentication\n\ncredentials: database\nmcp: neon, supabase\n", Need{Services: []string{"database"}, MCPServers: []string{"neon", "supabase"}}},
		{"says no MCP server", "## Authentication\n\ncredentials: none\nMCP: None\n", Need{}},
		{"names an MCP server in the older form", "## Authentication\n\nmcp: neon\n", Need{IsUnstated: true, MCPServers: []string{"neon"}}},
		{"an mcp line in another section is not read", "## Task\n\nmcp: neon\n\n## Authentication\n\ncredentials: none\n", Need{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NeedFromBrief(tc.brief)
			if err != nil {
				t.Fatalf("NeedFromBrief: %v", err)
			}
			if got.IsUnstated != tc.want.IsUnstated || !slices.Equal(got.Services, tc.want.Services) || !slices.Equal(got.MCPServers, tc.want.MCPServers) {
				t.Errorf("need = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestNeedFromBriefRefusesALineItCannotRead(t *testing.T) {
	cases := []struct {
		name  string
		brief string
		want  string
	}{
		{"an empty list", "## Authentication\ncredentials:\n", "none"},
		{"none beside a service", "## Authentication\ncredentials: none, database\n", "none"},
		{"an empty name", "## Authentication\ncredentials: database,, payments\n", "empty"},
		{"two lines", "## Authentication\ncredentials: database\ncredentials: payments\n", "one credentials line"},
		{"an empty server list", "## Authentication\nmcp:\n", "mcp: none"},
		{"none beside a server", "## Authentication\nmcp: none, neon\n", "beside a server"},
		{"two mcp lines", "## Authentication\nmcp: neon\nmcp: supabase\n", "one mcp line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NeedFromBrief(tc.brief)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

func TestTheUnknownLineNamesWhatIsDeclaredAndNeverRepeatsAValue(t *testing.T) {
	manifest := Manifest{Project: "ledger", Services: []Service{
		{Name: "payments", Method: MethodEnv, Env: []string{standInPayments}},
		{Name: "database", Method: MethodEnv, Env: []string{standInDatabase}},
	}}
	pasted := "sk_live_" + strings.Repeat("a1B2", 8)

	line := UnknownLine(filepath.Join("projects", "ledger"), manifest.Grant(Need{Services: []string{"billing", pasted}}))

	for _, want := range []string{"ledger", "billing", "database, payments", "shaped like a credential value"} {
		if !strings.Contains(line, want) {
			t.Errorf("line = %q, want %q in it", line, want)
		}
	}
	if strings.Contains(line, pasted) {
		t.Error("the line repeats what looks like a credential value")
	}
}

func TestAManifestRefusesAServiceABriefCouldNotName(t *testing.T) {
	for _, name := range []string{"none", "None", "pay,ments"} {
		manifest := Manifest{Project: "ledger", Services: []Service{{Name: name, Method: MethodEnv, Env: []string{standInPayments}}}}
		if err := manifest.Validate(); err == nil {
			t.Errorf("a service named %q validated, want it refused: a brief could not name it", name)
		}
	}
}

// everyService is the need of a task that names every service its project's
// manifest declares, which is what every terminal carried before a brief
// named its services.
func everyService(t *testing.T, dataDir, project string) Need {
	t.Helper()
	manifest, err := LoadManifest(dataDir, project)
	if err != nil {
		t.Fatalf("load the manifest to name its services: %v", err)
	}
	var need Need
	for _, service := range manifest.Services {
		need.Services = append(need.Services, service.Name)
	}
	return need
}
