package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// grantBoard is a supervisor whose task-1 is in project work, whose manifest
// declares the services sample and other and whose .mcp.json defines docs,
// with its goblin read from its task record by a fake that counts the reads
// and shows what the record gives it.
func grantBoard(t *testing.T) (*Service, string, *atomic.Int32) {
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
	reads := &atomic.Int32{}
	snapshots := s.connectors()
	t.Cleanup(snapshots.Close)
	snapshots.ReadGoblin = func(meta state.TaskMeta, _ string, _ int64, _ connections.Goblin) (connections.Goblin, int64) {
		reads.Add(1)
		goblin := connections.Goblin{Task: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Project: auth.ProjectName(meta.Project)}
		for _, name := range meta.Credentials {
			goblin.Items = append(goblin.Items, connections.Item{ID: "service:" + name, Name: name, Status: connections.StatusSet, Grant: "revoke"})
		}
		for _, name := range meta.MCPServers {
			goblin.Items = append(goblin.Items, connections.Item{ID: "mcp:" + name, Name: name, Status: connections.StatusSet, Grant: "revoke"})
		}
		return goblin, 0
	}
	snapshots.Transcript = func(state.TaskMeta) string { return "" }
	return s, h.State, reads
}

// A connector's Connect gives the task the service or MCP server, and its
// Disconnect takes it back, as goblins auth grant and goblins auth revoke do: the task's
// record changes, its status names the Overlord's board, and the CEO is told.
func TestAConnectorPressGivesAndTakesBackAServiceOrAnMCPServer(t *testing.T) {
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
			s, stateDir, reads := grantBoard(t)
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
					t.Fatalf("wakes = %+v (%v), want the CEO told of the %s", pending, err, step.action)
				}

				// Assert: the press's own answer shows the goblin as the record it
				// left gives it, read once more for the press and not by the page's next
				// read (#2394).
				var answer struct {
					Goblins []connections.Goblin `json:"goblins"`
				}
				if err := json.Unmarshal([]byte(body), &answer); err != nil || len(answer.Goblins) != 1 {
					t.Fatalf("answer to the %s = %s (%v), want the goblin read again", step.action, body, err)
				}
				var shown []string
				for _, item := range answer.Goblins[0].Items {
					shown = append(shown, item.Name)
				}
				if !slices.Equal(shown, slices.Concat(meta.Credentials, meta.MCPServers)) {
					t.Fatalf("the answer to the %s shows %v, want what the record names: %v and %v", step.action, shown, meta.Credentials, meta.MCPServers)
				}
				readsAfterPress := reads.Load()
				askTheBoard(t, s, "GET", "/api/connectors", "", nil)
				if reads.Load() != readsAfterPress {
					t.Fatalf("a read after the %s read the goblin again", step.action)
				}
			}
		})
	}
}

// Only a board proven to be the Overlord's own changes what a goblin carries,
// since any program on the PC can read the board's token, and the press names
// only a service or an MCP server of the task it was made for.
func TestAConnectorPressChangesNothingForAnotherCallerOrConnector(t *testing.T) {
	started := time.Now().Add(-time.Hour)
	agentBrowser := func(pid int) ([]proc.Entry, []string, error) {
		return []proc.Entry{
			{PID: pid, ParentPID: 4242, ExeBase: "chrome.exe", Start: started},
			{PID: 4242, ParentPID: 900, ExeBase: "node.exe", Start: started.Add(-time.Minute)},
			{PID: 900, ExeBase: "explorer.exe", Start: started.Add(-time.Hour)},
		}, nil, nil
	}
	goblinsBrowser := func(pid int) ([]proc.Entry, []string, error) {
		return []proc.Entry{
			{PID: pid, ParentPID: 900, ExeBase: "chrome.exe", Start: started},
			{PID: 900, ExeBase: "explorer.exe", Start: started.Add(-time.Hour)},
		}, []string{harness.RoleVariable + "=" + harness.RoleGoblin}, nil
	}
	grant := `{"task":"task-1","generation":"g1","connection":"service:sample","action":"grant"}`
	cases := []struct {
		name, body string
		caller     func(int) ([]proc.Entry, []string, error)
		want       int
	}{
		{name: "a browser an agent harness started", body: grant, caller: agentBrowser, want: http.StatusForbidden},
		{name: "a browser a goblin's terminal started", body: grant, caller: goblinsBrowser, want: http.StatusForbidden},
		{name: "a credential variable", body: `{"task":"task-1","generation":"g1","connection":"credential:SAMPLE_TOKEN","action":"grant"}`, want: http.StatusBadRequest},
		{name: "another action", body: `{"task":"task-1","generation":"g1","connection":"service:sample","action":"login"}`, want: http.StatusBadRequest},
		{name: "a replaced task", body: `{"task":"task-1","generation":"old","connection":"service:sample","action":"grant"}`, want: http.StatusConflict},
		{name: "a service the manifest does not declare", body: `{"task":"task-1","generation":"g1","connection":"service:unknown","action":"grant"}`, want: http.StatusConflict},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s, stateDir, _ := grantBoard(t)
			asOverlordsBoard(s)
			if test.caller != nil {
				s.inspectCaller = test.caller
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

// A grant that changed the task's record stands though its credential script
// could not be rewritten after it, as a goblin can arrange in its own task
// folder: the board is answered with the check, not told nothing changed, and
// the CEO is told of the grant and of what failed after it.
func TestAConnectorGrantThatStandsIsToldAsOneWhateverFailedAfterIt(t *testing.T) {
	// Arrange
	s, stateDir, _ := grantBoard(t)
	asOverlordsBoard(s)
	meta, err := state.ReadTaskMeta(stateDir, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(meta.TaskTmp); err != nil {
		t.Fatal(err)
	}

	// Act
	code, body := askTheBoard(t, s, "POST", "/api/connections/grant", `{"task":"task-1","generation":"g1","connection":"service:sample","action":"grant"}`, nil)

	// Assert
	meta, err = state.ReadTaskMeta(stateDir, "task-1")
	if code != http.StatusOK || err != nil || !slices.Equal(meta.Credentials, []string{"other", "sample"}) {
		t.Fatalf("press = %d %s, record names %v (%v), want the grant answered as made", code, body, meta.Credentials, err)
	}
	pending, err := wake.Pending(stateDir)
	if err != nil || !slices.ContainsFunc(pending, func(record wake.Record) bool {
		return strings.Contains(record.Detail, "connections: the Overlord granted sample from his own board") && strings.Contains(record.Detail, ", but ") && strings.Contains(record.Detail, "auth.ps1 was not rewritten")
	}) {
		t.Fatalf("wakes = %+v (%v), want the CEO told of the grant and what failed after it", pending, err)
	}
}

// A program that sends a refused press again and again wakes the CEO once a
// minute at most, and each press is still refused.
func TestARefusedConnectorPressRepeatedWakesTheCEOOnce(t *testing.T) {
	// Arrange
	s, stateDir, _ := grantBoard(t)
	asOverlordsBoard(s)
	s.inspectCaller = func(pid int) ([]proc.Entry, []string, error) {
		started := time.Now().Add(-time.Hour)
		return []proc.Entry{{PID: pid, ParentPID: 900, ExeBase: "chrome.exe", Start: started}, {PID: 900, ExeBase: "explorer.exe", Start: started}}, []string{harness.RoleVariable + "=" + harness.RoleGoblin}, nil
	}
	handler := NewHTTP(s, credentialBoardHost, nil)
	press := func() int {
		request := httptest.NewRequest("POST", "http://"+credentialBoardHost+"/api/connections/grant", strings.NewReader(`{"task":"task-1","generation":"g1","connection":"service:sample","action":"grant"}`))
		request.RemoteAddr = "127.0.0.1:50000"
		request.Header.Set("Origin", "http://"+credentialBoardHost)
		request.Header.Set("X-CEO-Token", s.Instance)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}

	// Act
	codes := []int{press(), press(), press()}

	// Assert
	pending, err := wake.Pending(stateDir)
	refusals := 0
	for _, record := range pending {
		if strings.Contains(record.Detail, "A press of a connector's button on a board was refused") {
			refusals++
		}
	}
	if !slices.Equal(codes, []int{http.StatusForbidden, http.StatusForbidden, http.StatusForbidden}) || err != nil || refusals != 1 {
		t.Fatalf("presses = %v, CEO woken %d times (%v), want every press refused and one wake", codes, refusals, err)
	}
}

// liveNativeGoblin makes the task a native goblin whose terminal's host is
// this test's own process, so a grant's re-source notice is sent to it.
func liveNativeGoblin(t *testing.T, stateDir, id string) {
	t.Helper()
	meta, err := state.ReadTaskMeta(stateDir, id)
	if err != nil {
		t.Fatal(err)
	}
	meta.Backend = "native"
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	// stall-accepted: a record's start time, compared only with this process's start, never by a wait.
	data, err := json.Marshal(host.Record{ID: id, HostPID: os.Getpid(), Started: time.Now().Add(time.Second), Pipe: "fixture", Token: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stateDir, "hosts", id+".json"), string(data))
}

// deliverNotices answers every notice typed into a goblin with delivery and
// returns the notices it was handed.
func deliverNotices(t *testing.T, delivery error) *[]string {
	t.Helper()
	previous := typeIntoGoblin
	t.Cleanup(func() { typeIntoGoblin = previous })
	notices := &[]string{}
	typeIntoGoblin = func(_ context.Context, _ string, _ state.TaskMeta, text string, _, _, _ bool) error {
		*notices = append(*notices, text)
		return delivery
	}
	return notices
}

// noticeDeliveries are a re-source notice a goblin in a turn holds for its next
// tool call, which still reaches it, and one that reached no terminal.
var noticeDeliveries = []struct {
	name         string
	delivery     error
	isReportable bool
}{
	{name: "queued for the goblin's next tool call", delivery: fmt.Errorf("spawn: native terminal task-1 has the text submitted: %w", fleet.ErrQueuedForToolCall), isReportable: false},
	{name: "typed into no terminal", delivery: fmt.Errorf("spawn: native terminal task-1: %w", fleet.ErrNotTyped), isReportable: true},
}

// A connector's grant to a goblin in a turn has its re-source notice held for
// the goblin's next tool call, a delivery that worked, so it raises no
// supervisor error; a notice that reached no terminal still raises one
// (#2015).
func TestAConnectorGrantReportsOnlyANoticeThatDidNotReachTheGoblin(t *testing.T) {
	for _, test := range noticeDeliveries {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s, stateDir, _ := grantBoard(t)
			asOverlordsBoard(s)
			s.Options.CEO = &CEOConnection{State: stateDir}
			liveNativeGoblin(t, stateDir, "task-1")
			notices := deliverNotices(t, test.delivery)

			// Act
			code, body := askTheBoard(t, s, "POST", "/api/connections/grant", `{"task":"task-1","generation":"g1","connection":"service:sample","action":"grant"}`, nil)
			s.credentialWork.Wait()

			// Assert
			if code != http.StatusOK || len(*notices) != 1 || !strings.Contains((*notices)[0], "credentials refreshed: re-source") {
				t.Fatalf("grant = %d %s, notices %q, want one re-source notice", code, body, *notices)
			}
			s.mu.Lock()
			lastError := s.lastError
			s.mu.Unlock()
			if reported := strings.Contains(lastError, "telling task-1 to load the credentials the Overlord granted"); reported != test.isReportable {
				t.Fatalf("supervisor error = %q, want it reported: %v", lastError, test.isReportable)
			}
		})
	}
}

// A connector's grant tells a goblin to re-source its credentials only once a
// view of its terminal has answered the Enter of a message typed there as
// keys, as every board delivery to a goblin waits for that answer: keys its
// harness has not read yet show nothing on the prompt line, and the notice
// typed behind them would join their words. Refused instead, the notice never
// reached the goblin, since nothing sends it again (#2848's review).
func TestAConnectorGrantTellsTheGoblinOnceAViewAnswersATypedMessagesEnter(t *testing.T) {
	// Arrange
	s, stateDir, _ := grantBoard(t)
	asOverlordsBoard(s)
	s.Options.CEO = &CEOConnection{State: stateDir}
	liveNativeGoblin(t, stateDir, "task-1")
	notices := deliverNotices(t, nil)
	answered := s.Options.CEO.awaitEnter("task-1")

	// Act: the grant, what it typed while the Enter waits, and the Enter's
	// answer.
	code, body := askTheBoard(t, s, "POST", "/api/connections/grant", `{"task":"task-1","generation":"g1","connection":"service:sample","action":"grant"}`, nil)
	time.Sleep(300 * time.Millisecond)
	held := slices.Clone(*notices)
	answered()
	s.credentialWork.Wait()

	// Assert
	if code != http.StatusOK || len(held) != 0 {
		t.Fatalf("grant = %d %s, notices %q while the Enter waits, want the grant and none typed", code, body, held)
	}
	if len(*notices) != 1 || !strings.Contains((*notices)[0], "credentials refreshed: re-source") {
		t.Fatalf("notices = %q once the Enter was answered, want the one re-source notice", *notices)
	}
	s.mu.Lock()
	lastError := s.lastError
	s.mu.Unlock()
	if strings.Contains(lastError, "telling task-1 to load the credentials the Overlord granted") {
		t.Fatalf("supervisor error = %q, want none for a notice that reached the goblin", lastError)
	}
}
