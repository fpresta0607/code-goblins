package connections

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

const acmeCLIServices = `[{"name":"hosting","method":"cli","probe":["acme-cli","status"]},{"name":"repo","method":"cli","probe":["gh","api","user"]}]`

// A tool's sign-in is what its status command's exit code says and nothing it
// printed: a failing command that prints a welcome and a passing one that
// prints a refusal read the same as the exit codes say.
func TestMachineCheckReadsASignInFromTheExitCodeAlone(t *testing.T) {
	cases := []struct {
		name       string
		isMissing  bool
		result     execx.Result
		err        error
		wantStatus string
		wantDetail string
	}{
		{name: "exit zero", result: execx.Result{Stdout: []byte("not logged in to anything")}, wantStatus: StatusVerified, wantDetail: "Signed in"},
		{name: "exit one", result: execx.Result{ExitCode: 1, Stdout: []byte("Logged in as acme-user")}, wantStatus: StatusUnauthorized, wantDetail: "Not signed in"},
		{name: "timed out", err: context.DeadlineExceeded, wantStatus: StatusNotChecked, wantDetail: "did not answer in 20 s"},
		{name: "could not start", err: errors.New("exec failed"), wantStatus: StatusNotChecked, wantDetail: "could not run"},
		{name: "not installed", isMissing: true, wantStatus: StatusMissing, wantDetail: "Not installed"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			e := newTestEnvironment(t)
			e.manifest(t, "acme", `[{"name":"repo","method":"cli","probe":["gh","api","user"]}]`)
			e.missing["gh"] = test.isMissing
			e.runner.respond = func(execx.Request) (execx.Result, error) { return test.result, test.err }

			// Act
			level := e.CheckMachine(t.Context())

			// Assert
			item := find(t, level.Items, "cli:gh")
			if item.Status != test.wantStatus || !strings.Contains(item.Detail, test.wantDetail) {
				t.Fatalf("gh = %q %q, want %q containing %q", item.Status, item.Detail, test.wantStatus, test.wantDetail)
			}
			if item.Level != LevelPC || item.Group != GroupCLI {
				t.Fatalf("gh is %q in %q, want the PC level's command line tools", item.Level, item.Group)
			}
		})
	}
}

// A tool several projects name is checked once for the whole PC, and a tool
// with no status command of its own is installed or not, with no process.
func TestMachineCheckRunsEachToolOnceAndStartsNothingForOneWithoutAStatusCommand(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	e.manifest(t, "acme", acmeCLIServices)
	e.manifest(t, "globex", `[{"name":"repo","method":"cli","probe":["gh","api","user"]},{"name":"hosting","method":"cli","login":["acme-cli","login"]}]`)

	// Act
	level := e.CheckMachine(t.Context())

	// Assert
	if got := e.runner.ran("gh"); got != 1 {
		t.Fatalf("gh ran %d times for two projects, want once", got)
	}
	if got := e.runner.ran("acme-cli"); got != 0 {
		t.Fatalf("acme-cli ran %d times, want none: it has no sign-in status command", got)
	}
	if item := find(t, level.Items, "cli:acme-cli"); item.Status != StatusSet {
		t.Fatalf("acme-cli = %q, want it set as installed", item.Status)
	}
	var count int
	for _, item := range level.Items {
		if item.ID == "cli:gh" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%d gh items, want one", count)
	}
}

func TestMachineCheckReadsTheHarnessesSignIns(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	e.missing["pi"] = true
	e.runner.respond = func(request execx.Request) (execx.Result, error) {
		return execx.Result{ExitCode: map[string]int{"claude": 0, "codex": 1}[request.Name]}, nil
	}

	// Act
	level := e.CheckMachine(t.Context())

	// Assert
	want := map[string]string{"harness:claude": StatusVerified, "harness:codex": StatusUnauthorized, "harness:pi": StatusMissing}
	for id, status := range want {
		if got := find(t, level.Items, id); got.Status != status || got.Level != LevelAccount || got.Group != GroupHarness {
			t.Errorf("%s = %q in %q, want %q on the account level's harnesses", id, got.Status, got.Group, status)
		}
	}
}

// The names of the user's own Claude servers and of the connectors its account
// holds are read, and the servers' own fields are never decoded.
func TestMachineCheckNamesTheUserConfigServersAndAccountConnectors(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	writeFile(t, filepath.Join(e.UserHome, ".claude.json"), `{
		"claudeAiMcpEverConnected": ["Globex Mail", "Acme Docs", "Acme Docs"],
		"mcpServers": {"zeta": {"command": "zeta-server", "env": {"ZETA_KEY": "zeta-secret-value"}}, "alpha": {"url": "https://alpha.example/mcp"}},
		"projects": {"anything": {"mcpServers": {"hidden": {}}}}
	}`)

	// Act
	level := e.CheckMachine(t.Context())

	// Assert
	var connectors, servers []string
	for _, item := range level.Items {
		switch item.Group {
		case GroupAccountConnector:
			connectors = append(connectors, item.Name)
			if item.Level != LevelAccount || item.Status != StatusNotGiven {
				t.Errorf("%s = %q on %q, want an account connector goblins are not given", item.ID, item.Status, item.Level)
			}
		case GroupUserConfig:
			servers = append(servers, item.Name)
		}
	}
	if strings.Join(connectors, ",") != "Acme Docs,Globex Mail" || strings.Join(servers, ",") != "alpha,zeta" {
		t.Fatalf("connectors %v and servers %v, want each name once and sorted", connectors, servers)
	}
	if strings.Contains(asJSON(t, level), "zeta-secret-value") {
		t.Fatal("a server's environment value reached the level")
	}
}

func TestMachineCheckNamesTheCodexUserConfigServersFromTheirHeadersOnly(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	writeFile(t, filepath.Join(e.UserHome, ".codex", "config.toml"), "model = \"x\"\n[mcp_servers.acme-docs]\ncommand = \"acme-docs\"\n[mcp_servers.acme-docs.env]\nACME_KEY = \"codex-secret-value\"\n[mcp_servers.globex_db]\nurl = \"https://db.globex.example\"\n")

	// Act
	level := e.CheckMachine(t.Context())

	// Assert
	var names []string
	for _, item := range level.Items {
		if strings.HasPrefix(item.ID, "codex:") {
			names = append(names, item.Name)
			if item.Group != GroupUserConfig || item.Status != StatusSet {
				t.Errorf("%s = %q in %q, want a set user config server", item.ID, item.Status, item.Group)
			}
		}
	}
	if strings.Join(names, ",") != "acme-docs,globex_db" {
		t.Fatalf("codex servers %v, want the two declared and not the table of one's environment", names)
	}
	if strings.Contains(asJSON(t, level), "codex-secret-value") {
		t.Fatal("a server's environment value reached the level")
	}
}

// The environment names come from the injected registry read: a credential-like
// name is shown for every terminal, a name that is not one or is a harness's
// billing key is not, and the projects that declare one are named.
func TestMachineCheckListsCredentialLikeEnvironmentNamesFromTheRegistryRead(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	e.manifest(t, "acme", `[{"name":"db","method":"env","env":["ACME_DB_TOKEN"]}]`)
	e.userEnv = []string{"PATH", "ACME_DB_TOKEN", "GLOBEX_API_KEY", "OPENAI_API_KEY", "Not Valid TOKEN", "ACME_DB_TOKEN"}

	// Act
	level := e.CheckMachine(t.Context())

	// Assert
	var names []string
	for _, item := range level.Items {
		if item.Group == GroupEnvironment {
			names = append(names, item.Name)
		}
	}
	if strings.Join(names, ",") != "ACME_DB_TOKEN,GLOBEX_API_KEY" {
		t.Fatalf("environment names %v, want the two credential-like names once", names)
	}
	declared, undeclared := find(t, level.Items, "credential:ACME_DB_TOKEN"), find(t, level.Items, "credential:GLOBEX_API_KEY")
	if declared.Status != StatusEverywhere || !strings.Contains(declared.Detail, "Declared by acme.") || !strings.Contains(undeclared.Detail, "No project declares it.") {
		t.Fatalf("details %q and %q, want the declaring project named and the other said undeclared", declared.Detail, undeclared.Detail)
	}
}

func TestMachineCheckKeepsWhatItCouldReadWhenAFileOrTheRegistryCannotBeRead(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	writeFile(t, filepath.Join(e.UserHome, ".claude.json"), "{not json")
	e.userEnvErr = errors.New("registry unavailable")

	// Act
	level := e.CheckMachine(t.Context())

	// Assert
	if !strings.Contains(level.Error, "Claude's own settings could not be read.") || !strings.Contains(level.Error, "user environment could not be read.") {
		t.Fatalf("error = %q, want both reads said to have failed", level.Error)
	}
	find(t, level.Items, "harness:claude")
}
