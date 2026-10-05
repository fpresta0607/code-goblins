package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestPrimaryPresentationUsesVerifiedContextWithoutBorrowingTask(t *testing.T) {
	store, h := testStore(t)
	_, identity, terminal, cfo := primaryFixture(t, store)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_SESSION_ID", "actual-primary")
	t.Setenv("CFO_SESSION_HARNESS", "codex")
	now := time.Now().UTC()
	a := BoardActivity{ID: "primary-review", Kind: "review", State: "active", URL: "http://127.0.0.1:4387/session/review", At: now, Until: now.Add(time.Minute)}
	if err := PublishPresentation(h, a); err != nil {
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
	service.reconcilePresentations()
	snap, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Activity[0].Live {
		t.Fatal("verified primary presentation not live")
	}
	// The CFO's terminal now runs another program.
	terminal.runs(t, 4)
	service.presentationChecked = time.Time{}
	service.reconcilePresentations()
	snap, err = service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Activity[0].Live {
		t.Fatal("unavailable primary presentation stayed live")
	}
	if err := PublishPresentation(h, a); err == nil {
		t.Fatal("unverified primary published")
	}
}

// A send to a native goblin, by its id or gb-<id>, leaves the board's message
// receipt.
func TestASendToANativeGoblinLeavesABoardReceipt(t *testing.T) {
	for _, target := range []string{"task-1", "gb-task-1"} {
		t.Run(target, func(t *testing.T) {
			store, h := testStore(t)
			primaryFixture(t, store)
			meta := makeNative(t, h.State, "task-1")
			if err := store.Accept(event(t, h, "SessionStart", "worker", "", time.Now().UTC())); err != nil {
				t.Fatal(err)
			}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}

			err := PrepareSendActivity(h, target)()

			if err != nil {
				t.Fatal(err)
			}
			if err := store.ingestActivity(); err != nil {
				t.Fatal(err)
			}
			var receipts []BoardActivity
			for _, a := range store.Snapshot().Activity {
				if a.Kind == "message" {
					receipts = append(receipts, a)
				}
			}
			if len(receipts) != 1 || receipts[0].TaskID != "task-1" || receipts[0].Generation != meta.SpawnGen {
				t.Fatalf("message receipts = %+v, want one for task-1 in its generation", receipts)
			}
		})
	}
}

func TestSendReceiptNeverBorrowsSameHarnessParent(t *testing.T) {
	store, h := testStore(t)
	primaryFixture(t, store)
	makeNative(t, h.State, "task-1")
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
	if err := PrepareSendActivity(h, "task-1")(); err != nil {
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
	if err := PrepareSendActivity(h, "task-1")(); err != nil {
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
	_, identity, _, _ := primaryFixture(t, store)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_SESSION_ID", "actual-primary")
	t.Setenv("CFO_SESSION_HARNESS", "codex")
	now := time.Now().UTC()
	a := BoardActivity{ID: "late-primary-report", Kind: "review", State: "active", URL: "http://127.0.0.1:4387/session/review", At: now, Until: now.Add(time.Minute)}
	if err := PublishPresentation(h, a); err != nil {
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
		if err := PublishPresentation(h, a); err != nil {
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
// from its own terminal and serve shows it at once; a process outside that
// terminal cannot present in its name.
func TestGoblinSpawnedWhileServeRunsPresentsFromItsOwnTerminal(t *testing.T) {
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
	meta := state.TaskMeta{ID: "task-2", Project: worktree, Worktree: worktree, Harness: "codex", Mode: "no-mistakes", Kind: "ship", Backend: "native", Window: "native", SpawnGen: "g7"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	goblin := hostTerminal(t, h.State, "task-2")
	goblin.standIn(t)
	now := time.Now().UTC()
	a := BoardActivity{ID: "task-2-review", Kind: "review", TaskID: "task-2", State: "active", URL: "http://127.0.0.1:4387/session/f26e1c33babf6415", At: now, Until: now.Add(10 * time.Minute)}
	if err := PublishPresentation(h, a); err != nil {
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
	if err := PublishPresentation(h, a); err != nil {
		t.Fatalf("the tailnet link Lavish returns was refused: %v", err)
	}
	a.ID, a.URL = "task-2-foreign", "http://192.0.2.10:4387/session/f26e1c33babf6415"
	if err := PublishPresentation(h, a); err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Fatalf("a plain http link off this machine and the tailnet = %v, want the rule named", err)
	}
	a.ID, a.Generation = "task-2-stale", "g6"
	if err := PublishPresentation(h, a); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("a previous generation presented: %v", err)
	}
	// The System process is live and never the ancestor of a test.
	goblin.runs(t, 4)
	a.ID, a.Generation = "task-2-other-terminal", ""
	if err := PublishPresentation(h, a); err == nil || !strings.Contains(err.Error(), "does not run under it") {
		t.Fatalf("a process outside the goblin's terminal presented in its name: %v", err)
	}
}
