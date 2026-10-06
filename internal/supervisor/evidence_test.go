package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestFleetEvaluationPrefersAWaitingQuestionThenTheGateThenHerdr(t *testing.T) {
	meta := state.TaskMeta{ID: "g1", SpawnGen: "gen2"}
	busy := RuntimeEvidence{State: "busy", Reason: "Herdr reports busy"}
	idle := RuntimeEvidence{State: "idle", Reason: "Herdr reports idle"}
	none := RuntimeEvidence{State: "unknown", Reason: "Current Herdr liveness evidence is unavailable"}
	ready := Evaluation{Phase: "ready", Generation: "gen2", PR: "https://example/pr/1"}
	question := []wake.Record{{Seq: 3, Kind: "notify", Key: "g1", Detail: "blocked: Which schema? options: a | b"}}
	asked := []wake.Record{{Seq: 4, Kind: "stale", Key: "g1", Detail: proseAsk}}
	for _, c := range []struct {
		name       string
		evaluation Evaluation
		runtime    RuntimeEvidence
		records    []wake.Record
		phase      string
		reason     string
	}{
		{"a waiting question outranks the gate and the pane", ready, busy, question, "blocked", "Waiting on the CFO: Which schema? options: a | b"},
		{"another task's question does not block this one", ready, busy, []wake.Record{{Kind: "notify", Key: "g2", Detail: "blocked: other"}}, "ready", ""},
		{"a question answered on the board no longer blocks", ready, busy, []wake.Record{{Seq: 3, Kind: "notify", Key: "g1", Detail: "blocked: Which schema? options: a | b", Answered: "b"}}, "ready", ""},
		{"a done notify is not a question", Evaluation{}, busy, []wake.Record{{Kind: "notify", Key: "g1", Detail: "done: PR https://example/pr/1"}}, "working", "Herdr reports busy"},
		{"a question asked in prose waits like a notify", ready, busy, asked, "blocked", "Waiting on the CFO: Which layout do you want?"},
		{"an idle goblin asked nothing", Evaluation{}, idle, []wake.Record{{Seq: 4, Kind: "stale", Key: "g1", Detail: "goblin_idle: at its prompt for 3m"}}, "idle", "Herdr reports idle"},
		{"the gate outranks the pane", ready, busy, nil, "ready", ""},
		{"a busy pane is working", Evaluation{Phase: "review", Generation: "gen2"}, busy, nil, "working", "Herdr reports busy"},
		{"an idle pane is awaiting input", Evaluation{}, idle, nil, "idle", "Herdr reports idle"},
		{"a prior generation's delivery is never reused", Evaluation{Phase: "done", Generation: "gen1", Verified: true}, none, nil, "unknown", "Current Herdr liveness evidence is unavailable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := fleetEvaluation(c.evaluation, meta, c.runtime, c.records)
			if got.Phase != c.phase || got.Reason != c.reason || got.Generation != "gen2" || got.Verified && c.phase != "ready" {
				t.Fatalf("fleetEvaluation = %+v, want phase %q reason %q in generation gen2", got, c.phase, c.reason)
			}
		})
	}
}

// The live board read every goblin as awaiting evidence while all of them
// were working, because no native hook had reported them. The fleet's own
// records answer for them now.
func TestSnapshotShowsWhatTheFleetKnowsForATaskNoHookReported(t *testing.T) {
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"done: PR https://example/pr/7", "working: gate test step"} {
		if err := state.AppendStatus(h.State, "task-1", line); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	if err := monitor.WriteObservation(h.State, monitor.Observation{TaskID: "task-1", Endpoint: (herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: now, Health: monitor.HealthBusy, Reason: monitor.None, Digest: "d", LastSeen: now, LastProgress: now}); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store}
	view, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Tasks[0]; got.Phase != "working" || got.Activity != "working: gate test step" || got.PR != "https://example/pr/7" {
		t.Fatalf("task = phase %q activity %q pr %q, want working with its own status line and PR", got.Phase, got.Activity, got.PR)
	}
	// cfo notify writes the status line before the wake record, as here.
	if err := state.AppendStatus(h.State, "task-1", "blocked: Which schema?"); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Append(h.State, "notify", "task-1", "blocked: Which schema?"); err != nil {
		t.Fatal(err)
	}
	if view, err = service.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if got := view.Tasks[0]; got.Phase != "blocked" || got.Activity != "Which schema?" {
		t.Fatalf("waiting task = phase %q activity %q, want blocked showing its question", got.Phase, got.Activity)
	}
}

// proseAsk is the monitor's wake for a goblin that ended its turn asking in
// prose, as it raises it.
const proseAsk = `goblin_asks: g1 ended its turn asking in prose instead of with cfo notify --blocked and waits at its prompt for the answer; next: answer it with cfo send g1 "<your answer>" (cfo answer takes only a notify's question). It asked: "Which layout do you want?"`

// A goblin that ended its turn asking in prose shows on the board as waiting
// on the CFO with its question, as one that asked with cfo notify --blocked
// does, until it reports again.
func TestSnapshotShowsAGoblinThatAskedInProseAsWaitingOnTheCFO(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	now := time.Now().UTC()
	writeFile(t, state.StatusPath(h.State, "task-1"), now.Add(-time.Minute).Format(time.RFC3339)+" working: building the layout\n")
	if _, err := wake.Append(h.State, "stale", "task-1", strings.ReplaceAll(proseAsk, "g1", "task-1")); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store}

	// Act
	view, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, state.StatusPath(h.State, "task-1"), now.Add(-time.Minute).Format(time.RFC3339)+" working: building the layout\n"+now.Add(time.Minute).Format(time.RFC3339)+" working: grid layout, as the CFO answered\n")
	after, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if got := view.Tasks[0]; got.Phase != "blocked" || got.Reason != "Waiting on the CFO: Which layout do you want?" || got.Activity != "Which layout do you want?" {
		t.Fatalf("asking task = phase %q reason %q activity %q, want it waiting on the CFO with its question", got.Phase, got.Reason, got.Activity)
	}
	if got := after.Tasks[0]; got.Phase == "blocked" {
		t.Fatalf("after its next report the task still reads %q: %q", got.Phase, got.Reason)
	}
}

// A goblin's own failed report leaves its phase to the evidence, so the board
// alerts on it only through the report kind the snapshot carries.
func TestSnapshotReportNamesTheKindOfATasksLatestReport(t *testing.T) {
	store, h := testStore(t)
	service := &Service{Store: store}
	for _, c := range []struct{ line, report string }{
		{"working: gate test step", "working"},
		{"failed: The build broke on a missing asset", "failed"},
		{"done: PR https://example/pr/7", "done"},
	} {
		if err := state.AppendStatus(h.State, "task-1", c.line); err != nil {
			t.Fatal(err)
		}
		view, err := service.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(view.Tasks[0])
		if err != nil {
			t.Fatal(err)
		}
		var sent struct {
			Report string `json:"report"`
		}
		if err := json.Unmarshal(encoded, &sent); err != nil {
			t.Fatal(err)
		}
		if sent.Report != c.report {
			t.Errorf("after %q the snapshot sends report %q, want %q", c.line, sent.Report, c.report)
		}
	}
}

// A blocked or failed notify the CFO handled still holds the task, as the hold
// tests in hold_status_test.go require, and is marked as the CFO's so the board
// never announces it as the goblin's news, through a reconnect and later gate
// transitions.
func TestSnapshotMarksAHandledNotifyAsTheCFOsThroughReconnectAndGateTransitions(t *testing.T) {
	for _, verb := range []string{"blocked", "failed"} {
		for _, handling := range []string{"answer then drain", "drain without a waiting snapshot"} {
			t.Run(verb+"/"+handling, func(t *testing.T) {
				store, h := testStore(t)
				service := &Service{Store: store}
				before := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
				meta, err := state.ReadTaskMeta(h.State, "task-1")
				if err != nil {
					t.Fatal(err)
				}
				meta.SpawnGen = fmt.Sprintf("s%d", before.Add(-time.Second).UnixNano())
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
					t.Fatal(err)
				}
				question := verb + ": Which fix? options: Retry | Revert"
				lines := before.Format(time.RFC3339) + " working: implementing the fix\n" + before.Add(time.Second).Format(time.RFC3339) + " " + question + "\n"
				if err := os.WriteFile(state.StatusPath(h.State, "task-1"), []byte(lines), 0600); err != nil {
					t.Fatal(err)
				}
				record, err := wake.Append(h.State, "notify", "task-1", question)
				if err != nil {
					t.Fatal(err)
				}
				if handling == "answer then drain" {
					view, err := service.Snapshot()
					if err != nil || view.Tasks[0].Phase != verb || view.Tasks[0].ReportHandled {
						t.Fatalf("waiting snapshot = %+v, %v; want the question waiting and not handled", view.Tasks, err)
					}
					if err := wake.MarkAnswered(h.State, record.Seq, wake.AnsweredByCFO, "Retry"); err != nil {
						t.Fatal(err)
					}
					view, err = service.Snapshot()
					if err != nil || view.Tasks[0].Report != verb || !view.Tasks[0].ReportHandled {
						t.Errorf("answered snapshot = %+v, %v; want the %s report held and marked handled", view.Tasks, err, verb)
					}
				}
				if err := wake.AckThrough(h.State, record.Seq); err != nil {
					t.Fatal(err)
				}
				// Reopen the store and service: the stream may miss every waiting snapshot.
				store, err = Open(h)
				if err != nil {
					t.Fatal(err)
				}
				service = &Service{Store: store}
				for _, phase := range []string{"review", "blocked", "review"} {
					store.db.Tasks["task-1"] = Evaluation{Phase: phase, Reason: "gate " + phase, Generation: meta.SpawnGen, At: time.Now()}
					if err := store.save(); err != nil {
						t.Fatal(err)
					}
					view, err := service.Snapshot()
					if err != nil {
						t.Fatal(err)
					}
					isGateBlock := phase == "blocked"
					if task := view.Tasks[0]; task.Report != verb || task.ReportHandled == isGateBlock || isGateBlock && (task.Phase != "blocked" || task.Reason != "gate blocked") || !isGateBlock && task.Phase != verb {
						t.Errorf("after handling, gate %s sends %+v; want the %s report holding the task marked handled, or the gate's own block unmarked", phase, task, verb)
					}
				}
				// Even the same words reported again are new failure evidence.
				later := record.Time.Truncate(time.Second).Format(time.RFC3339) + " " + question + "\n"
				file, err := os.OpenFile(state.StatusPath(h.State, "task-1"), os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.WriteString(later)
				if closeErr := file.Close(); err != nil || closeErr != nil {
					t.Fatalf("append later report: %v, %v", err, closeErr)
				}
				view, err := service.Snapshot()
				if err != nil || view.Tasks[0].Report != verb || view.Tasks[0].ReportHandled {
					t.Fatalf("new report = %+v, %v; want an unhandled %s", view.Tasks, err, verb)
				}
			})
		}
	}
}

// A native task's runtime line names its terminal, which the monitor reads
// it from, never Herdr.
func TestANativeTasksRuntimeNamesItsTerminal(t *testing.T) {
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Backend = "native"
	meta.HerdrSession, meta.HerdrWorkspaceID, meta.HerdrTabID, meta.HerdrPaneID = "", "", "", ""
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := monitor.WriteObservation(h.State, monitor.Observation{TaskID: "task-1", Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: now, Health: monitor.HealthBusy, Reason: monitor.None, Digest: "d", LastSeen: now, LastProgress: now}); err != nil {
		t.Fatal(err)
	}

	view, err := (&Service{Store: store}).Snapshot()

	if err != nil {
		t.Fatal(err)
	}
	if got := view.Tasks[0].Runtime; got.State != "busy" || got.Reason != "Native terminal reports busy" {
		t.Errorf("runtime = %+v, want busy as its native terminal reports it", got)
	}
}

func TestFinishedTasksReadEveryCleanupLayout(t *testing.T) {
	stateDir := t.TempDir()
	archive := filepath.Join(stateDir, state.ArchiveDirName)
	now := time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC)
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Cleaned up: the status log stays, with no task record beside it.
	write(filepath.Join(stateDir, "cleaned.status"), "done: PR https://example/pr/1\ndone: returned worktree via cfo cleanup\n")
	// Live: a task record beside the log means it is not finished.
	write(filepath.Join(stateDir, "live.status"), "working: still going\n")
	write(filepath.Join(stateDir, "live.meta"), "id=live\n")
	// Reaped into its own archive file, and archived inside its directory.
	write(filepath.Join(archive, "reaped.status.20260922T101500Z"), "done: PR https://example/pr/2\n")
	write(filepath.Join(archive, "inside.20260923T144753Z", "inside.status"), "done: report written\n")
	// Outside the history window.
	write(filepath.Join(archive, "old.status.20260901T000000Z"), "done: PR https://example/pr/3\n")
	if err := os.Chtimes(filepath.Join(stateDir, "cleaned.status"), now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	finished := finishedTasks(home.Home{State: stateDir, Data: t.TempDir()}, now)
	got := map[string]string{}
	for _, task := range finished {
		if !task.Archived || task.PR != "" && task.Phase != "done" || task.PR == "" && task.Phase != "stopped" {
			t.Fatalf("finished task %+v is not archived history", task)
		}
		got[task.Title] = task.PR
	}
	want := map[string]string{"cleaned": "https://example/pr/1", "reaped": "https://example/pr/2", "inside": ""}
	if len(got) != len(want) {
		t.Fatalf("finished = %v, want %v", got, want)
	}
	for id, pr := range want {
		if got[id] != pr {
			t.Fatalf("finished = %v, want %v", got, want)
		}
	}
	if finished[0].Title != "cleaned" || finished[len(finished)-1].Title != "reaped" {
		t.Fatalf("finished order = %v, want newest first", finished)
	}
}

func TestCompletedStoppedCardRetainsTaskTitleRepositoryAndPullRequest(t *testing.T) {
	h := home.Home{State: t.TempDir(), Data: t.TempDir()}
	if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte("## Done\n- **launch-check** - Check launch behavior (repo: example)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "launch-check.status"), []byte("done: returned worktree via cfo cleanup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteOutcome(h.State, state.Outcome{ID: "delivered", Title: "Delivered title", Project: "example", Phase: "stopped", PR: "https://github.com/owner/example/pull/42", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: "delivered", Operation: "stop-1", Action: "stop", Phase: "stopped", Title: "Delivered title", Project: "example", Updated: time.Now()}); err != nil {
		t.Fatal(err)
	}
	tasks := finishedTasks(h, time.Now())
	if len(tasks) != 2 {
		t.Fatalf("Completed=%+v", tasks)
	}
	for _, task := range tasks {
		if task.Phase != "stopped" || task.Project != "example" || task.Title == strings.TrimPrefix(task.ID, "finished:") || task.ID == "finished:delivered" && task.PR == "" {
			t.Fatalf("inconsistent Completed card: %+v", task)
		}
	}
}

func TestMergedPullRequestsJoinTheTaskThatReportedThem(t *testing.T) {
	at := time.Date(2026, 9, 23, 16, 0, 0, 0, time.UTC)
	history := withMergedPRs([]Task{{ID: "finished:a", Title: "a", Archived: true, Evaluation: Evaluation{Phase: "done", PR: "https://example/pr/1", At: at}}}, []MergedPR{
		{PR: "https://example/pr/1", Branch: "fix/a", Project: `C:\dev\code-goblins`, At: at.Unix()},
		{PR: "https://example/pr/2", Branch: "fix/b", Project: `C:\dev\code-goblins`, At: at.Add(time.Hour).Unix()},
	})
	if len(history) != 2 || history[0].ID != "merged:https://example/pr/2" || history[0].Branch != "fix/b" || history[0].Title == "fix/b" || history[0].Project != "code-goblins" || !history[0].Merged {
		t.Fatalf("history = %+v, want the unclaimed merge first as its own entry", history)
	}
	if !history[1].Merged || history[1].ID != "finished:a" {
		t.Fatalf("history = %+v, want the finished task to carry its merged PR", history)
	}
}

func TestQueuedBriefsListOnlyBriefsNothingStarted(t *testing.T) {
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	for _, path := range []string{
		filepath.Join(h.Data, "queued", "brief.md"),
		filepath.Join(h.Data, "live", "brief.md"),
		filepath.Join(h.Data, "cleaned", "brief.md"),
		filepath.Join(h.Data, "archived", "brief.md"),
		filepath.Join(h.Data, "notes-only", "report.md"),
		filepath.Join(h.State, "live.meta"),
		filepath.Join(h.State, "cleaned.status"),
		filepath.Join(h.State, state.ArchiveDirName, "archived.20260920T000000Z", "handoff.md"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	queued := queuedBriefs(h, diskBriefs)
	if len(queued) != 1 || queued[0].ID != "queued" || queued[0].Phase != "queued" {
		t.Fatalf("queued = %+v, want only the brief nothing started", queued)
	}
}

func TestReconcileEvaluatesTasksNoHookReported(t *testing.T) {
	store, _ := testStore(t)
	if err := (&Service{Store: store}).reconcileTasks(time.Now()); err != nil {
		t.Fatal(err)
	}
	actions := store.Snapshot().Actions
	if len(actions) != 1 || actions[0].Kind != "evaluate" || actions[0].TaskID != "task-1" || actions[0].Session != "" {
		t.Fatalf("actions = %+v, want one evaluation of the task no hook reported", actions)
	}
}

// The reconcile leaves a task alone while an evaluation of it is pending, and
// that holds for one queued while the reconcile reads the task's metadata:
// deciding from the snapshot it took first queued a second evaluation behind
// a board request (TestAPIOriginIdempotencySafePathsAndReconnect on CI).
func TestReconcileNeverQueuesAnEvaluationBehindOnePending(t *testing.T) {
	for round := 0; round < 200; round++ {
		// Arrange
		store, _ := testStore(t)
		service := &Service{Store: store}
		start := make(chan struct{})
		var both sync.WaitGroup
		both.Add(2)
		// Act
		go func() {
			defer both.Done()
			<-start
			if err := service.reconcileTasks(time.Now()); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer both.Done()
			<-start
			if _, err := store.Queue(Action{ID: "request", Kind: "evaluate", TaskID: "task-1", Generation: "g1"}); err != nil {
				t.Error(err)
			}
		}()
		close(start)
		both.Wait()
		// Assert
		if actions := store.Snapshot().Actions; len(actions) > 1 && actions[0].ID == "request" {
			t.Fatalf("round %d: the reconcile queued %s behind the pending request", round, actions[1].ID)
		}
	}
}

// Reading a goblin's worktree must never rewrite its index: git status
// refreshes a stale index under an optional lock, which can fail a git
// command the goblin runs at the same moment.
func TestBoardGitReadsNeverRewriteAWorktreeIndex(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-qm", "a")
	// Same content, new timestamp: the index's stat data is now stale.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(dir, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := (Git{}).Clean(t.Context(), dir)
	if err != nil || !clean {
		t.Fatalf("Clean = %v, %v; want a clean tree", clean, err)
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("reading the worktree rewrote its index")
	}
}

func TestStatusActivityKeepsOnlyHttpsPullRequests(t *testing.T) {
	for _, c := range []struct{ line, pr string }{
		{"done: PR https://github.com/o/r/pull/9", "https://github.com/o/r/pull/9"},
		{"done: PR https://github.com/o/r/pull/9 (merged later)", "https://github.com/o/r/pull/9"},
		{"done: PR javascript:alert(1)", ""},
		{"done: PR http://github.com/o/r/pull/9", ""},
	} {
		activity, pr := statusActivity([]string{c.line}, time.Time{})
		if pr != c.pr || activity != c.line {
			t.Errorf("statusActivity(%q) = %q, %q; want pr %q", c.line, activity, pr, c.pr)
		}
	}
}

// The board alerts on a goblin's own failure or finish, so the snapshot says
// which kind of report a task last made.
func TestReportKindNamesTheKindOfAGoblinsLatestReport(t *testing.T) {
	for _, c := range []struct{ report, kind string }{
		{"working: Build review panel", "working"},
		{"blocked: Which colour? options: green | blue", "blocked"},
		{"failed: The build broke", "failed"},
		{"done: PR https://github.com/o/r/pull/9", "done"},
		{"waiting on overlord: sign-off", "waiting"},
		{"failedover to plan b", ""},
		{"", ""},
	} {
		if kind := reportKind(c.report); kind != c.kind {
			t.Errorf("reportKind(%q) = %q, want %q", c.report, kind, c.kind)
		}
	}
}

// Each refresh ran up to five git processes per checkout, every one scanned
// by Defender: about 33 git spawns a minute on the live home on 2026-09-27.
// A checkout is read again only when a fetch or a remote change rewrites its
// git files; otherwise the scan uses what it read last.
func TestGitMergedPRsReadsACheckoutAgainOnlyWhenItsRefsMove(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "code-goblins")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	merge := func(number int) {
		t.Helper()
		branch := fmt.Sprintf("fix/change-%d", number)
		git("switch", "-q", "-c", branch)
		git("commit", "-q", "--allow-empty", "-m", branch)
		git("switch", "-q", "main")
		git("merge", "-q", "--no-ff", branch, "-m", fmt.Sprintf("Merge pull request #%d from o/%s", number, branch))
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "--initial-branch=main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	git("remote", "add", "origin", "https://github.com/o/code-goblins.git")
	git("commit", "-q", "--allow-empty", "-m", "base")
	merge(1)
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	numbers := func(merged []MergedPR, err error) []string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		var listed []string
		for _, pr := range merged {
			listed = append(listed, strings.TrimPrefix(pr.PR, "https://github.com/o/code-goblins/pull/"))
		}
		slices.Sort(listed)
		return listed
	}
	list := GitMergedPRs([]string{repo})
	since := time.Now().Add(-time.Hour)
	if first := numbers(list(t.Context(), since)); !slices.Equal(first, []string{"1"}) {
		t.Fatalf("first scan lists %v, want PR 1", first)
	}

	// Move origin's main to a new merge, then put the ref file's time back:
	// to the scan nothing a fetch writes has changed.
	ref := filepath.Join(repo, ".git", "refs", "remotes", "origin", "main")
	before, err := os.Stat(ref)
	if err != nil {
		t.Fatal(err)
	}
	merge(2)
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	if err := os.Chtimes(ref, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	held := numbers(list(t.Context(), since))
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(ref, later, later); err != nil {
		t.Fatal(err)
	}
	moved := numbers(list(t.Context(), since))

	if !slices.Equal(held, []string{"1"}) {
		t.Fatalf("with its git files unchanged the scan lists %v, want what it read before (PR 1): it ran git again", held)
	}
	if !slices.Equal(moved, []string{"1", "2"}) {
		t.Fatalf("after origin's main moved the scan lists %v, want PRs 1 and 2", moved)
	}
}

func TestGitMergedPRsReadsMergeCommitsOfEachFleetRepository(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "code-goblins")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE=2026-09-23T16:00:00Z", "GIT_AUTHOR_DATE=2026-09-23T16:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run := func(args ...string) { t.Helper(); git(repo, args...) }
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "--initial-branch=main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	run("remote", "add", "origin", "https://github.com/o/code-goblins.git")
	run("commit", "-q", "--allow-empty", "-m", "base")
	run("switch", "-q", "-c", "fix/wake")
	run("commit", "-q", "--allow-empty", "-m", "the fix")
	run("switch", "-q", "main")
	run("merge", "-q", "--no-ff", "fix/wake", "-m", "Merge pull request #31 from o/fix/wake")
	run("commit", "-q", "--allow-empty", "-m", "Merge pull request #99 from o/not-a-merge")
	run("update-ref", "refs/remotes/origin/main", "HEAD")
	// A checkout with no GitHub remote has nothing to link and is skipped.
	other := filepath.Join(dir, "local-only")
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A master checkout that never fetched has no default branch to read,
	// which is not an error and does not hide the other repositories.
	legacy := filepath.Join(dir, "legacy")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	git(legacy, "init", "-q", "--initial-branch=master")
	git(legacy, "config", "user.email", "t@t")
	git(legacy, "config", "user.name", "t")
	git(legacy, "remote", "add", "origin", "https://github.com/o/legacy.git")
	git(legacy, "commit", "-q", "--allow-empty", "-m", "base")

	repos := FleetRepos(home.Home{Root: repo}, dir)
	if len(repos) != 3 {
		t.Fatalf("repos = %v, want the home once and the other two checkouts", repos)
	}
	merged, err := GitMergedPRs(repos)(t.Context(), time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 1 || merged[0].PR != "https://github.com/o/code-goblins/pull/31" || merged[0].Branch != "fix/wake" || merged[0].At != time.Date(2026, 9, 23, 16, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("merged = %+v, want only the merge commit of PR 31", merged)
	}
	if later, err := GitMergedPRs(repos)(t.Context(), time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)); err != nil || len(later) != 0 {
		t.Fatalf("merges before the window = %+v, %v; want none", later, err)
	}
}

// Cleanup leaves a task's status log in place, and a respawned id appends to
// it, so the new generation must not show the old one's pull request.
func TestSnapshotReadsOnlyTheStatusLinesOfTheTasksOwnGeneration(t *testing.T) {
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	spawned := time.Date(2026, 9, 21, 12, 0, 0, 500_000_000, time.UTC)
	meta.SpawnGen = fmt.Sprintf("s%d", spawned.UnixNano())
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(h.State, "task-1.status")
	if err := os.WriteFile(log, []byte("done: PR https://github.com/o/r/pull/9\n2026-09-20T00:00:00Z done: PR https://github.com/o/r/pull/10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store}
	view, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Tasks[0]; got.Activity != "" || got.PR != "" {
		t.Fatalf("respawned task = activity %q pr %q, want nothing from the earlier generation", got.Activity, got.PR)
	}
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("2026-09-21T12:00:00Z working: fresh start\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if view, err = service.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if got := view.Tasks[0]; got.Activity != "working: fresh start" || got.PR != "" {
		t.Fatalf("respawned task = activity %q pr %q, want its own first line and no PR", got.Activity, got.PR)
	}
}

// The merge scan reads every fleet repository with git, which took 47 to 50 s
// of each minute on 2026-09-27 while it ran on the supervisor's only loop:
// native events, the heartbeat and every snapshot waited behind it, and the
// board read the supervisor as unhealthy. The loop must never wait for it.
func TestTheSupervisorLoopDoesNotWaitForTheMergeScan(t *testing.T) {
	_, h := testStore(t)
	scanning, release := make(chan struct{}, 1), make(chan struct{})
	s, err := Start(context.Background(), h, Options{MergedPRs: func(ctx context.Context, _ time.Time) ([]MergedPR, error) {
		select {
		case scanning <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return []MergedPR{{PR: "https://github.com/o/r/pull/7", Branch: "fix/late", Project: "r", At: time.Now().Unix()}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	within := func(what string, done func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !done(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal(what)
			}
		}
	}
	snapshot := func() Snapshot {
		t.Helper()
		view, err := s.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		return view
	}

	select {
	case <-scanning:
	case <-time.After(5 * time.Second):
		t.Fatal("the merge scan never started")
	}

	within("the loop waited for the merge scan: no recovery cycle finished while it ran", func() bool { return !snapshot().Reconciled.IsZero() })
	close(release)
	within("the merge the scan found never reached the board", func() bool {
		return slices.ContainsFunc(snapshot().Tasks, func(task Task) bool { return task.ID == "merged:https://github.com/o/r/pull/7" })
	})
}

// Completed is rebuilt when a task finishes or its gate sees it merge, not
// only on the timer.
func TestHistoryIsRebuiltWhenATaskFinishesOrMerges(t *testing.T) {
	store, h := testStore(t)
	scans := make(chan struct{}, 8)
	s := &Service{Store: store, subscribers: map[chan struct{}]struct{}{}, Options: Options{MergedPRs: func(context.Context, time.Time) ([]MergedPR, error) {
		scans <- struct{}{}
		return nil, nil
	}}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go s.keepHistory(ctx, time.Hour, 20*time.Millisecond)
	scanned := func(why string) {
		t.Helper()
		select {
		case <-scans:
		case <-time.After(5 * time.Second):
			t.Fatal(why)
		}
	}
	quiet := func(why string) {
		t.Helper()
		select {
		case <-scans:
			t.Fatal(why)
		case <-time.After(200 * time.Millisecond):
		}
	}

	scanned("no rebuild at start")
	quiet("rebuilt again with nothing changed and the hour not up")
	if _, err := store.Queue(Action{ID: "evaluate-task-1", Kind: "evaluate", TaskID: "task-1", Generation: "g1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(t.Context(), func(context.Context, Action) (Evaluation, error) {
		return Evaluation{Phase: "merged", Reason: "PR merged"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	scanned("no rebuild after the gate saw task-1 merge")
	if err := os.Remove(filepath.Join(h.State, "task-1.meta")); err != nil {
		t.Fatal(err)
	}
	scanned("no rebuild after task-1 was cleaned up")
	quiet("rebuilt again with nothing changed since")
}

func TestHistoryKeepsHealthyMergesWhenARepositoryFails(t *testing.T) {
	store, _ := testStore(t)
	failure := errors.New("git log failed in one repository")
	service := &Service{Store: store, Options: Options{MergedPRs: func(context.Context, time.Time) ([]MergedPR, error) {
		return []MergedPR{{PR: "https://github.com/o/r/pull/32", Branch: "fix/b", Project: "r", At: time.Now().Unix()}}, failure
	}}}
	if err := service.refreshHistory(t.Context(), time.Now().UTC()); !errors.Is(err, failure) {
		t.Fatalf("refreshHistory error = %v, want the failing repository reported", err)
	}
	view, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(view.Tasks, func(task Task) bool { return task.ID == "merged:https://github.com/o/r/pull/32" }) {
		t.Fatalf("tasks = %+v, want the healthy repository's merge", view.Tasks)
	}
}

// The Completed column shows the newest twenty entries, but a finished task
// finds its merge among every merge in the window: pd-cost-cuts-resume read
// Finished while its merged PR sat behind 38 newer fleet merges.
func TestAFinishedTaskFindsItsMergeBehindTwentyNewerMerges(t *testing.T) {
	store, h := testStore(t)
	repo := filepath.Join(t.TempDir(), "PrecisionDocs-AI")
	now := time.Now().UTC()
	var stream strings.Builder
	committer := func(minute int) string {
		return fmt.Sprintf("committer t <t@t> %d +0000\n", now.Add(time.Duration(minute-60)*time.Minute).Unix())
	}
	fmt.Fprintf(&stream, "commit refs/heads/side\nmark :1\n%sdata 4\nside\n", committer(0))
	fmt.Fprintf(&stream, "commit refs/heads/main\n%sdata 4\nbase\n", committer(0))
	for pr := 1; pr <= 21; pr++ {
		subject := fmt.Sprintf("Merge pull request #%d from o/fix/pr-%d", pr, pr)
		fmt.Fprintf(&stream, "commit refs/heads/main\n%sdata %d\n%s\nmerge :1\n", committer(pr), len(subject), subject)
	}
	git := func(stdin string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git("", "init", "-q", "--initial-branch=main")
	git(stream.String(), "fast-import", "--quiet")
	git("", "remote", "add", "origin", "https://github.com/o/PrecisionDocs-AI.git")
	git("", "update-ref", "refs/remotes/origin/main", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(h.State, "pd-cost-cuts-resume.status"), []byte("done: PR https://github.com/o/PrecisionDocs-AI/pull/1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store, Options: Options{MergedPRs: GitMergedPRs([]string{repo})}}

	if err := service.refreshHistory(t.Context(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	view, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	index := slices.IndexFunc(view.Tasks, func(task Task) bool { return task.ID == "finished:pd-cost-cuts-resume" })
	if index < 0 {
		t.Fatal("Completed has no card for the finished task")
	}
	if !view.Tasks[index].Merged {
		t.Fatalf("finished task = %+v, want it to carry its merge", view.Tasks[index])
	}
	if completed := slices.DeleteFunc(slices.Clone(view.Tasks), func(task Task) bool { return !task.Archived }); len(completed) != historyLimit {
		t.Fatalf("Completed lists %d entries, want the newest %d", len(completed), historyLimit)
	}
}

// A squash merge leaves no merge commit and a closed pull request leaves
// nothing in git, so a finished task's pull request no fleet history shows
// merged is asked about on GitHub; a final answer is not asked for again.
func TestAFinishedTaskReadsItsPullRequestStateFromGitHub(t *testing.T) {
	store, h := testStore(t)
	for id, pr := range map[string]string{
		"squashed":  "https://github.com/o/r/pull/1",
		"dropped":   "https://github.com/o/r/pull/2",
		"open":      "https://github.com/o/r/pull/3",
		"landed":    "https://github.com/o/r/pull/4",
		"elsewhere": "https://gitlab.com/o/r/-/merge_requests/5",
	} {
		if err := os.WriteFile(filepath.Join(h.State, id+".status"), []byte("done: PR "+pr+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	answers := map[string]string{"https://github.com/o/r/pull/1": "MERGED", "https://github.com/o/r/pull/2": "CLOSED", "https://github.com/o/r/pull/3": "OPEN", "https://github.com/o/r/pull/4": "MERGED"}
	var asked []string
	service := &Service{Store: store, Options: Options{
		MergedPRs: func(context.Context, time.Time) ([]MergedPR, error) {
			return []MergedPR{{PR: "https://github.com/o/r/pull/4", Branch: "fix/landed", Project: "r", At: time.Now().Unix()}}, nil
		},
		PullRequestState: func(_ context.Context, url string) (PullRequestInfo, error) {
			asked = append(asked, url)
			return PullRequestInfo{State: answers[url], Title: "The PR title"}, nil
		},
	}}
	now := time.Now().UTC()
	refresh := func(at time.Time) []string {
		t.Helper()
		asked = nil
		if err := service.refreshHistory(t.Context(), at); err != nil {
			t.Fatal(err)
		}
		slices.Sort(asked)
		return asked
	}

	first := refresh(now)
	view, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"https://github.com/o/r/pull/1", "https://github.com/o/r/pull/2", "https://github.com/o/r/pull/3", "https://github.com/o/r/pull/4"}; !slices.Equal(first, want) {
		t.Fatalf("asked GitHub about %v, want only the GitHub pull requests no merge commit shows: %v", first, want)
	}
	for id, want := range map[string][2]bool{"squashed": {true, false}, "dropped": {false, true}, "open": {false, false}, "landed": {true, false}, "elsewhere": {false, false}} {
		index := slices.IndexFunc(view.Tasks, func(task Task) bool { return task.ID == "finished:"+id })
		if index < 0 || view.Tasks[index].Merged != want[0] || view.Tasks[index].Closed != want[1] {
			t.Fatalf("finished:%s in %+v, want merged %v and closed %v", id, view.Tasks, want[0], want[1])
		}
		if id != "elsewhere" && (view.Tasks[index].Title != "The PR title" || view.Tasks[index].Project != "r") {
			t.Fatalf("inconsistent Completed card: %+v", view.Tasks[index])
		}
	}
	if again := refresh(now.Add(time.Minute)); len(again) != 0 {
		t.Fatalf("a minute later asked about %v, want nothing", again)
	}
	if later := refresh(now.Add(pullRequestRecheck)); !slices.Equal(later, []string{"https://github.com/o/r/pull/3"}) {
		t.Fatalf("after the recheck interval asked about %v, want only the open pull request", later)
	}
}

func TestAPullRequestGitHubCouldNotReadStaysFinishedAndIsReported(t *testing.T) {
	store, h := testStore(t)
	if err := os.WriteFile(filepath.Join(h.State, "unread.status"), []byte("done: PR https://github.com/o/r/pull/7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("gh is not signed in")
	asks := 0
	service := &Service{Store: store, Options: Options{PullRequestState: func(context.Context, string) (PullRequestInfo, error) {
		asks++
		return PullRequestInfo{}, failure
	}}}
	now := time.Now().UTC()

	err := service.refreshHistory(t.Context(), now)
	view, snapshotErr := service.Snapshot()
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}

	if !errors.Is(err, failure) {
		t.Fatalf("refreshHistory error = %v, want GitHub's failure reported", err)
	}
	index := slices.IndexFunc(view.Tasks, func(task Task) bool { return task.ID == "finished:unread" })
	if index < 0 || view.Tasks[index].Merged || view.Tasks[index].Closed {
		t.Fatalf("tasks = %+v, want the unread pull request to read Finished", view.Tasks)
	}
	if err := service.refreshHistory(t.Context(), now.Add(time.Minute)); err != nil || asks != 1 {
		t.Fatalf("a minute later: error %v after %d asks, want no new ask", err, asks)
	}
	if err := service.refreshHistory(t.Context(), now.Add(pullRequestRecheck)); !errors.Is(err, failure) || asks != 2 {
		t.Fatalf("after the recheck interval: error %v after %d asks, want one more ask", err, asks)
	}
}

// All asks of one refresh share pullRequestBudget, so a GitHub that does not
// answer holds the supervisor's loop only that long; what was not asked is
// asked on the next refresh.
func TestASlowGitHubHoldsARefreshOnlyForTheBudget(t *testing.T) {
	store, h := testStore(t)
	for pr := 1; pr <= 3; pr++ {
		if err := os.WriteFile(filepath.Join(h.State, fmt.Sprintf("slow-%d.status", pr)), []byte(fmt.Sprintf("done: PR https://github.com/o/r/pull/%d\n", pr)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var asked []string
	blocking := true
	service := &Service{Store: store, Options: Options{PullRequestState: func(ctx context.Context, url string) (PullRequestInfo, error) {
		asked = append(asked, url)
		if blocking {
			blocking = false
			<-ctx.Done()
			return PullRequestInfo{}, ctx.Err()
		}
		return PullRequestInfo{State: "OPEN", Title: "Task title"}, nil
	}}}
	now := time.Now().UTC()

	started := time.Now()
	err := service.refreshHistory(t.Context(), now)
	elapsed := time.Since(started)

	if elapsed > pullRequestBudget+3*time.Second {
		t.Fatalf("the refresh took %v, want it to end soon after the %v budget", elapsed, pullRequestBudget)
	}
	if !errors.Is(err, context.DeadlineExceeded) || len(asked) != 1 {
		t.Fatalf("first refresh: error %v after asking %v, want one ask cut off by the budget and reported", err, asked)
	}
	blocked := asked[0]
	asked = nil
	if err := service.refreshHistory(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 || slices.Contains(asked, blocked) {
		t.Fatalf("next refresh asked %v, want the two pull requests the budget left and not %s", asked, blocked)
	}
}

type ghRunner struct {
	result  execx.Result
	err     error
	request execx.Request
}

func (r *ghRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.request = req
	return r.result, r.err
}

func TestGitHubPullRequestStateAcceptsOnlyGitHubsThreeStates(t *testing.T) {
	const url = "https://github.com/o/r/pull/9"
	for _, c := range []struct {
		name   string
		result execx.Result
		err    error
		want   string
	}{
		{"merged", execx.Result{Stdout: []byte(`{"state":"MERGED","title":"Task title"}`)}, nil, "MERGED"},
		{"closed", execx.Result{Stdout: []byte(`{"state":"CLOSED","title":"Task title"}`)}, nil, "CLOSED"},
		{"open", execx.Result{Stdout: []byte(`{"state":"OPEN","title":"Task title"}`)}, nil, "OPEN"},
		{"unknown answer", execx.Result{Stdout: []byte(`{"state":"DRAFT","title":"Task title"}`)}, nil, ""},
		{"empty answer", execx.Result{}, nil, ""},
		{"gh refused", execx.Result{ExitCode: 1, Stderr: []byte("no pull requests found")}, nil, ""},
		{"gh missing", execx.Result{}, errors.New("executable file not found"), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			runner := &ghRunner{result: c.result, err: c.err}

			got, err := GitHubPullRequestState(runner)(t.Context(), url)

			if got.State != c.want || (err == nil) != (c.want != "") {
				t.Fatalf("state = %q, %v; want %q", got, err, c.want)
			}
			if want := []string{"pr", "view", url, "--json", "state,title"}; runner.request.Name != "gh" || !slices.Equal(runner.request.Args, want) {
				t.Fatalf("ran %s %v, want gh %v", runner.request.Name, runner.request.Args, want)
			}
		})
	}
}

func TestSnapshotDropsAMergeOnlyWhenTheLiveTaskAlreadyShowsIt(t *testing.T) {
	for _, c := range []struct {
		phase    string
		isListed bool
	}{
		{"merged", false},
		{"done", false},
		{"ready", true},
		{"blocked", true},
	} {
		t.Run(c.phase, func(t *testing.T) {
			store, h := testStore(t)
			store.db.Tasks["task-1"] = Evaluation{Phase: c.phase, Generation: "g1", At: time.Now()}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			if err := state.AppendStatus(h.State, "task-1", "done: PR https://github.com/o/r/pull/31"); err != nil {
				t.Fatal(err)
			}
			service := &Service{Store: store, history: withMergedPRs(nil, []MergedPR{
				{PR: "https://github.com/o/r/pull/31", Branch: "fix/a", Project: "r", At: time.Now().Unix()},
			})}
			view, err := service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if got := view.Tasks[0]; got.ID != "task-1" || got.Phase != c.phase || got.Merged {
				t.Fatalf("live task = %s phase %q merged %v, want its own phase %q untouched", got.ID, got.Phase, got.Merged, c.phase)
			}
			isListed := slices.ContainsFunc(view.Tasks, func(task Task) bool { return task.ID == "merged:https://github.com/o/r/pull/31" })
			if isListed != c.isListed {
				t.Fatalf("merged card listed = %v, want %v for a live task in phase %q", isListed, c.isListed, c.phase)
			}
		})
	}
}

func TestCompletedLiveTaskUsesItsPullRequestTitleAndRepository(t *testing.T) {
	store, h := testStore(t)
	store.mu.Lock()
	store.db.Tasks["task-1"] = Evaluation{Phase: "done", Generation: "g1", PR: "https://github.com/owner/repository/pull/7", At: time.Now()}
	err := store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store, Options: Options{PullRequestState: func(context.Context, string) (PullRequestInfo, error) {
		return PullRequestInfo{State: "MERGED", Title: "Make task completion consistent"}, nil
	}}}
	if err := service.refreshHistory(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	view, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Tasks) != 1 || view.Tasks[0].Title != "Make task completion consistent" || view.Tasks[0].Project != "repository" || !view.Tasks[0].Merged {
		t.Fatalf("completed card metadata=%+v", view.Tasks)
	}
	before := service.historyMark()
	if err := state.WriteOutcome(h.State, state.Outcome{ID: "queued-stop", Phase: "stopped", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if before == service.historyMark() {
		t.Fatal("stopping an undispatched task did not invalidate Completed")
	}
}
