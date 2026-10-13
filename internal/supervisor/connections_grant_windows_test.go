package supervisor

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// grantBoard is a supervisor whose task-1 is in project work, whose manifest
// declares the services sample and other and whose .mcp.json defines docs,
// with its credential store and its connection checks kept in the test.
func grantBoard(t *testing.T) (*Service, string) {
	t.Helper()
	t.Setenv(auth.StoreDirEnv, t.TempDir())
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.TaskTmp = filepath.Join(h.Root, "tasktmp", meta.ID)
	meta.Credentials, meta.HasCredentials = []string{"other"}, true
	if err := os.MkdirAll(meta.TaskTmp, 0700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	manifestPath := auth.ManifestPath(h.Data, meta.Project)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"project":"work","services":[{"name":"sample","method":"env","env":["SAMPLE_TOKEN"]},{"name":"other","method":"env","env":["OTHER_TOKEN"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meta.Project, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"command":"npx","env":{"DOCS_MODE":"literal"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := boardService(store)
	s.connectionChecks = connections.NewCache(time.Minute, time.Second, func(context.Context, string) connections.Snapshot {
		return connections.Snapshot{Entries: []connections.Entry{}}
	})
	t.Cleanup(s.connectionChecks.Close)
	return s, h.State
}

// The + on a connector card gives the task the service or MCP server, and its
// check takes it back, as cfo auth grant and cfo auth revoke do: the task's
// record changes, its status names the Overlord's board, and the CFO is told.
func TestAConnectorCardGivesAndTakesBackAServiceOrAnMCPServer(t *testing.T) {
	cases := []struct {
		name, connection string
		carried          func(state.TaskMeta) []string
		granted, revoked []string
	}{
		{name: "service", connection: "service:sample", carried: func(meta state.TaskMeta) []string { return meta.Credentials }, granted: []string{"other", "sample"}, revoked: []string{"other"}},
		{name: "MCP server", connection: "withheld:docs", carried: func(meta state.TaskMeta) []string { return meta.MCPServers }, granted: []string{"docs"}, revoked: nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s, stateDir := grantBoard(t)
			asOverlordsBoard(s)
			name := test.connection[strings.Index(test.connection, ":")+1:]

			for _, step := range []struct {
				action, done string
				want         []string
			}{{"grant", "granted", test.granted}, {"revoke", "revoked", test.revoked}} {
				// Act
				code, body := askTheBoard(t, s, "POST", "/api/connections/grant", `{"task":"task-1","generation":"g1","connection":"`+test.connection+`","action":"`+step.action+`"}`, nil)

				// Assert
				meta, err := state.ReadTaskMeta(stateDir, "task-1")
				if code != http.StatusOK || err != nil || !slices.Equal(test.carried(meta), step.want) {
					t.Fatalf("%s = %d %s, record names %v (%v), want %v", step.action, code, body, test.carried(meta), err, step.want)
				}
				log, err := state.TailStatus(stateDir, "task-1", 10)
				if err != nil || !strings.Contains(strings.Join(log, "\n"), "credentials: the Overlord from his own board (goblins-window.exe pid 4242) "+step.done) {
					t.Fatalf("status log = %q (%v), want the change credited to his board", log, err)
				}
				pending, err := wake.Pending(stateDir)
				if err != nil || !slices.ContainsFunc(pending, func(record wake.Record) bool {
					return strings.Contains(record.Detail, "connections: the Overlord "+step.done+" "+name+" from his own board")
				}) {
					t.Fatalf("wakes = %+v (%v), want the CFO told of the %s", pending, err, step.action)
				}
			}
		})
	}
}

// Only a board proven to be the Overlord's own changes what a goblin carries,
// since any program on the PC can read the board's token, and the card names
// only a service or an MCP server of the task it was drawn for.
func TestAConnectorCardChangesNothingForAnotherCallerOrConnector(t *testing.T) {
	cases := []struct {
		name, body string
		isHis      bool
		want       int
	}{
		{name: "a browser an agent harness started", body: `{"task":"task-1","generation":"g1","connection":"service:sample","action":"grant"}`, want: http.StatusForbidden},
		{name: "a credential variable", body: `{"task":"task-1","generation":"g1","connection":"credential:SAMPLE_TOKEN","action":"grant"}`, isHis: true, want: http.StatusBadRequest},
		{name: "another action", body: `{"task":"task-1","generation":"g1","connection":"service:sample","action":"login"}`, isHis: true, want: http.StatusBadRequest},
		{name: "a replaced task", body: `{"task":"task-1","generation":"old","connection":"service:sample","action":"grant"}`, isHis: true, want: http.StatusConflict},
		{name: "a service the manifest does not declare", body: `{"task":"task-1","generation":"g1","connection":"service:unknown","action":"grant"}`, isHis: true, want: http.StatusConflict},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s, stateDir := grantBoard(t)
			asOverlordsBoard(s)
			if !test.isHis {
				s.inspectCaller = func(pid int) ([]proc.Entry, []string, error) {
					started := time.Now().Add(-time.Hour)
					return []proc.Entry{
						{PID: pid, ParentPID: 4242, ExeBase: "chrome.exe", Start: started},
						{PID: 4242, ParentPID: 900, ExeBase: "node.exe", Start: started.Add(-time.Minute)},
						{PID: 900, ExeBase: "explorer.exe", Start: started.Add(-time.Hour)},
					}, nil, nil
				}
			}

			// Act
			code, body := askTheBoard(t, s, "POST", "/api/connections/grant", test.body, nil)

			// Assert
			meta, err := state.ReadTaskMeta(stateDir, "task-1")
			if code != test.want || err != nil || !slices.Equal(meta.Credentials, []string{"other"}) || len(meta.MCPServers) != 0 {
				t.Fatalf("press = %d %s, record names %v and %v (%v), want %d and nothing changed", code, body, meta.Credentials, meta.MCPServers, err, test.want)
			}
		})
	}
}
