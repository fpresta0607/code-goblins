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
	_, _, runner, cfo := primaryFixture(t, store)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	service := &Service{Store: store, Options: Options{CFO: cfo}}
	valid := reviewAction(t, service, meta, "review-stale", "main.go", 2, 2)
	valid, err := store.Queue(valid)
	if err != nil {
		t.Fatal(err)
	}
	if valid.CFOIdentity == "" {
		t.Fatal("test requires an admitted pinned action")
	}
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
	if len(runner.prompts) != 0 {
		t.Fatal("stale context reached native CFO")
	}
}

func TestReviewKeepsPinnedPrimaryAcrossRetryAndPreservesHistoricalWake(t *testing.T) {
	store, h := testStore(t)
	primary, identity, runner, cfo := primaryFixture(t, store)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	service := &Service{Store: store, Options: Options{CFO: cfo, Send: func(context.Context, string, string) error { t.Fatal("review sent to worker"); return nil }}}
	old, err := wake.Append(h.State, "notify", "unrelated", "blocked: retained historical wake")
	if err != nil {
		t.Fatal(err)
	}
	a := reviewAction(t, service, meta, "pinned-review", "main.go", 2, 2)
	queued, err := store.Queue(a)
	if err != nil {
		t.Fatal(err)
	}
	if queued.CFOIdentity != identity {
		t.Fatal("recipient not pinned on admission")
	}
	primary.Terminal = "replacement"
	data, _ := json.Marshal(primary)
	if err := os.WriteFile(filepath.Join(h.State, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	again, err := store.Queue(a)
	if err != nil || again.CFOIdentity != identity {
		t.Fatal("retry silently retargeted primary", err)
	}
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	if store.Snapshot().Actions[0].Status != "failed" || len(runner.prompts) != 0 {
		t.Fatal("stale primary received review")
	}
	// Old queued annotations without a recipient cannot be replayed into a new CFO.
	queued.ID = "legacy-review"
	queued.CFOIdentity = ""
	queued.Status = "queued"
	store.db.Actions = append(store.db.Actions, queued)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	if store.Snapshot().Actions[1].Status != "failed" || len(runner.prompts) != 0 {
		t.Fatal("legacy review was adopted by new transport")
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 1 || pending[0].Seq != old.Seq {
		t.Fatal("historical wake changed", err)
	}
}

func TestReviewUsesRequiredNativeChannelAcrossHarnesses(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			store, h := testStore(t)
			primary, _, runner, cfo := primaryFixture(t, store)
			primary.Agent = harness
			runner.harness = harness
			data, _ := json.Marshal(primary)
			if err := os.WriteFile(filepath.Join(h.State, "primary.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			meta, _ := state.ReadTaskMeta(h.State, "task-1")
			gitFixture(t, meta.Worktree)
			service := &Service{Store: store, Options: Options{CFO: cfo, Send: func(context.Context, string, string) error { t.Fatal("worker send"); return nil }}}
			if _, err := store.Queue(reviewAction(t, service, meta, "harness-review", "main.go", 2, 2)); err != nil {
				t.Fatal(err)
			}
			if err := store.ProcessOne(context.Background(), service.execute); err != nil {
				t.Fatal(err)
			}
			if len(runner.prompts) != 1 || store.Snapshot().Actions[0].Status != "succeeded" {
				t.Fatal("native review not accepted")
			}
		})
	}
}

func TestReviewCrashAfterNativeAcceptanceRemainsUncertain(t *testing.T) {
	store, h := testStore(t)
	_, _, runner, cfo := primaryFixture(t, store)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	service := &Service{Store: store, Options: Options{CFO: cfo}}
	action := reviewAction(t, service, meta, "review-crash", "main.go", 2, 2)
	if _, err := store.Queue(action); err != nil {
		t.Fatal(err)
	}
	var restore func()
	runner.beforePrompt = func() { restore = blockStoreWrites(t, store) }
	if err := store.ProcessOne(context.Background(), service.execute); !errors.Is(err, ErrStorage) {
		t.Fatal("expected failed outcome persistence", err)
	}
	restore()
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	service.Store = reopened
	if reopened.Snapshot().Actions[0].Status != "uncertain" {
		t.Fatal("native side effect was retried")
	}
	if _, err := reopened.Queue(action); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	if len(runner.prompts) != 1 {
		t.Fatal("accepted comment replayed")
	}
}

func TestReviewTwoFilesReachNativeCFOWithoutWorkerSteering(t *testing.T) {
	store, h := testStore(t)
	_, _, runner, cfo := primaryFixture(t, store)
	_ = runner
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	for path, content := range map[string]string{"main.go": "package main\nfunc main() {\n println(2)\n}\n", "extra.go": "package main\nconst extra = 1\n"} {
		if err := os.WriteFile(filepath.Join(meta.Worktree, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sends := 0
	service := &Service{Store: store, Options: Options{
		CFO:  cfo,
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
	if err != nil || len(records) != 0 || len(runner.prompts) != 2 {
		t.Fatalf("wrong transport: wake=%d native=%d error=%v", len(records), len(runner.prompts), err)
	}
	for i, prompt := range runner.prompts {
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
		if err := json.Unmarshal([]byte(strings.SplitN(prompt, "\n", 2)[1]), &wire); err != nil {
			t.Fatal(err)
		}
		if wire.TaskID != meta.ID || wire.Generation != "g1" || wire.Line != 2 || wire.Head == "" || wire.DiffID == "" || wire.Code == "" || wire.Text != "Please simplify this range" {
			t.Fatalf("missing context: %+v", wire)
		}
		if i == 0 && (wire.EndLine != 4 || wire.File != "main.go" || !strings.Contains(wire.Code, "println(2)")) {
			t.Fatalf("wrong main range: %+v", wire)
		}
		if i == 1 && (wire.EndLine != 2 || wire.File != "extra.go" || !strings.Contains(wire.Code, "const extra")) {
			t.Fatalf("wrong second file: %+v", wire)
		}
	}
	if episode, err := wake.ReadEpisode(h.State); err != nil || episode.Pending {
		t.Fatal("review duplicated actionable wake")
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
	if len(records) != 0 || sends != 0 || len(runner.prompts) != 2 {
		t.Fatal("completed review replayed after restart")
	}
}
