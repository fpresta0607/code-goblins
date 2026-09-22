package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func reviewAction(t *testing.T, service *Service, meta state.TaskMeta, id, path string, first, last int) Action {
	t.Helper()
	diff, err := service.previewGit(meta).Diff(context.Background(), meta.Worktree, "", path)
	if err != nil {
		t.Fatal(err)
	}
	var action Action
	body := fmt.Sprintf(`{"id":%q,"kind":"review","task_id":%q,"generation":%q,"text":"Please simplify this range","file":%q,"line":%d,"end_line":%d,"side":"new","head":%q,"diff_id":%q}`, id, meta.ID, meta.SpawnGen, path, first, last, diff.Head, diff.Fingerprint)
	if err := json.Unmarshal([]byte(body), &action); err != nil {
		t.Fatal(err)
	}
	return action
}

func TestReviewRangeAcceptsVisibleContextAndRefusesGaps(t *testing.T) {
	patch := "@@ -2,3 +2,4 @@\n before\n-old\n+new\n+extra\n after\n@@ -20 +21 @@\n-far\n+away\n"
	for _, side := range []string{"old", "new"} {
		if code, err := diffRangeContext(patch, 2, 2, side); err != nil || code != "before\n" {
			t.Fatalf("context %s: %q %v", side, code, err)
		}
	}
	if _, err := diffRangeContext(patch, 2, 21, "new"); err == nil {
		t.Fatal("range crossed an unreported hunk gap")
	}
	if _, err := diffRangeContext(patch, 2, 202, "new"); err == nil {
		t.Fatal("unbounded selection accepted")
	}
}

func TestReviewRejectsStaleContextAndGenerationWithoutDelivery(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	service := &Service{Store: store}
	valid := reviewAction(t, service, meta, "review-stale", "main.go", 2, 2)
	for _, mutate := range []func(*Action){
		func(a *Action) { a.Head = strings.Repeat("0", 40) },
		func(a *Action) { a.DiffID = "stale" },
		func(a *Action) { a.EndLine = 2000 },
		func(a *Action) { a.Side = "invalid" },
		func(a *Action) { a.File = "../.env" },
		func(a *Action) { a.Revision = "HEAD~1" },
	} {
		action := valid
		mutate(&action)
		if _, err := service.execute(context.Background(), action); !errors.Is(err, ErrRejected) {
			t.Fatalf("invalid selection not rejected: %v", err)
		}
	}
	if _, err := store.Queue(valid); err != nil {
		t.Fatal(err)
	}
	conflict := valid
	conflict.EndLine++
	if _, err := store.Queue(conflict); err == nil {
		t.Fatal("same ID accepted with another range")
	}
	// A queued or browser-held draft must not adopt the next spawn generation.
	meta.SpawnGen = "g2"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(valid); err == nil {
		t.Fatal("old generation accepted after respawn")
	}
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Actions[0].Status; got != "failed" {
		t.Fatalf("stale outcome %s", got)
	}
	records, err := wake.Pending(h.State)
	if err != nil || len(records) != 0 {
		t.Fatalf("invalid review reached queue: %+v %v", records, err)
	}
}

func TestReviewCrashAfterQueueWriteRemainsUncertainAndDoesNotRepeat(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	service := &Service{Store: store}
	action := reviewAction(t, service, meta, "review-crash", "main.go", 2, 2)
	if _, err := store.Queue(action); err != nil {
		t.Fatal(err)
	}
	// Fail publication after the durable queue append, like an interrupted
	// two-write wake or a crash before the action result is committed.
	if err := os.Mkdir(filepath.Join(h.State, ".watcher-down"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Actions[0].Status; got != "uncertain" {
		t.Fatalf("partial delivery %s", got)
	}
	if err := os.Remove(filepath.Join(h.State, ".watcher-down")); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	service.Store = reopened
	if _, err := reopened.Queue(action); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	records, err := wake.Pending(h.State)
	if err != nil || len(records) != 1 {
		t.Fatalf("partial review replayed: %+v %v", records, err)
	}
	// Also exercise the persisted running state recovered after abrupt exit.
	reopened.mu.Lock()
	reopened.db.Actions[0].Status = "running"
	err = reopened.save()
	reopened.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	crashed, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	service.Store = crashed
	if err := crashed.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	if crashed.Snapshot().Actions[0].Status != "uncertain" {
		t.Fatal("crash lost uncertainty")
	}
	records, _ = wake.Pending(h.State)
	if len(records) != 1 {
		t.Fatal("crashed review delivered twice")
	}
}

func TestReviewTwoFilesReachCFOQueueWithoutWorkerSteering(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	for path, content := range map[string]string{"main.go": "package main\nfunc main() {\n println(2)\n}\n", "extra.go": "package main\nconst extra = 1\n"} {
		if err := os.WriteFile(filepath.Join(meta.Worktree, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sends := 0
	service := &Service{Store: store, Options: Options{
		Gate: fakeProgress{value: pipeline.Progress{Status: "running"}},
		Send: func(context.Context, string, string) error { sends++; return nil },
	}}
	for i, path := range []string{"main.go", "extra.go"} {
		last := 4
		if path == "extra.go" {
			last = 2
		}
		action := reviewAction(t, service, meta, fmt.Sprintf("review-%d", i), path, 2, last)
		if _, err := store.Queue(action); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Queue(action); err != nil {
			t.Fatal(err)
		}
		if err := store.ProcessOne(context.Background(), service.execute); err != nil {
			t.Fatal(err)
		}
		got := store.Snapshot().Actions[i]
		if got.Status != "succeeded" || !strings.Contains(got.Message, "CFO") {
			t.Fatalf("review outcome: %+v", got)
		}
	}
	if sends != 0 {
		t.Fatal("review was silently sent to worker")
	}
	records, err := wake.Pending(h.State)
	if err != nil || len(records) != 2 {
		t.Fatalf("queue: %+v %v", records, err)
	}
	for i, record := range records {
		// Decode using the wire field names, independently of the action type.
		var wire struct {
			ID         string `json:"id"`
			TaskID     string `json:"task_id"`
			Generation string `json:"generation"`
			File       string `json:"file"`
			Side       string `json:"side"`
			Head       string `json:"head"`
			DiffID     string `json:"diff_id"`
			Code       string `json:"code"`
			Text       string `json:"text"`
			Line       int    `json:"line"`
			EndLine    int    `json:"end_line"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(record.Detail, "review: ")), &wire); err != nil {
			t.Fatal(err)
		}
		if record.Kind != "notify" || record.Key != meta.ID || wire.TaskID != meta.ID || wire.Generation != "g1" || wire.Line != 2 || wire.Head == "" || wire.DiffID == "" || wire.Code == "" || wire.Text != "Please simplify this range" {
			t.Fatalf("missing context: %+v", wire)
		}
		if i == 0 && (wire.EndLine != 4 || wire.File != "main.go" || !strings.Contains(wire.Code, "println(2)")) {
			t.Fatalf("wrong main range: %+v", wire)
		}
		if i == 1 && (wire.EndLine != 2 || wire.File != "extra.go" || !strings.Contains(wire.Code, "const extra")) {
			t.Fatalf("wrong second file: %+v", wire)
		}
	}
	if episode, err := wake.ReadEpisode(h.State); err != nil || !episode.Pending {
		t.Fatal("review did not wake CFO")
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	service.Store = reopened
	if err := reopened.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	records, _ = wake.Pending(h.State)
	if len(records) != 2 || sends != 0 {
		t.Fatal("completed review replayed after restart")
	}
}
