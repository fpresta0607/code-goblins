package supervisor

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/installbase"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const removeManifest = `{"project":"acme","services":[{"name":"keyed","method":"env","env":["ACME_KEY"]},{"name":"other","method":"env","env":["OTHER_KEY"]}]}`

// removeBoard is a supervisor whose saved snapshot lists the credential
// ACME_API_KEY of this PC's user environment, stood in by a file, and the tool
// gh; the made-up project acme, with the services keyed and other its manifest
// declares and the MCP server docs of its .mcp.json; and task-1, a goblin of
// acme whose session failed to load held, which its record gives it, docs,
// which every goblin of acme gets, and a server of his Claude account. It
// returns the service, its manifest's path and the user environment's file.
func removeBoard(t *testing.T) (*Service, string, string) {
	t.Helper()
	t.Setenv(auth.StoreDirEnv, t.TempDir())
	userEnv := filepath.Join(t.TempDir(), "user-env.json")
	t.Setenv(installbase.UserEnvFileVariable, userEnv)
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.MCPServers = []string{"held"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	checked := time.Now().Add(-time.Minute)
	item := func(level, group, kind, name, status string) connections.Item {
		return connections.Item{ID: kind + ":" + name, Level: level, Group: group, Kind: kind, Name: name, Status: status, CheckedAt: checked}
	}
	failed := func(name string) connections.Item {
		return connections.Item{ID: "failed:" + name, Level: connections.LevelGoblin, Group: connections.GroupFailed, Kind: "mcp", Name: name, Status: connections.StatusFailed, CheckedAt: checked}
	}
	snapshots := map[string]any{
		"machine.json": connections.Level{CheckedAt: checked, Items: []connections.Item{
			item(connections.LevelPC, connections.GroupEnvironment, "credential", "ACME_API_KEY", connections.StatusEverywhere),
			item(connections.LevelPC, connections.GroupCLI, "cli", "gh", connections.StatusNotChecked),
		}},
		filepath.Join("projects", "acme.json"): connections.Project{Name: "acme", Path: meta.Project, Level: connections.Level{CheckedAt: checked, Items: []connections.Item{
			item(connections.LevelProject, connections.GroupService, "service", "keyed", connections.StatusFailed),
			item(connections.LevelProject, connections.GroupService, "service", "other", connections.StatusFailed),
			item(connections.LevelProject, connections.GroupMCP, "mcp", "docs", connections.StatusMissing),
		}}},
		filepath.Join("goblins", "task-1.json"): connections.Goblin{Task: "task-1", Generation: "g1", Project: "acme", Harness: meta.Harness, IsConfirmed: true, Level: connections.Level{CheckedAt: checked, Items: []connections.Item{
			failed("held"), failed("docs"), failed("claude.ai Gmail"),
		}}},
	}
	files := map[string]string{
		auth.ManifestPath(h.Data, "acme"):        removeManifest,
		filepath.Join(meta.Project, ".mcp.json"): `{"mcpServers":{"docs":{"command":"npx"},"held":{"command":"npx","env":{"HELD_MODE":"literal"}}}}`,
		userEnv:                                  `{"ACME_API_KEY":"stand-in-value","PATH":"kept"}`,
	}
	for name, snapshot := range snapshots {
		data, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Join(h.State, connectorsFolder, name)] = string(data)
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := boardService(store)
	connectors := s.connectors()
	t.Cleanup(connectors.Close)
	// A goblin is read again from its record with its session's servers
	// carried forward from what the snapshot holds, as a read from its offset
	// carries them.
	connectors.ReadGoblin = func(meta state.TaskMeta, _ string, _ int64, prior connections.Goblin) (connections.Goblin, int64) {
		return connections.Goblin{Task: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Project: "acme", IsConfirmed: true, Level: connections.Level{Items: prior.Items}}, 0
	}
	connectors.Transcript = func(state.TaskMeta) string { return "" }
	return s, auth.ManifestPath(h.Data, "acme"), userEnv
}

// listedIDs is every item the page lists, the machine's, then the projects',
// then the goblins'.
func listedIDs(s *Service) []string {
	view := s.connectors().Read()
	var ids []string
	for _, item := range view.Machine.Items {
		ids = append(ids, item.ID)
	}
	for _, project := range view.Projects {
		for _, item := range project.Items {
			ids = append(ids, item.ID)
		}
	}
	for _, goblin := range view.Goblins {
		for _, item := range goblin.Items {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

var allListed = []string{"credential:ACME_API_KEY", "cli:gh", "service:keyed", "service:other", "mcp:docs", "failed:held", "failed:docs", "failed:claude.ai Gmail"}

func without(ids []string, gone string) []string {
	return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return id == gone })
}

// Remove from his own board takes the item out of where it is declared and
// out of the page at once, and tells the CEO: a project's service out of its
// auth.json, kept word for word beside it; a credential of this PC out of its
// user environment, its value kept in the vault first; and a server a goblin's
// task record gives it out of that record.
func TestRemoveTakesTheItemOutOfWhereItIsDeclaredAndThePage(t *testing.T) {
	cases := []struct {
		name, body, gone, told string
		changed                func(t *testing.T, s *Service, manifestPath, userEnv string)
	}{
		{
			name: "project's service", gone: "service:keyed", told: "the Overlord removed the service keyed from acme's auth.json",
			body: `{"level":"project","project":"acme","task":"","generation":"","connection":"service:keyed"}`,
			changed: func(t *testing.T, s *Service, manifestPath, _ string) {
				manifest, err := auth.LoadManifest(s.Store.Home.Data, "acme")
				if err != nil || len(manifest.Services) != 1 || manifest.Services[0].Name != "other" {
					t.Fatalf("manifest = %+v (%v), want only other left", manifest.Services, err)
				}
				if kept, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), connections.RemovedFileName)); err != nil || !strings.Contains(string(kept), `"keyed"`) {
					t.Fatalf("removed file = %s (%v), want keyed kept", kept, err)
				}
			},
		},
		{
			name: "credential of this PC", gone: "credential:ACME_API_KEY", told: "the Overlord removed ACME_API_KEY from this PC's user environment",
			body: `{"level":"pc","project":"","task":"","generation":"","connection":"credential:ACME_API_KEY"}`,
			changed: func(t *testing.T, _ *Service, _, userEnv string) {
				if data, err := os.ReadFile(userEnv); err != nil || strings.Contains(string(data), "ACME_API_KEY") || !strings.Contains(string(data), "PATH") {
					t.Fatalf("user environment = %s (%v), want only ACME_API_KEY gone", data, err)
				}
				vault, err := auth.OpenStore()
				if err != nil {
					t.Fatal(err)
				}
				if value, isStored, err := vault.Get(auth.Shared("ACME_API_KEY")); err != nil || !isStored || value != "stand-in-value" {
					t.Fatalf("vault holds it = %v (%v), want its value kept", isStored, err)
				}
			},
		},
		{
			name: "goblin's server", gone: "failed:held", told: "the Overlord removed held from task-1's task record",
			body: `{"level":"goblin","project":"acme","task":"task-1","generation":"g1","connection":"failed:held"}`,
			changed: func(t *testing.T, s *Service, _, _ string) {
				if meta, err := state.ReadTaskMeta(s.Store.Home.State, "task-1"); err != nil || len(meta.MCPServers) != 0 {
					t.Fatalf("task record gives %v (%v), want held taken back", meta.MCPServers, err)
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s, manifestPath, userEnv := removeBoard(t)
			asOverlordsBoard(s)

			// Act
			code, body := askTheBoard(t, s, "POST", "/api/connectors/remove", test.body, nil)

			// Assert
			if code != http.StatusOK || strings.Contains(body, `"`+test.gone+`"`) || strings.Contains(body, "stand-in-value") {
				t.Fatalf("remove = %d %s, want 200 and the snapshots without %s", code, body, test.gone)
			}
			test.changed(t, s, manifestPath, userEnv)
			if ids := listedIDs(s); !slices.Equal(ids, without(allListed, test.gone)) {
				t.Fatalf("listed = %v, want only %s gone", ids, test.gone)
			}
			pending, err := wake.Pending(s.Store.Home.State)
			if err != nil || !slices.ContainsFunc(pending, func(record wake.Record) bool {
				return strings.Contains(record.Detail, test.told) && strings.Contains(record.Detail, "his own board") && !strings.Contains(record.Detail, "stand-in-value")
			}) {
				t.Fatalf("wakes = %+v (%v), want the CEO told", pending, err)
			}
		})
	}
}

// A Remove that is refused says why and changes nothing: a board that is not
// his own, a body with another field, a project the page does not list, a
// service the manifest no longer declares, an MCP server of the project's
// repository, a credential whose name the vault holds another value under, a
// goblin that restarted, a server every goblin of the project gets, one of his
// Claude account, and an item Code Goblins does not declare.
func TestRemoveRefusesAndChangesNothing(t *testing.T) {
	cases := []struct {
		name, body, vault, want string
		isHisBoard              bool
		code                    int
	}{
		{name: "not his board", body: `{"level":"project","project":"acme","connection":"service:keyed"}`, want: "Remove it in the Code Goblins window", code: http.StatusForbidden},
		{name: "another field", body: `{"level":"project","project":"acme","connection":"service:keyed","value":"x"}`, want: "Choose a connector to remove.", isHisBoard: true, code: http.StatusBadRequest},
		{name: "project not listed", body: `{"level":"project","project":"globex","connection":"service:keyed"}`, want: "This project is not listed.", isHisBoard: true, code: http.StatusConflict},
		{name: "service not declared", body: `{"level":"project","project":"acme","connection":"service:gone"}`, want: "no longer declares gone", isHisBoard: true, code: http.StatusConflict},
		{name: "repository's MCP server", body: `{"level":"project","project":"acme","connection":"mcp:docs"}`, want: "docs is declared in acme's .mcp.json", isHisBoard: true, code: http.StatusConflict},
		{name: "another value in the vault", body: `{"level":"pc","connection":"credential:ACME_API_KEY"}`, vault: "other-stand-in", want: "already holds another value under ACME_API_KEY", isHisBoard: true, code: http.StatusConflict},
		{name: "credential not listed", body: `{"level":"pc","connection":"credential:OTHER_API_KEY"}`, want: "OTHER_API_KEY is not listed", isHisBoard: true, code: http.StatusConflict},
		{name: "goblin restarted", body: `{"level":"goblin","task":"task-1","generation":"g0","connection":"failed:held"}`, want: "Task restarted", isHisBoard: true, code: http.StatusConflict},
		{name: "server of the project", body: `{"level":"goblin","task":"task-1","generation":"g1","connection":"failed:docs"}`, want: "docs comes to every goblin of", isHisBoard: true, code: http.StatusConflict},
		{name: "server of his account", body: `{"level":"goblin","task":"task-1","generation":"g1","connection":"failed:claude.ai Gmail"}`, want: "comes from your Claude settings or account", isHisBoard: true, code: http.StatusConflict},
		{name: "item not declared by Code Goblins", body: `{"level":"pc","connection":"cli:gh"}`, want: "Code Goblins does not declare this item", isHisBoard: true, code: http.StatusConflict},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s, manifestPath, userEnv := removeBoard(t)
			if test.isHisBoard {
				asOverlordsBoard(s)
			}
			vault, err := auth.OpenStore()
			if err != nil {
				t.Fatal(err)
			}
			if test.vault != "" {
				if err := vault.Set(auth.Shared("ACME_API_KEY"), test.vault); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			code, body := askTheBoard(t, s, "POST", "/api/connectors/remove", test.body, nil)

			// Assert
			if code != test.code || !strings.Contains(body, test.want) || strings.Contains(body, "stand-in") {
				t.Fatalf("remove = %d %s, want %d %q and no value", code, body, test.code, test.want)
			}
			if manifest, err := os.ReadFile(manifestPath); err != nil || string(manifest) != removeManifest {
				t.Fatalf("manifest = %s (%v), want it unchanged", manifest, err)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(manifestPath), connections.RemovedFileName)); !os.IsNotExist(err) {
				t.Fatalf("removed file stat = %v, want none written", err)
			}
			if data, err := os.ReadFile(userEnv); err != nil || !strings.Contains(string(data), "ACME_API_KEY") {
				t.Fatalf("user environment = %s (%v), want it unchanged", data, err)
			}
			if value, _, err := vault.Get(auth.Shared("ACME_API_KEY")); err != nil || value != test.vault {
				t.Fatalf("the vault's value changed (%v)", err)
			}
			if meta, err := state.ReadTaskMeta(s.Store.Home.State, "task-1"); err != nil || !slices.Equal(meta.MCPServers, []string{"held"}) {
				t.Fatalf("task record gives %v (%v), want it unchanged", meta.MCPServers, err)
			}
			if ids := listedIDs(s); !slices.Equal(ids, allListed) {
				t.Fatalf("listed = %v, want all kept", ids)
			}
		})
	}
}
