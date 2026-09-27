package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

	finished := finishedTasks(stateDir, now)
	got := map[string]string{}
	for _, task := range finished {
		if !task.Archived || task.Phase != "done" {
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

func TestMergedPullRequestsJoinTheTaskThatReportedThem(t *testing.T) {
	at := time.Date(2026, 9, 23, 16, 0, 0, 0, time.UTC)
	history := withMergedPRs([]Task{{ID: "finished:a", Title: "a", Archived: true, Evaluation: Evaluation{Phase: "done", PR: "https://example/pr/1", At: at}}}, []MergedPR{
		{PR: "https://example/pr/1", Branch: "fix/a", Project: `C:\dev\code-goblins`, At: at.Unix()},
		{PR: "https://example/pr/2", Branch: "fix/b", Project: `C:\dev\code-goblins`, At: at.Add(time.Hour).Unix()},
	})
	if len(history) != 2 || history[0].ID != "merged:https://example/pr/2" || history[0].Title != "fix/b" || history[0].Project != "code-goblins" || !history[0].Merged {
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
	queued := queuedBriefs(h)
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
	answers := map[string]string{"https://github.com/o/r/pull/1": "MERGED", "https://github.com/o/r/pull/2": "CLOSED", "https://github.com/o/r/pull/3": "OPEN"}
	var asked []string
	service := &Service{Store: store, Options: Options{
		MergedPRs: func(context.Context, time.Time) ([]MergedPR, error) {
			return []MergedPR{{PR: "https://github.com/o/r/pull/4", Branch: "fix/landed", Project: "r", At: time.Now().Unix()}}, nil
		},
		PullRequestState: func(_ context.Context, url string) (string, error) {
			asked = append(asked, url)
			return answers[url], nil
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

	if want := []string{"https://github.com/o/r/pull/1", "https://github.com/o/r/pull/2", "https://github.com/o/r/pull/3"}; !slices.Equal(first, want) {
		t.Fatalf("asked GitHub about %v, want only the GitHub pull requests no merge commit shows: %v", first, want)
	}
	for id, want := range map[string][2]bool{"squashed": {true, false}, "dropped": {false, true}, "open": {false, false}, "landed": {true, false}, "elsewhere": {false, false}} {
		index := slices.IndexFunc(view.Tasks, func(task Task) bool { return task.ID == "finished:"+id })
		if index < 0 || view.Tasks[index].Merged != want[0] || view.Tasks[index].Closed != want[1] {
			t.Fatalf("finished:%s in %+v, want merged %v and closed %v", id, view.Tasks, want[0], want[1])
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
	service := &Service{Store: store, Options: Options{PullRequestState: func(context.Context, string) (string, error) {
		asks++
		return "", failure
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
		{"merged", execx.Result{Stdout: []byte("MERGED\n")}, nil, "MERGED"},
		{"closed", execx.Result{Stdout: []byte("CLOSED\n")}, nil, "CLOSED"},
		{"open", execx.Result{Stdout: []byte("OPEN\n")}, nil, "OPEN"},
		{"unknown answer", execx.Result{Stdout: []byte("DRAFT\n")}, nil, ""},
		{"empty answer", execx.Result{}, nil, ""},
		{"gh refused", execx.Result{ExitCode: 1, Stderr: []byte("no pull requests found")}, nil, ""},
		{"gh missing", execx.Result{}, errors.New("executable file not found"), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			runner := &ghRunner{result: c.result, err: c.err}

			got, err := GitHubPullRequestState(runner)(t.Context(), url)

			if got != c.want || (err == nil) != (c.want != "") {
				t.Fatalf("state = %q, %v; want %q", got, err, c.want)
			}
			if want := []string{"pr", "view", url, "--json", "state", "--jq", ".state"}; runner.request.Name != "gh" || !slices.Equal(runner.request.Args, want) {
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
