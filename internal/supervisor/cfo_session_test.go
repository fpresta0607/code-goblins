package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/host"
)

// cfoConversationFile names the folder Claude Code keeps the fleet home's
// conversations in, as Claude Code names it: each character that is not a
// letter or digit as a dash.
func cfoConversationFile(userHome, fleetHome string) string {
	folder := strings.Map(func(ch rune) rune {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			return ch
		}
		return '-'
	}, fleetHome)
	return filepath.Join(userHome, ".claude", "projects", folder)
}

// runningCFOHarness makes this test process the CFO's harness, as treeGoblin
// makes it a goblin's, and answers the folder Claude Code keeps the fleet
// home's conversations in under the user's home.
func runningCFOHarness(t *testing.T, stateDir, userHome, fleetHome string) string {
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
	record, err := json.Marshal(host.Record{ID: "cfo", Pipe: `\\.\pipe\code-goblins-host-cfo`, Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: os.Getpid(), ChildStart: self.Started, Started: self.Started})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stateDir, "hosts", "cfo.json"), string(record))
	writeFile(t, filepath.Join(userHome, ".claude", "sessions", strconv.Itoa(os.Getpid())+".json"),
		`{"pid":`+strconv.Itoa(os.Getpid())+`,"sessionId":"live-session","procStart":"`+strconv.FormatInt(self.Created, 10)+`"}`)
	return cfoConversationFile(userHome, fleetHome)
}

// TestTheCFOsConversationRecordFollowsTheLiveSession is issue #2172: the
// record named a conversation that stopped hours ago while another was being
// written, and the board rendered the stopped one as current. A read of the
// CFO's messages follows the conversation its harness is in now, and writes
// the record, so the board never needs a reload.
func TestTheCFOsConversationRecordFollowsTheLiveSession(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	registerNativeCFO(t, h.State, "claude", "stale-session")
	folder := runningCFOHarness(t, h.State, userHome, h.Root)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, session := range []string{"stale-session", "live-session"} {
		writeFile(t, filepath.Join(folder, session+".jsonl"),
			`{"type":"user","uuid":"`+session+`","timestamp":"`+stamp+`","message":{"role":"user","content":"`+session+`"}}`+"\n")
	}
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	handler := NewHTTP(service, "127.0.0.1:50000", nil)

	// Act
	locate, err := handler.messagesLocator(context.Background(), map[string][]string{"cfo": {"cfo"}, "token": {"board-token"}})
	if err != nil {
		t.Fatal(err)
	}
	located, err := locate()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if located.Identity.Session != "live-session" || !strings.HasSuffix(located.Path, "live-session.jsonl") {
		t.Fatalf("the CFO's conversation: %+v; want the live session", located)
	}
	recorded, err := ReadCFOConversation(h.State)
	if err != nil || recorded.Session != "live-session" || recorded.Harness != "claude" {
		t.Fatalf("the conversation record: %+v, %v; want the live session", recorded, err)
	}
}

// TestACFOsRecordThatNamesAConversationItNeverWroteIsReported keeps a record
// that cannot be followed from rendering as the current conversation: the
// board says so instead.
func TestACFOsRecordThatNamesAConversationItNeverWroteIsReported(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	registerNativeCFO(t, h.State, "claude", "never-written")
	runningCFOHarness(t, h.State, userHome, h.Root)
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	handler := NewHTTP(service, "127.0.0.1:50000", nil)

	// Act
	locate, err := handler.messagesLocator(context.Background(), map[string][]string{"cfo": {"cfo"}, "token": {"board-token"}})
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

// TestAConnectedSessionsConversationIsItsOwnFolder keeps the board from
// following another session of the same harness, as one process stands for
// several on a machine where a test's goblin shares the CFO's process.
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
