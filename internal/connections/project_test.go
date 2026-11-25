package connections

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// A project's .mcp.json servers read by how a goblin of the project gets each,
// and each says what it points at: a Supabase project by the last four
// characters of its reference, however the reference is given, else its host,
// else a command on this PC.
func TestProjectCheckClassifiesMCPServersAndNamesWhatEachPointsAt(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	e.manifest(t, "acme", `[{"name":"tokens","method":"env","env":["ACME_MCP_TOKEN"]}]`)
	path := e.checkout(t, "acme")
	writeFile(t, path+"/.mcp.json", `{"mcpServers":{
		"local-tools": {"command": "acme-tools", "args": ["--quiet"]},
		"signin-docs": {"url": "https://docs.acme.example/mcp"},
		"query-ref": {"url": "https://mcp.supabase.example/mcp?project_ref=abcdwxyz1234"},
		"joined-ref": {"command": "supabase-mcp", "args": ["--project-ref=qrstuv5555"]},
		"split-ref": {"command": "supabase-mcp", "args": ["--project-ref", "ghijkl9876"]},
		"keyed-api": {"url": "https://api.globex.example/mcp", "bearerTokenEnvVar": "ACME_MCP_TOKEN"},
		"unstored-api": {"url": "https://api.acme.example/mcp", "bearerTokenEnvVar": "OTHER_MCP_TOKEN"},
		"valued": {"command": "acme-valued", "env": {"ACME_MODE": "literal-value"}}
	}}`)
	e.store(t, "acme", "ACME_MCP_TOKEN", "stored-token-value")

	// Act
	level := e.CheckProject(t.Context(), "acme", path)

	// Assert
	want := map[string]struct{ status, target string }{
		"mcp:local-tools":  {StatusSet, "local command"},
		"mcp:signin-docs":  {StatusNotGiven, "docs.acme.example"},
		"mcp:query-ref":    {StatusNotGiven, "Supabase project …1234"},
		"mcp:joined-ref":   {StatusSet, "Supabase project …5555"},
		"mcp:split-ref":    {StatusSet, "Supabase project …9876"},
		"mcp:keyed-api":    {StatusSet, "api.globex.example"},
		"mcp:unstored-api": {StatusMissing, "api.acme.example"},
		"mcp:valued":       {StatusSet, "local command"},
	}
	for id, expected := range want {
		item := find(t, level.Items, id)
		if item.Status != expected.status || item.Target != expected.target || item.Level != LevelProject || item.Group != GroupMCP {
			t.Errorf("%s = %q pointing at %q, want %q pointing at %q", id, item.Status, item.Target, expected.status, expected.target)
		}
	}
	if valued := find(t, level.Items, "mcp:valued"); !strings.Contains(valued.Detail, "brief names it") {
		t.Errorf("valued detail = %q, want it held for a brief that names it", valued.Detail)
	}
	if signin := find(t, level.Items, "mcp:signin-docs"); !strings.Contains(signin.Detail, "Sign-in only") {
		t.Errorf("sign-in detail = %q, want it said to be a browser sign-in", signin.Detail)
	}
	if strings.Contains(asJSON(t, level), "literal-value") {
		t.Fatal("a value written into a server's entry reached the level")
	}
}

// A server whose token variable is not stored offers to store it only when the
// project's manifest declares that name.
func TestProjectCheckOffersToStoreAMissingMCPTokenOnlyWhenTheManifestDeclaresIt(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	e.manifest(t, "acme", `[{"name":"tokens","method":"env","env":["ACME_MCP_TOKEN"]}]`)
	path := e.checkout(t, "acme")
	writeFile(t, path+"/.mcp.json", `{"mcpServers":{
		"declared": {"url": "https://a.acme.example/mcp", "bearerTokenEnvVar": "ACME_MCP_TOKEN"},
		"undeclared": {"url": "https://b.acme.example/mcp", "bearerTokenEnvVar": "LOOSE_MCP_TOKEN"}
	}}`)

	// Act
	level := e.CheckProject(t.Context(), "acme", path)

	// Assert
	if got := find(t, level.Items, "mcp:declared").Actions; !slices.Equal(got, []string{"store:ACME_MCP_TOKEN"}) {
		t.Errorf("declared actions = %v, want to store its token", got)
	}
	if got := find(t, level.Items, "mcp:undeclared").Actions; len(got) != 0 {
		t.Errorf("undeclared actions = %v, want none for a name no manifest declares", got)
	}
}

// A server whose token variable this PC's user environment sets is given to
// every goblin, as spawn gives it, though the project stores no such token and
// its manifest declares no service; a harness billing key there counts as none.
func TestProjectCheckCountsAnMCPTokenTheUserEnvironmentSets(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	path := e.checkout(t, "acme")
	writeFile(t, path+"/.mcp.json", `{"mcpServers":{
		"env-api": {"url": "https://api.acme.example/mcp", "bearerTokenEnvVar": "ACME_USER_TOKEN"},
		"billed-api": {"url": "https://billed.acme.example/mcp", "bearerTokenEnvVar": "ANTHROPIC_API_KEY"},
		"unset-api": {"url": "https://other.acme.example/mcp", "bearerTokenEnvVar": "ACME_UNSET_TOKEN"}
	}}`)
	e.userEnv = []string{"Acme_User_Token", "ANTHROPIC_API_KEY"}

	// Act
	level := e.CheckProject(t.Context(), "acme", path)

	// Assert
	for id, want := range map[string]string{"mcp:env-api": StatusSet, "mcp:billed-api": StatusMissing, "mcp:unset-api": StatusMissing} {
		if got := find(t, level.Items, id).Status; got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
	if level.Error != "" {
		t.Errorf("error = %q, want none", level.Error)
	}
}

// A user environment that cannot be read says so on the project's level.
func TestProjectCheckSaysWhenTheUserEnvironmentCouldNotBeRead(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	path := e.checkout(t, "acme")
	writeFile(t, path+"/.mcp.json", `{"mcpServers":{"env-api": {"url": "https://api.acme.example/mcp", "bearerTokenEnvVar": "ACME_USER_TOKEN"}}}`)
	e.userEnvErr = os.ErrPermission

	// Act
	level := e.CheckProject(t.Context(), "acme", path)

	// Assert
	if !strings.Contains(level.Error, "user environment could not be read") {
		t.Errorf("error = %q, want it to say the user environment could not be read", level.Error)
	}
	if got := find(t, level.Items, "mcp:env-api").Status; got != StatusMissing {
		t.Errorf("env-api = %q, want missing", got)
	}
}

func TestProjectCheckSaysWhatEachServiceIs(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	e.manifest(t, "acme", `[
		{"name":"stored","method":"env","env":["ACME_STORED"]},
		{"name":"absent","method":"env","env":["ACME_ABSENT"]},
		{"name":"probed","method":"cli","probe":["acme-cli","status"],"url":"https://login.acme.example/start"},
		{"name":"rejected","method":"cli","probe":["acme-cli","whoami"],"login":["acme-cli","login"]}
	]`)
	path := e.checkout(t, "acme")
	e.store(t, "acme", "ACME_STORED", "stored-value")
	e.runner.respond = func(request execx.Request) (execx.Result, error) {
		if len(request.Args) > 0 && request.Args[0] == "whoami" {
			return execx.Result{ExitCode: 1, Stderr: []byte("unauthorized")}, nil
		}
		return execx.Result{}, nil
	}

	// Act
	level := e.CheckProject(t.Context(), "acme", path)

	// Assert
	stored := find(t, level.Items, "service:stored")
	if stored.Status != StatusSet || len(stored.Credentials) != 1 || !stored.Credentials[0].IsResolved {
		t.Errorf("stored = %q %+v, want set with its variable resolved", stored.Status, stored.Credentials)
	}
	absent := find(t, level.Items, "service:absent")
	if absent.Status != StatusMissing || absent.Credentials[0].IsResolved || !slices.Equal(absent.Actions, []string{"store:ACME_ABSENT"}) {
		t.Errorf("absent = %q %+v %v, want missing and offered to be stored", absent.Status, absent.Credentials, absent.Actions)
	}
	if probed := find(t, level.Items, "service:probed"); probed.Status != StatusVerified || len(probed.Actions) != 0 {
		t.Errorf("probed = %q %v, want verified with nothing to repair", probed.Status, probed.Actions)
	}
	rejected := find(t, level.Items, "service:rejected")
	if rejected.Status == StatusVerified || !slices.Contains(rejected.Actions, "cli") {
		t.Errorf("rejected = %q %v, want it not verified and its login offered", rejected.Status, rejected.Actions)
	}
}

// The manifest's check runs once for a project however many items it has: each
// service's probe once, whatever the .mcp.json holds.
func TestProjectCheckRunsEachProbeOnce(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	e.manifest(t, "acme", `[{"name":"probed","method":"cli","probe":["acme-cli","status"]},{"name":"other","method":"cli","probe":["acme-cli","other"]}]`)
	path := e.checkout(t, "acme")
	writeFile(t, path+"/.mcp.json", `{"mcpServers":{"a":{"command":"a"},"b":{"command":"b"}}}`)

	// Act
	e.CheckProject(t.Context(), "acme", path)

	// Assert
	if got := e.runner.ran("acme-cli"); got != 2 {
		t.Fatalf("acme-cli ran %d times, want once for each of the two services", got)
	}
}

func TestProjectCheckDoesNotRunWhatItCannotCheckAgainstACheckoutThatIsMissing(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	e.manifest(t, "acme", `[{"name":"probed","method":"cli","probe":["acme-cli","status"]}]`)

	// Act
	level := e.CheckProject(t.Context(), "acme", e.DataDir+"/no-such-checkout")

	// Assert
	item := find(t, level.Items, "service:probed")
	if item.Status != StatusNotChecked || !strings.Contains(item.Detail, "its checkout was not found") || e.runner.total() != 0 {
		t.Fatalf("probed = %q %q after %d commands, want it not checked and nothing run", item.Status, item.Detail, e.runner.total())
	}
}

// Services of two projects with the same name stay two items, each in its own
// project and with the target its own manifest expects.
func TestProjectsKeepSameNamedServicesApartWithTheirOwnTargets(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	for project, host := range map[string]string{"acme": "db.acme.example", "globex": "db.globex.example"} {
		e.manifest(t, project, `[{"name":"supabase","method":"env","env":["DATABASE_URL"],"identity":{"var":"DATABASE_URL","expect":"`+host+`"}}]`)
		e.checkout(t, project)
		e.store(t, project, "DATABASE_URL", "postgres://user@"+host+"/app")
	}
	e.ProjectsRoot = func() string { return filepath.Dir(e.checkout(t, "acme")) }
	snapshots := NewSnapshots(e.Environment, t.TempDir())
	t.Cleanup(snapshots.Close)
	for _, project := range []string{"acme", "globex"} {
		if err := snapshots.Refresh(RefreshProject, project); err != nil {
			t.Fatal(err)
		}
	}
	snapshots.work.Wait()

	// Act
	view := snapshots.Read()

	// Assert
	targets := map[string]string{}
	for _, project := range view.Projects {
		item := find(t, project.Items, "service:supabase")
		targets[project.Name] = item.Target
		if item.Status != StatusSet {
			t.Errorf("%s supabase = %q, want it set", project.Name, item.Status)
		}
	}
	if len(view.Projects) != 2 || targets["acme"] != "db.acme.example" || targets["globex"] != "db.globex.example" {
		t.Fatalf("targets = %v over %d projects, want each project its own", targets, len(view.Projects))
	}
}
