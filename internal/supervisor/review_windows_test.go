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
	"time"

	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// reviewJSON is the browser's request body for a range of the current diff.
func reviewJSON(t *testing.T, service *Service, meta state.TaskMeta, id, path string, first, last int) string {
	t.Helper()
	diff, err := service.previewGit(meta).Diff(context.Background(), meta.Worktree, "", path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{"id":%q,"kind":"review","task_id":%q,"generation":%q,"text":"Please simplify this range","file":%q,"line":%d,"end_line":%d,"side":"new","head":%q,"diff_id":%q}`, id, meta.ID, meta.SpawnGen, path, first, last, diff.Head, diff.Fingerprint)
}

func reviewAction(t *testing.T, service *Service, meta state.TaskMeta, id, path string, first, last int) Action {
	t.Helper()
	var action Action
	if err := json.Unmarshal([]byte(reviewJSON(t, service, meta, id, path, first, last)), &action); err != nil {
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
	ctx := context.Background()
	valid, err := store.QueueReview(ctx, reviewAction(t, service, meta, "review-stale", "main.go", 2, 2), cfo)
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
		if _, err := service.execute(ctx, action); !errors.Is(err, ErrRejected) {
			t.Fatalf("invalid selection not rejected: %v", err)
		}
	}
	if _, err := store.QueueReview(ctx, valid, cfo); err != nil {
		t.Fatal(err)
	}
	conflict := valid
	conflict.EndLine++
	if _, err := store.QueueReview(ctx, conflict, cfo); err == nil {
		t.Fatal("same ID accepted with another range")
	}
	// A browser-held draft must not adopt the next spawn generation, and a
	// new request ID is what makes this a new admission rather than a retry.
	meta.SpawnGen = "g2"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	stale := valid
	stale.ID = "review-after-respawn"
	if _, err := store.QueueReview(ctx, stale, cfo); err == nil {
		t.Fatal("old generation accepted after respawn")
	}
	if len(store.Snapshot().Actions) != 1 {
		t.Fatal("refused admission queued an action")
	}
	if err := store.ProcessOne(ctx, service.execute); err != nil {
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

// A stale registration used to answer 202 Queued and fail only at delivery.
// Admission now proves the registered CFO live first; a refusal leaves no
// durable action and sends nothing.
func TestReviewAdmissionRefusesAStaleCFOBeforeQueueing(t *testing.T) {
	for _, c := range []struct {
		name, reason string
		stale        func(*testing.T, string, primaryRegistration, *cfoRunner)
	}{
		{"exited process", "process is unavailable", func(t *testing.T, dir string, primary primaryRegistration, _ *cfoRunner) {
			primary.Process.Start = primary.Process.Start.Add(-time.Hour)
			data, _ := json.Marshal(primary)
			if err := os.WriteFile(filepath.Join(dir, "primary.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"replaced terminal", "terminal changed", func(_ *testing.T, _ string, _ primaryRegistration, runner *cfoRunner) {
			runner.terminal = "replacement-terminal"
		}},
		{"shell back in the foreground", "no longer owns its pane", func(_ *testing.T, _ string, _ primaryRegistration, runner *cfoRunner) {
			runner.pid = 1
		}},
		{"Herdr unavailable", "Herdr cannot verify", func(_ *testing.T, _ string, _ primaryRegistration, runner *cfoRunner) {
			runner.offline = true
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			store, h := testStore(t)
			primary, _, runner, cfo := primaryFixture(t, store)
			meta, _ := state.ReadTaskMeta(h.State, "task-1")
			gitFixture(t, meta.Worktree)
			service := &Service{Store: store, Options: Options{CFO: cfo}}
			action := reviewAction(t, service, meta, "stale-cfo-review", "main.go", 2, 2)
			c.stale(t, h.State, primary, runner)
			_, err := store.QueueReview(context.Background(), action, cfo)
			if err == nil || !strings.Contains(err.Error(), c.reason) || !strings.Contains(err.Error(), "No review was queued") {
				t.Fatalf("stale CFO admission: %v", err)
			}
			reopened, err := Open(h)
			if err != nil {
				t.Fatal(err)
			}
			if len(store.Snapshot().Actions) != 0 || len(reopened.Snapshot().Actions) != 0 || len(runner.prompts) != 0 {
				t.Fatal("refused review left an action or reached the CFO")
			}
		})
	}
}

// The browser path: a stale registration is refused with 409 before anything
// is queued, and the same unchanged request is admitted once the CFO is live.
func TestReviewEndpointVerifiesTheCFOBeforeQueueing(t *testing.T) {
	h, server, identity, runner := terminalHTTPFixture(t, newTestTerminal())
	store := h.Service.Store
	meta, _ := state.ReadTaskMeta(store.Home.State, "task-1")
	gitFixture(t, meta.Worktree)
	body := reviewJSON(t, h.Service, meta, "endpoint-review", "main.go", 2, 2)
	runner.terminal = "replacement-terminal"
	response := terminalPost(t, server, "/api/actions", body)
	response.Body.Close()
	if response.StatusCode != 409 || len(store.Snapshot().Actions) != 0 {
		t.Fatalf("stale CFO review: status %d, actions %+v", response.StatusCode, store.Snapshot().Actions)
	}
	runner.terminal = ""
	response = terminalPost(t, server, "/api/actions", body)
	var admitted Action
	err := json.NewDecoder(response.Body).Decode(&admitted)
	response.Body.Close()
	if err != nil || response.StatusCode != 202 || admitted.CFOIdentity != identity || admitted.Status != "queued" || len(runner.prompts) != 0 {
		t.Fatalf("live CFO review: status %d, action %+v, error %v", response.StatusCode, admitted, err)
	}
}

func TestReviewKeepsPinnedPrimaryAcrossRetryAndPreservesHistoricalWake(t *testing.T) {
	store, h := testStore(t)
	primary, identity, runner, cfo := primaryFixture(t, store)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	service := &Service{Store: store, Options: Options{CFO: cfo}}
	old, err := wake.Append(h.State, "notify", "unrelated", "blocked: retained historical wake")
	if err != nil {
		t.Fatal(err)
	}
	a := reviewAction(t, service, meta, "pinned-review", "main.go", 2, 2)
	queued, err := store.QueueReview(context.Background(), a, cfo)
	if err != nil {
		t.Fatal(err)
	}
	probes := runner.calls
	if queued.CFOIdentity != identity || probes == 0 || len(runner.prompts) != 0 {
		t.Fatal("admission did not probe and pin the recipient, or it sent")
	}
	primary.Terminal = "replacement"
	data, _ := json.Marshal(primary)
	if err := os.WriteFile(filepath.Join(h.State, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	again, err := store.QueueReview(context.Background(), a, cfo)
	if err != nil || again.CFOIdentity != identity || runner.calls != probes || len(store.Snapshot().Actions) != 1 {
		t.Fatal("retry reprobed or retargeted the primary", err)
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
			service := &Service{Store: store, Options: Options{CFO: cfo}}
			if _, err := store.QueueReview(context.Background(), reviewAction(t, service, meta, "harness-review", "main.go", 2, 2), cfo); err != nil {
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

// An empty untracked file has no line 1 to annotate; a lone newline has one.
func TestReviewCannotDeliverALineAZeroByteFileDoesNotHave(t *testing.T) {
	store, h := testStore(t)
	_, _, runner, cfo := primaryFixture(t, store)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	service := &Service{Store: store, Options: Options{CFO: cfo}}
	for i, c := range []struct {
		path, content, code string
		delivered           bool
	}{
		{"empty.txt", "", "", false},
		{"newline.txt", "\n", "\n", true},
		{"text.txt", "first\nsecond\n", "first\n", true},
	} {
		if err := os.WriteFile(filepath.Join(meta.Worktree, c.path), []byte(c.content), 0600); err != nil {
			t.Fatal(err)
		}
		prompts := len(runner.prompts)
		if _, err := store.QueueReview(context.Background(), reviewAction(t, service, meta, fmt.Sprintf("line-one-%d", i), c.path, 1, 1), cfo); err != nil {
			t.Fatal(err)
		}
		if err := store.ProcessOne(context.Background(), service.execute); err != nil {
			t.Fatal(err)
		}
		got := store.Snapshot().Actions[i]
		if !c.delivered {
			if got.Status != "failed" || !strings.Contains(got.Message, "contiguous visible diff range") || len(runner.prompts) != prompts {
				t.Fatalf("%s: a missing line 1 was delivered: %+v", c.path, got)
			}
			continue
		}
		if got.Status != "succeeded" || len(runner.prompts) != prompts+1 {
			t.Fatalf("%s: line 1 was not delivered: %+v", c.path, got)
		}
		var wire struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal([]byte(strings.SplitN(runner.prompts[prompts], "\n", 2)[1]), &wire); err != nil || wire.Code != c.code {
			t.Fatalf("%s: delivered context %q %v", c.path, wire.Code, err)
		}
	}
}

func TestReviewCrashAfterNativeAcceptanceRemainsUncertain(t *testing.T) {
	store, h := testStore(t)
	_, _, runner, cfo := primaryFixture(t, store)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	service := &Service{Store: store, Options: Options{CFO: cfo}}
	action := reviewAction(t, service, meta, "review-crash", "main.go", 2, 2)
	if _, err := store.QueueReview(context.Background(), action, cfo); err != nil {
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
	if _, err := reopened.QueueReview(context.Background(), action, cfo); err != nil {
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
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	for path, content := range map[string]string{"main.go": "package main\nfunc main() {\n println(2)\n}\n", "extra.go": "package main\nconst extra = 1\n"} {
		if err := os.WriteFile(filepath.Join(meta.Worktree, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{Store: store, Options: Options{
		CFO:  cfo,
		Gate: fakeProgress{value: pipeline.Progress{Status: "running"}},
	}}
	for i, path := range []string{"main.go", "extra.go"} {
		last := 4
		if path == "extra.go" {
			last = 2
		}
		action := reviewAction(t, service, meta, fmt.Sprintf("review-%d", i), path, 2, last)
		if _, err := store.QueueReview(context.Background(), action, cfo); err != nil {
			t.Fatal(err)
		}
		if _, err := store.QueueReview(context.Background(), action, cfo); err != nil {
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
	if len(records) != 0 || len(runner.prompts) != 2 {
		t.Fatal("completed review replayed after restart")
	}
}
