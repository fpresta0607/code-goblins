package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// runPipe serves run requests for s's home until the test ends.
func runPipe(t *testing.T, s *Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.serveRunRequests(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// A request from a process outside the registered CFO's tree is refused
// before anything is written, and never reaches the board.
func TestRunRequestFromOutsideTheCFOTreeNeverReachesTheBoard(t *testing.T) {
	store, h := testStore(t)
	// The registered CFO is a process of its own, which this test does not run
	// under.
	cfo := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-Command", "Start-Sleep -Seconds 120")
	cfo.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if err := cfo.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cfo.Process.Kill()
		_ = cfo.Wait()
	})
	entries, err := proc.Ancestry(cfo.Process.Pid, 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the stand-in CFO's start time: %v %v", entries, err)
	}
	primary := primaryRegistration{Target: herdr.Target{Session: "isolated", Pane: "w1:p1"}, Workspace: "w1", Tab: "w1:t1", Agent: "codex", Terminal: "test-terminal", Process: lock.Info{PID: cfo.Process.Pid, Start: entries[0].Start}}
	data, _ := json.Marshal(primary)
	if err := os.WriteFile(filepath.Join(h.State, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	runPipe(t, &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State, Herdr: &herdr.Client{Commands: &cfoRunner{t: t, pid: os.Getpid()}}}}})
	err = PublishRun(h, RunRequest{ID: "install-driver", Title: "Install the driver", Shell: "powershell", Admin: true, CommandFile: commandFile(t, "Write-Output driver\n")})
	if err == nil || !strings.Contains(err.Error(), "does not run under the registered CFO") {
		t.Fatalf("a run request from outside the CFO's tree = %v, want it refused", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(h.State, "runs")); len(entries) != 0 || len(store.Snapshot().Runs) != 0 {
		t.Fatalf("a refused request left %d script folders and runs %+v, want none", len(entries), store.Snapshot().Runs)
	}
}

// Nothing written straight into the state directory reaches the board: an
// item with its script placed where the old inbox took them is never read,
// however well-formed it is and whatever identity it claims.
func TestRunItemDroppedIntoTheInboxNeverReachesTheBoard(t *testing.T) {
	store, h := testStore(t)
	_, identity, _, _ := primaryFixture(t, store)
	r := Run{ID: "planted-item", Identity: identity, Title: "Looks like it came from the CFO", Shell: "powershell", Admin: true, Command: "Write-Output planted\n", Cwd: h.Root, State: "ready", CreatedAt: time.Now().UTC()}
	name, script := runScript(r)
	dir := runDir(h.State, r)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), script, 0600); err != nil {
		t.Fatal(err)
	}
	r.ScriptSum, r.ExpiresAt = runDigest(script), r.CreatedAt.Add(runLifetime)
	data, _ := json.Marshal(r)
	inbox := filepath.Join(h.State, "runs-inbox")
	if err := os.MkdirAll(inbox, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "planted.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	(&Service{Store: store, work: make(chan struct{}, 1)}).cycle(context.Background(), false)
	if runs := store.Snapshot().Runs; len(runs) != 0 {
		t.Fatalf("runs = %+v after an item was planted in the state directory, want none", runs)
	}
}

// The CFO proof walks up from a process to the registered CFO, and a parent
// created after its child ends the walk: Windows reuses PIDs, so that parent is
// another process that took a dead parent's PID.
func TestDescendsFromNeedsEachAncestorOlderThanItsChild(t *testing.T) {
	base := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	cfo := lock.Info{PID: 100, Start: base}
	child := proc.Entry{PID: 300, Start: base.Add(2 * time.Minute)}
	for _, test := range []struct {
		name    string
		entries []proc.Entry
		want    bool
	}{
		{name: "the CFO itself", entries: []proc.Entry{{PID: 100, Start: base}}, want: true},
		{name: "a descendant", entries: []proc.Entry{child, {PID: 200, Start: base.Add(time.Minute)}, {PID: 100, Start: base}}, want: true},
		{name: "a parent PID reused after its child started", entries: []proc.Entry{child, {PID: 200, Start: base.Add(3 * time.Minute)}, {PID: 100, Start: base}}},
		{name: "the CFO's PID reused by a newer process", entries: []proc.Entry{child, {PID: 100, Start: base.Add(time.Minute)}}},
		{name: "another tree", entries: []proc.Entry{child, {PID: 400, Start: base}}},
	} {
		if got := descendsFrom(test.entries, cfo); got != test.want {
			t.Errorf("%s: descendsFrom = %v, want %v", test.name, got, test.want)
		}
	}
}
