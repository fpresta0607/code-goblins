package supervisor

import (
	"context"
	"testing"
	"time"
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
