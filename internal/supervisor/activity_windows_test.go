package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestPrimaryPresentationUsesVerifiedContextWithoutBorrowingTask(t *testing.T) {
	store, h := testStore(t)
	_, identity, runner, cfo := primaryFixture(t, store)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_SESSION_ID", "actual-primary")
	t.Setenv("CFO_SESSION_HARNESS", "codex")
	now := time.Now().UTC()
	a := BoardActivity{ID: "primary-review", Kind: "review", State: "active", URL: "http://127.0.0.1:4387/session/review", At: now, Until: now.Add(time.Minute)}
	if err := PublishPresentation(context.Background(), h, cfo.Herdr, a); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestActivity(); err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot().Activity[0]
	if got.TaskID != "" || got.CFOIdentity != identity || got.Target != "primary-cfo" {
		t.Fatal("primary borrowed or invented a task", got)
	}
	service := &Service{Store: store, Options: Options{CFO: cfo}}
	service.reconcilePresentations(context.Background())
	snap, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Activity[0].Live {
		t.Fatal("verified primary presentation not live")
	}
	runner.pid = 1
	service.presentationChecked = time.Time{}
	service.reconcilePresentations(context.Background())
	snap, err = service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Activity[0].Live {
		t.Fatal("unavailable primary presentation stayed live")
	}
	if err := PublishPresentation(context.Background(), h, cfo.Herdr, a); err == nil {
		t.Fatal("unverified primary published")
	}
}

func TestSendReceiptNeverBorrowsSameHarnessParent(t *testing.T) {
	store, h := testStore(t)
	_, _, _, cfo := primaryFixture(t, store)
	now := time.Now().UTC()
	if err := store.Accept(event(t, h, "SessionStart", "worker", "", now)); err != nil {
		t.Fatal(err)
	}
	id := store.db.TaskSessions["task-1"]
	node := store.db.Sessions[id]
	node.Parent = "codex/old-primary"
	store.db.Sessions[id] = node
	store.db.Sessions[node.Parent] = Session{ID: node.Parent, NativeID: "old-primary", Harness: "codex", Role: "cfo", Phase: "ended"}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_SESSION_ID", "actual-primary")
	t.Setenv("CFO_SESSION_HARNESS", "codex")
	if err := PrepareSendActivity(context.Background(), h, cfo.Herdr, "task-1")(); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestActivity(); err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot().Activity
	if len(got) != 2 || got[1].Source != "" {
		t.Fatal("same harness manufactured sender ancestry", got)
	}
	// Explicit caller identity can prove the real reported parent.
	node.Parent = "codex/actual-primary"
	store.db.Sessions[id] = node
	store.db.Sessions[node.Parent] = Session{ID: node.Parent, NativeID: "actual-primary", Harness: "codex", Role: "cfo", Phase: "active"}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	if err := PrepareSendActivity(context.Background(), h, cfo.Herdr, "task-1")(); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestActivity(); err != nil {
		t.Fatal(err)
	}
	got = store.Snapshot().Activity
	if len(got) != 3 || got[2].Source != node.Parent {
		t.Fatal("explicit sender link missing", got)
	}
}

func TestPrimaryPresentationLateSessionDiscoveryKeepsReportIdentity(t *testing.T) {
	store, h := testStore(t)
	_, identity, _, cfo := primaryFixture(t, store)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_SESSION_ID", "actual-primary")
	t.Setenv("CFO_SESSION_HARNESS", "codex")
	now := time.Now().UTC()
	a := BoardActivity{ID: "late-primary-report", Kind: "review", State: "active", URL: "http://127.0.0.1:4387/session/review", At: now, Until: now.Add(time.Minute)}
	if err := PublishPresentation(context.Background(), h, cfo.Herdr, a); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestActivity(); err != nil {
		t.Fatal(err)
	}
	start := event(t, h, "SessionStart", "actual-primary", "", now)
	start.Harness, start.Role, start.TaskID, start.Generation = "codex", "cfo", "", ""
	if err := store.Accept(start); err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"active", "ended"} {
		a.State = status
		a.At = now.Add(time.Duration(i+1) * time.Second)
		if err := PublishPresentation(context.Background(), h, cfo.Herdr, a); err != nil {
			t.Fatal("late native discovery rejected update", status, err)
		}
		if err := store.ingestActivity(); err != nil {
			t.Fatal(err)
		}
		got := store.Snapshot().Activity[0]
		if got.Target != "primary-cfo" || got.CFOIdentity != identity || got.State != status {
			t.Fatal("primary report changed identity", got)
		}
	}
}

// A goblin spawned while serve runs, with no native hook installed, presents
// from its own pane and serve shows it at once; a process outside that pane
// cannot present in its name.
func TestGoblinSpawnedWhileServeRunsPresentsFromItsOwnPane(t *testing.T) {
	_, h := testStore(t)
	s, err := Start(context.Background(), h, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(h.State, ".supervisor.json")); err == nil {
			break
		}
	}
	worktree := t.TempDir()
	meta := state.TaskMeta{ID: "task-2", Project: worktree, Worktree: worktree, Harness: "codex", Mode: "no-mistakes", Kind: "ship", Backend: "herdr", SpawnGen: "g7", HerdrSession: "isolated", HerdrWorkspaceID: "w1", HerdrTabID: "t1", HerdrPaneID: "w1:p1"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	runner := &cfoRunner{t: t, pid: os.Getpid()}
	client := &herdr.Client{Commands: runner}
	now := time.Now().UTC()
	a := BoardActivity{ID: "task-2-review", Kind: "review", TaskID: "task-2", State: "active", URL: "http://127.0.0.1:4387/session/f26e1c33babf6415", At: now, Until: now.Add(10 * time.Minute)}
	if err := PublishPresentation(context.Background(), h, client, a); err != nil {
		t.Fatalf("a goblin spawned while serve runs could not present: %v", err)
	}
	var got BoardActivity
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if i := slices.IndexFunc(s.Store.Snapshot().Activity, func(x BoardActivity) bool { return x.ID == a.ID }); i >= 0 {
			got = s.Store.Snapshot().Activity[i]
			break
		}
	}
	if got.TaskID != "task-2" || got.Generation != "g7" || got.Target != "" || got.State != "active" {
		t.Fatalf("serve holds %+v, want the goblin's live review", got)
	}
	a.ID, a.URL = "task-2-tailnet", "http://sermon.tailcc4238.ts.net:4387/session/f26e1c33babf6415"
	if err := PublishPresentation(context.Background(), h, client, a); err != nil {
		t.Fatalf("the tailnet link Lavish returns was refused: %v", err)
	}
	a.ID, a.URL = "task-2-foreign", "http://192.0.2.10:4387/session/f26e1c33babf6415"
	if err := PublishPresentation(context.Background(), h, client, a); err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Fatalf("a plain http link off this machine and the tailnet = %v, want the rule named", err)
	}
	a.ID, a.Generation = "task-2-stale", "g6"
	if err := PublishPresentation(context.Background(), h, client, a); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("a previous generation presented: %v", err)
	}
	runner.pid = 2147483647
	a.ID, a.Generation = "task-2-other-pane", ""
	if err := PublishPresentation(context.Background(), h, client, a); err == nil || !strings.Contains(err.Error(), "does not run under it") {
		t.Fatalf("a process outside the goblin's pane presented in its name: %v", err)
	}
}
