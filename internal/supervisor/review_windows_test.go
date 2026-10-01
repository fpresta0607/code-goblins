package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
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
	_, _, terminal, cfo := primaryFixture(t, store)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	service := &Service{Store: store, Options: Options{CFO: cfo}}
	ctx := context.Background()
	valid, err := store.QueueReview(reviewAction(t, service, meta, "review-stale", "main.go", 2, 2), cfo)
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
	if _, err := store.QueueReview(valid, cfo); err != nil {
		t.Fatal(err)
	}
	conflict := valid
	conflict.EndLine++
	if _, err := store.QueueReview(conflict, cfo); err == nil {
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
	if _, err := store.QueueReview(stale, cfo); err == nil {
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
	if typed := terminal.lines(t); len(typed) != 0 {
		t.Fatalf("stale context reached the CFO's terminal: %q", typed)
	}
}

// A stale registration used to answer 202 Queued and fail only at delivery.
// Admission now proves the registered CFO live first; a refusal leaves no
// durable action and sends nothing.
func TestReviewAdmissionRefusesAStaleCFOBeforeQueueing(t *testing.T) {
	for _, c := range []struct {
		name, reason string
		stale        func(*testing.T, string, primaryRegistration, hostedTerminal)
	}{
		{"exited process", "process is unavailable", func(t *testing.T, dir string, primary primaryRegistration, _ hostedTerminal) {
			primary.Process.Start = primary.Process.Start.Add(-time.Hour)
			data, _ := json.Marshal(primary)
			if err := os.WriteFile(filepath.Join(dir, "primary.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"another program in its terminal", "ended or runs another program", func(t *testing.T, _ string, _ primaryRegistration, terminal hostedTerminal) {
			terminal.runs(t, 4)
		}},
		{"a terminal that ended", "ended or runs another program", func(t *testing.T, _ string, _ primaryRegistration, terminal hostedTerminal) {
			terminal.exit(t)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			store, h := testStore(t)
			primary, _, terminal, cfo := primaryFixture(t, store)
			meta, _ := state.ReadTaskMeta(h.State, "task-1")
			gitFixture(t, meta.Worktree)
			service := &Service{Store: store, Options: Options{CFO: cfo}}
			action := reviewAction(t, service, meta, "stale-cfo-review", "main.go", 2, 2)
			c.stale(t, h.State, primary, terminal)
			_, err := store.QueueReview(action, cfo)
			if err == nil || !strings.Contains(err.Error(), c.reason) || !strings.Contains(err.Error(), "No review was queued") {
				t.Fatalf("stale CFO admission: %v", err)
			}
			reopened, err := Open(h)
			if err != nil {
				t.Fatal(err)
			}
			if len(store.Snapshot().Actions) != 0 || len(reopened.Snapshot().Actions) != 0 || len(terminal.lines(t)) != 0 {
				t.Fatal("refused review left an action or reached the CFO")
			}
		})
	}
}

// The browser path: a stale registration is refused with 409 before anything
// is queued, and the same unchanged request is admitted once the CFO is live.
func TestReviewEndpointVerifiesTheCFOBeforeQueueing(t *testing.T) {
	store, _ := testStore(t)
	_, identity, terminal, cfo := primaryFixture(t, store)
	h := NewHTTP(&Service{Store: store, Options: Options{CFO: cfo}, Instance: "instance", done: make(chan struct{})}, "", nil)
	server := httptest.NewServer(h)
	h.Host = strings.TrimPrefix(server.URL, "http://")
	t.Cleanup(server.Close)
	meta, _ := state.ReadTaskMeta(store.Home.State, "task-1")
	gitFixture(t, meta.Worktree)
	body := reviewJSON(t, h.Service, meta, "endpoint-review", "main.go", 2, 2)
	terminal.runs(t, 4)
	response := terminalPost(t, server, "/api/actions", body)
	response.Body.Close()
	if response.StatusCode != 409 || len(store.Snapshot().Actions) != 0 {
		t.Fatalf("stale CFO review: status %d, actions %+v", response.StatusCode, store.Snapshot().Actions)
	}
	terminal.standIn(t)
	response = terminalPost(t, server, "/api/actions", body)
	var admitted Action
	err := json.NewDecoder(response.Body).Decode(&admitted)
	response.Body.Close()
	if err != nil || response.StatusCode != 202 || admitted.CFOIdentity != identity || admitted.Status != "queued" || len(terminal.lines(t)) != 0 {
		t.Fatalf("live CFO review: status %d, action %+v, error %v", response.StatusCode, admitted, err)
	}
}

func TestReviewKeepsPinnedPrimaryAcrossRetryAndPreservesHistoricalWake(t *testing.T) {
	store, h := testStore(t)
	primary, identity, terminal, cfo := primaryFixture(t, store)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	service := &Service{Store: store, Options: Options{CFO: cfo}}
	old, err := wake.Append(h.State, "notify", "unrelated", "blocked: retained historical wake")
	if err != nil {
		t.Fatal(err)
	}
	a := reviewAction(t, service, meta, "pinned-review", "main.go", 2, 2)
	queued, err := store.QueueReview(a, cfo)
	if err != nil {
		t.Fatal(err)
	}
	if queued.CFOIdentity != identity || len(terminal.lines(t)) != 0 {
		t.Fatal("admission did not pin the recipient, or it sent")
	}
	// Another registration replaces the one the review was pinned to.
	primary.Agent = "claude"
	data, _ := json.Marshal(primary)
	if err := os.WriteFile(filepath.Join(h.State, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	again, err := store.QueueReview(a, cfo)
	if err != nil || again.CFOIdentity != identity || len(store.Snapshot().Actions) != 1 {
		t.Fatal("retry retargeted the primary", err)
	}
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	if store.Snapshot().Actions[0].Status != "failed" || len(terminal.lines(t)) != 0 {
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
	if store.Snapshot().Actions[1].Status != "failed" || len(terminal.lines(t)) != 0 {
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
			primary, _, terminal, cfo := primaryFixture(t, store)
			primary.Agent = harness
			data, _ := json.Marshal(primary)
			if err := os.WriteFile(filepath.Join(h.State, "primary.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			meta, _ := state.ReadTaskMeta(h.State, "task-1")
			gitFixture(t, meta.Worktree)
			service := &Service{Store: store, Options: Options{CFO: cfo}}
			if _, err := store.QueueReview(reviewAction(t, service, meta, "harness-review", "main.go", 2, 2), cfo); err != nil {
				t.Fatal(err)
			}
			if err := store.ProcessOne(context.Background(), service.execute); err != nil {
				t.Fatal(err)
			}
			if typed := terminal.lines(t); len(typed) != 1 || store.Snapshot().Actions[0].Status != "succeeded" {
				t.Fatalf("native review not accepted: the terminal received %q, action %+v", typed, store.Snapshot().Actions[0])
			}
		})
	}
}

// An empty untracked file has no line 1 to annotate; a lone newline has one.
func TestReviewCannotDeliverALineAZeroByteFileDoesNotHave(t *testing.T) {
	store, h := testStore(t)
	_, _, terminal, cfo := primaryFixture(t, store)
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
		before := len(terminal.lines(t))
		if _, err := store.QueueReview(reviewAction(t, service, meta, fmt.Sprintf("line-one-%d", i), c.path, 1, 1), cfo); err != nil {
			t.Fatal(err)
		}
		if err := store.ProcessOne(context.Background(), service.execute); err != nil {
			t.Fatal(err)
		}
		got := store.Snapshot().Actions[i]
		if !c.delivered {
			if got.Status != "failed" || !strings.Contains(got.Message, "contiguous visible diff range") || len(terminal.lines(t)) != before {
				t.Fatalf("%s: a missing line 1 was delivered: %+v", c.path, got)
			}
			continue
		}
		typed := terminal.lines(t)
		if got.Status != "succeeded" || len(typed) != before+1 {
			t.Fatalf("%s: line 1 was not delivered on one line: %+v, the terminal received %q", c.path, got, typed)
		}
		var wire struct {
			Code string `json:"code"`
		}
		prompt := typed[before]
		if err := json.Unmarshal([]byte(prompt[strings.Index(prompt, "{"):]), &wire); err != nil || wire.Code != c.code {
			t.Fatalf("%s: delivered context %q %v", c.path, wire.Code, err)
		}
	}
}

func TestReviewCrashAfterNativeAcceptanceRemainsUncertain(t *testing.T) {
	store, h := testStore(t)
	_, _, terminal, cfo := primaryFixture(t, store)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	service := &Service{Store: store, Options: Options{CFO: cfo}}
	action := reviewAction(t, service, meta, "review-crash", "main.go", 2, 2)
	if _, err := store.QueueReview(action, cfo); err != nil {
		t.Fatal(err)
	}
	// The store stops taking writes once the CFO has the review, so its
	// outcome cannot be recorded.
	var restore func()
	delivered := func(ctx context.Context, a Action) (Evaluation, error) {
		evaluation, err := service.execute(ctx, a)
		restore = blockStoreWrites(t, store)
		return evaluation, err
	}
	if err := store.ProcessOne(context.Background(), delivered); !errors.Is(err, ErrStorage) {
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
	if _, err := reopened.QueueReview(action, cfo); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	if typed := terminal.lines(t); len(typed) != 1 {
		t.Fatalf("the CFO's terminal received %q, want the accepted comment once and never replayed", typed)
	}
}

func TestReviewTwoFilesReachNativeCFOWithoutWorkerSteering(t *testing.T) {
	store, h := testStore(t)
	_, _, terminal, cfo := primaryFixture(t, store)
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
		if _, err := store.QueueReview(action, cfo); err != nil {
			t.Fatal(err)
		}
		if _, err := store.QueueReview(action, cfo); err != nil {
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
	typed := terminal.lines(t)
	if err != nil || len(records) != 0 || len(typed) != 2 {
		t.Fatalf("wrong transport: wake=%d native=%q error=%v", len(records), typed, err)
	}
	for i, prompt := range typed {
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
		if err := json.Unmarshal([]byte(prompt[strings.Index(prompt, "{"):]), &wire); err != nil {
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
	if len(records) != 0 || len(terminal.lines(t)) != 2 {
		t.Fatal("completed review replayed after restart")
	}
}
