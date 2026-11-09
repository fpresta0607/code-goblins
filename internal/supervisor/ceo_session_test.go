package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/host"
)

// ceoConversationFile names the folder Claude Code keeps the fleet home's
// conversations in, as Claude Code names it: each character that is not a
// letter or digit as a dash.
func ceoConversationFile(userHome, fleetHome string) string {
	folder := strings.Map(func(ch rune) rune {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			return ch
		}
		return '-'
	}, fleetHome)
	return filepath.Join(userHome, ".claude", "projects", folder)
}

// runningCEOHarness makes this test process the CEO's harness, as treeGoblin
// makes it a goblin's, and answers the folder Claude Code keeps the fleet
// home's conversations in under the user's home.
func runningCEOHarness(t *testing.T, stateDir, userHome, fleetHome string) string {
	t.Helper()
	processes, err := fleettree.Processes()
	if err != nil {
		t.Fatal(err)
	}
	var self fleettree.Process
	for _, process := range processes {
		if process.PID == os.Getpid() {
			self = process
		}
	}
	record, err := json.Marshal(host.Record{ID: "ceo", Pipe: `\\.\pipe\code-goblins-host-ceo`, Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: os.Getpid(), ChildStart: self.Started, Started: self.Started})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stateDir, "hosts", "ceo.json"), string(record))
	writeFile(t, filepath.Join(userHome, ".claude", "sessions", strconv.Itoa(os.Getpid())+".json"),
		`{"pid":`+strconv.Itoa(os.Getpid())+`,"sessionId":"live-session","procStart":"`+strconv.FormatInt(self.Created, 10)+`"}`)
	return ceoConversationFile(userHome, fleetHome)
}

// ceoMessageStream opens the CEO's messages as the board streams them, and
// answers a reader of the data of the next event of a name.
func ceoMessageStream(t *testing.T, service *Service) func(event string) string {
	t.Helper()
	handler := NewHTTP(service, "127.0.0.1:50000", nil)
	handler.terminalTick = 100 * time.Millisecond
	server := httptest.NewUnstartedServer(handler)
	handler.Host = server.Listener.Addr().String()
	server.Start()
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/messages?ceo=ceo&token=board-token&stream=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(nil, 1<<20)
	return func(event string) string {
		t.Helper()
		current := ""
		for scanner.Scan() {
			if name, isEvent := strings.CutPrefix(scanner.Text(), "event: "); isEvent {
				current = name
			}
			if data, isData := strings.CutPrefix(scanner.Text(), "data: "); isData && current == event {
				return data
			}
		}
		t.Fatalf("the CEO's stream sent no %s event: %v", event, scanner.Err())
		return ""
	}
}

// TestTheCEOsConversationRecordFollowsTheLiveSession is issue #2172: the
// record named a conversation that stopped hours ago while another was being
// written, and the board rendered the stopped one as current. A read of the
// CEO's messages follows the conversation its harness is in now, and writes
// the record, so the board never needs a reload.
func TestTheCEOsConversationRecordFollowsTheLiveSession(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	registerNativeCEO(t, h.State, "claude", "stale-session")
	folder := runningCEOHarness(t, h.State, userHome, h.Root)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, session := range []string{"stale-session", "live-session"} {
		writeFile(t, filepath.Join(folder, session+".jsonl"),
			`{"type":"user","uuid":"`+session+`","timestamp":"`+stamp+`","message":{"role":"user","content":"`+session+`"}}`+"\n")
	}
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	handler := NewHTTP(service, "127.0.0.1:50000", nil)

	// Act
	locate, err := handler.messagesLocator(context.Background(), map[string][]string{"ceo": {"ceo"}, "token": {"board-token"}})
	if err != nil {
		t.Fatal(err)
	}
	located, err := locate()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if located.Identity.Session != "live-session" || !strings.HasSuffix(located.Path, "live-session.jsonl") {
		t.Fatalf("the CEO's conversation: %+v; want the live session", located)
	}
	recorded, err := ReadCEOConversation(h.State)
	if err != nil || recorded.Session != "live-session" || recorded.Harness != "claude" {
		t.Fatalf("the conversation record: %+v, %v; want the live session", recorded, err)
	}
}

// TestACEOsRecordThatNamesAConversationItNeverWroteIsReported keeps a record
// that cannot be followed from rendering as the current conversation: the
// board says so instead.
func TestACEOsRecordThatNamesAConversationItNeverWroteIsReported(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	registerNativeCEO(t, h.State, "claude", "never-written")
	runningCEOHarness(t, h.State, userHome, h.Root)
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	handler := NewHTTP(service, "127.0.0.1:50000", nil)

	// Act
	locate, err := handler.messagesLocator(context.Background(), map[string][]string{"ceo": {"ceo"}, "token": {"board-token"}})
	if err != nil {
		t.Fatal(err)
	}
	located, err := locate()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if located.Stale == "" {
		t.Fatalf("a record naming a conversation this session never wrote is rendered as current: %+v", located)
	}
}

// TestACEORecordItsHomeDoesNotHoldIsStaleAndUnread keeps the CEO's board on
// its home's own conversation when nothing proves a live one: a record naming
// a conversation another folder keeps, or one never written, is reported
// stale, and nothing of it is read as the CEO's.
func TestACEORecordItsHomeDoesNotHoldIsStaleAndUnread(t *testing.T) {
	for name, tc := range map[string]struct {
		harness    string
		transcript string
	}{
		"claude in another folder": {"claude", filepath.Join(".claude", "projects", "C--work-other", "other-session.jsonl")},
		"pi in another folder":     {"pi", filepath.Join(".pi", "agent", "sessions", "--C--work-other--", "2026-11-09T09-00-00-000Z_other-session.jsonl")},
		"codex never written":      {"codex", ""},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			userHome := t.TempDir()
			registerNativeCEO(t, h.State, tc.harness, "other-session")
			if tc.transcript != "" {
				writeFile(t, filepath.Join(userHome, tc.transcript), "{}\n")
			}
			service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
			locate, err := NewHTTP(service, "127.0.0.1:50000", nil).messagesLocator(context.Background(), map[string][]string{"ceo": {"ceo"}, "token": {"board-token"}})
			if err != nil {
				t.Fatal(err)
			}

			// Act
			located, err := locate()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(located.Stale, "other-session") || located.Path != "" {
				t.Fatalf("a record its home does not hold: %+v; want it named stale and no conversation read", located)
			}
		})
	}
}

// TestABoardHearsWhyItsCEORecordIsStale is the reason reaching the board over
// the stream, not only the locator's own return: a record naming another
// folder's conversation is said to be stale, and the page draws none of it.
func TestABoardHearsWhyItsCEORecordIsStale(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	registerNativeCEO(t, h.State, "claude", "other-folder-session")
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	writeFile(t, filepath.Join(userHome, ".claude", "projects", "C--work-other", "other-folder-session.jsonl"),
		`{"type":"user","uuid":"o1","timestamp":"`+stamp+`","message":{"role":"user","content":"Another project's question"}}`+"\n")
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}

	// Act
	read := ceoMessageStream(t, service)
	page, session := read("page"), read("session")

	// Assert
	if strings.Contains(page, "Another project") {
		t.Fatalf("the CEO's page drew another folder's conversation: %s", page)
	}
	if !strings.Contains(session, "other-folder-session") || !strings.Contains(session, "stale") {
		t.Fatalf("the session event of a stale record: %s", session)
	}
}

// TestABoardHearsACEORecordGoStaleWhileItsSessionStands keeps an open view on
// what the record is now: a CEO whose harness moved to a conversation the
// board could not record goes stale while the conversation the view reads
// stands, and the view is told so without another page.
func TestABoardHearsACEORecordGoStaleWhileItsSessionStands(t *testing.T) {
	// Arrange: a record the home holds, which nothing proves stale yet.
	store, h := testStore(t)
	userHome := t.TempDir()
	registerNativeCEO(t, h.State, "claude", "recorded-session")
	folder := ceoConversationFile(userHome, h.Root)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, session := range []string{"recorded-session", "live-session"} {
		writeFile(t, filepath.Join(folder, session+".jsonl"),
			`{"type":"user","uuid":"`+session+`","timestamp":"`+stamp+`","message":{"role":"user","content":"`+session+`"}}`+"\n")
	}
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	read := ceoMessageStream(t, service)
	if page := read("page"); !strings.Contains(page, "recorded-session") {
		t.Fatalf("the CEO's page before it moved: %s", page)
	}
	if session := read("session"); strings.Contains(session, `"stale"`) {
		t.Fatalf("a record its home holds is stale: %s", session)
	}

	// Act: the harness proves another conversation, and a folder where the
	// record belongs keeps the board from writing it.
	if err := os.MkdirAll(ceoConversationPath(h.State), 0o700); err != nil {
		t.Fatal(err)
	}
	runningCEOHarness(t, h.State, userHome, h.Root)

	// Assert
	if session := read("session"); !strings.Contains(session, "could not record") {
		t.Fatalf("the session event after the CEO moved: %s", session)
	}
}

// TestARecoveryCycleKeepsTheCEOOnTheLiveConversation is the caller that keeps
// the record right without a board read: a cycle with a live CEO rewrites a
// record that names a conversation its home has stopped writing.
func TestARecoveryCycleKeepsTheCEOOnTheLiveConversation(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	userHome := t.TempDir()
	service.Options.Tree = &fleettree.Reader{Home: userHome}
	registerNativeCEO(t, h.State, "claude", "stale-session")
	folder := runningCEOHarness(t, h.State, userHome, h.Root)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, session := range []string{"stale-session", "live-session"} {
		writeFile(t, filepath.Join(folder, session+".jsonl"),
			`{"type":"user","uuid":"`+session+`","timestamp":"`+stamp+`","message":{"role":"user","content":"`+session+`"}}`+"\n")
	}

	// Act
	service.cycle(context.Background(), true)

	// Assert
	recorded, err := ReadCEOConversation(h.State)
	if err != nil || recorded.Session != "live-session" {
		t.Fatalf("the conversation record after a cycle: %+v, %v; want the live session", recorded, err)
	}
}

// TestAConnectedSessionsConversationIsItsOwnFolder keeps the board from
// following another session of the same harness, as one process stands for
// several on a machine where a test's goblin shares the CEO's process.
func TestAConnectedSessionsConversationIsItsOwnFolder(t *testing.T) {
	for name, tc := range map[string]struct {
		harness string
		dir     string
		session string
		want    bool
	}{
		"claude in its own folder":       {"claude", `C:\work`, "session-1", true},
		"claude in another folder":       {"claude", `C:\work`, "session-2", false},
		"pi in its own folder":           {"pi", `C:\work`, "pi-1", true},
		"pi in another folder":           {"pi", `C:\work`, "pi-2", false},
		"codex names its own folder":     {"codex", `C:\work`, "rollout-1", true},
		"a harness that keeps no record": {"grok", `C:\work`, "session-1", false},
	} {
		// Arrange
		home := t.TempDir()
		writeFile(t, filepath.Join(home, ".claude", "projects", "C--work", "session-1.jsonl"), "{}\n")
		writeFile(t, filepath.Join(home, ".pi", "agent", "sessions", "--C--work--", "2026-10-06T21-30-00-000Z_pi-1.jsonl"), "{}\n")
		// Act
		got := fleettree.HoldsTranscript(home, tc.harness, tc.dir, tc.session)

		// Assert
		if got != tc.want {
			t.Fatalf("%s: %t; want %t", name, got, tc.want)
		}
	}
}
