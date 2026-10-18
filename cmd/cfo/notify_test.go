package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestNotifyBlockedWritesStatusAndWakesTheCFO(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", "Should I merge this?"}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}

	stateDir := filepath.Join(dir, "state")
	lines, err := state.TailStatus(stateDir, "g1", 1)
	if err != nil || len(lines) != 1 {
		t.Fatalf("status = %v, %v; want one blocked line", lines, err)
	}
	if _, event := state.SplitStatus(lines[0]); event != "blocked: Should I merge this?" {
		t.Fatalf("status = %v, %v; want one blocked line", lines, err)
	}

	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "notify" || records[0].Key != "g1" || records[0].Detail != "blocked: Should I merge this?" {
		t.Fatalf("wake records = %+v, want one notify carrying the question", records)
	}

	ep, err := wake.ReadEpisode(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !ep.Pending || ep.Gen != 1 {
		t.Errorf("episode = %+v, want pending:1 so the CFO is actually woken", ep)
	}
}

func TestNotifyDoneRequiresPR(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--done"}, &stdout, &stderr, defaultCommandRuntime()); exit != 2 || !strings.Contains(stderr.String(), "--pr") {
		t.Fatalf("exit=%d stderr=%q, want --pr refusal", exit, stderr.String())
	}
}

type notifyHeadRemote struct {
	t      *testing.T
	remote string
	url    string
}

func (r notifyHeadRemote) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	r.t.Helper()
	if request.Name != "gh" || !slices.Equal(request.Args, []string{"pr", "view", r.url, "--json", "headRefOid", "--jq", ".headRefOid"}) {
		r.t.Fatalf("unexpected PR head lookup: %+v", request)
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		r.t.Error("PR head lookup has no deadline")
	}
	return execx.OSRunner{}.Run(ctx, execx.Request{Name: "git", Args: []string{"-C", r.remote, "rev-parse", "refs/heads/second-pr"}})
}

// On 2026-10-07 cg-fleet-tree reported PR 416 done, and again when the CFO
// asked it to wrap up, and cg-ci-green did the same with PR 421: two wakes
// for one finished head each. A done wakes the CFO once per pull request
// head, even after the CFO acknowledged it; a new head is a new report.
func TestNotifyDoneWakesOncePerPullRequestHead(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	worktree := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", worktree, "-c", "user.name=t", "-c", "user.email=t@example.test"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "first")
	remote := t.TempDir()
	gitIn(t, remote, "init", "-q", "--bare")
	git("remote", "add", "origin", remote)
	otherWorktree := filepath.Join(t.TempDir(), "second-pr")
	git("worktree", "add", "-q", "-b", "second-pr", otherWorktree)
	gitIn(t, otherWorktree, "-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "-q", "--allow-empty", "-m", "second PR")
	gitIn(t, otherWorktree, "push", "-q", "origin", "second-pr")
	if err := state.WriteTaskMeta(filepath.Join(dir, "state"), state.TaskMeta{ID: "g1", SpawnGen: "s1", Worktree: worktree}); err != nil {
		t.Fatal(err)
	}
	const pr = "https://github.com/o/r/pull/416"
	runtime := defaultCommandRuntime()
	runtime.github = notifyHeadRemote{t: t, remote: remote, url: pr}
	notifyDone := func(link string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if exit := runNotify([]string{"g1", "--done", "--pr", link}, &stdout, &stderr, runtime); exit != 0 {
			t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
		}
		return stdout.String()
	}
	doneWakes := func() int {
		t.Helper()
		records, err := wake.Pending(filepath.Join(dir, "state"))
		if err != nil {
			t.Fatal(err)
		}
		return len(records)
	}

	// Act and Assert
	notifyDone(pr)
	if again := notifyDone(pr + " (merged, ready to retire)"); doneWakes() != 1 || !strings.Contains(again, "not woken again") {
		t.Fatalf("a second done for the same head left %d wakes and said %q, want one wake and a word that the CFO was not woken again", doneWakes(), again)
	}
	records, _ := wake.Pending(filepath.Join(dir, "state"))
	if err := wake.AckThrough(filepath.Join(dir, "state"), records[len(records)-1].Seq); err != nil {
		t.Fatal(err)
	}
	notifyDone(pr)
	if doneWakes() != 0 {
		t.Fatalf("a done for a head the CFO already acknowledged woke it again: %d wakes", doneWakes())
	}
	gitIn(t, otherWorktree, "-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "-q", "--allow-empty", "-m", "update second PR")
	gitIn(t, otherWorktree, "push", "-q", "origin", "second-pr")
	notifyDone(pr)
	if doneWakes() != 1 {
		t.Fatalf("a done for a new head left %d wakes, want one", doneWakes())
	}
	git("commit", "-q", "--allow-empty", "-m", "unrelated task branch update")
	if again := notifyDone(pr); doneWakes() != 1 || !strings.Contains(again, "not woken again") {
		t.Fatalf("moving the task branch left %d wakes and said %q, want the unchanged PR head suppressed", doneWakes(), again)
	}
}

func TestNotifyDoneWakesWhenPullRequestHeadCannotBeRead(t *testing.T) {
	for name, runner := range map[string]proofRunner{
		"command error": {err: errors.New("gh unavailable")},
		"timeout":       {err: context.DeadlineExceeded},
		"failed lookup": {exit: 1, stdout: strings.Repeat("a", 40)},
		"empty head":    {stdout: "\n"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			if err := os.MkdirAll(h.State, 0o700); err != nil {
				t.Fatal(err)
			}
			worktree := t.TempDir()
			gitIn(t, worktree, "init", "-q")
			gitIn(t, worktree, "-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "-q", "--allow-empty", "-m", "task branch")
			if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "g1", Worktree: worktree}); err != nil {
				t.Fatal(err)
			}
			runtime := testCommandRuntimeForHome(h)
			runtime.github = runner
			var stdout, stderr bytes.Buffer

			// Act
			for range 2 {
				if exit := runNotify([]string{"g1", "--done", "--pr", "https://github.com/o/r/pull/416"}, &stdout, &stderr, runtime); exit != 0 {
					t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
				}
			}

			// Assert
			records, err := wake.Pending(h.State)
			if err != nil || len(records) != 2 {
				t.Fatalf("wakes = %+v (%v), want both reports delivered without a known PR head", records, err)
			}
			if !strings.Contains(stderr.String(), "without deduplication") {
				t.Errorf("stderr = %q, want the failed head lookup explained", stderr.String())
			}
		})
	}
}

func TestNotifyDoneDeduplicatesWithoutATaskWorktree(t *testing.T) {
	// Arrange
	h := testHome(t)
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := testCommandRuntimeForHome(h)
	runtime.github = proofRunner{stdout: strings.Repeat("a", 40) + "\n"}
	var stdout, stderr bytes.Buffer

	// Act
	for _, url := range []string{"https://github.com/o/r/pull/416", "https://github.com/o/r/pull/416", "https://github.com/o/r/pull/417"} {
		if exit := runNotify([]string{"g1", "--done", "--pr", url}, &stdout, &stderr, runtime); exit != 0 {
			t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
		}
	}

	// Assert
	records, err := wake.Pending(h.State)
	if err != nil || len(records) != 2 {
		t.Fatalf("wakes = %+v (%v), want one per PR even when their heads match", records, err)
	}
}

func TestNotifyRequiresExactlyOneOutcome(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1"}, &stdout, &stderr, defaultCommandRuntime()); exit != 2 || !strings.Contains(stderr.String(), "exactly one") {
		t.Fatalf("exit=%d stderr=%q, want exactly-one refusal", exit, stderr.String())
	}
}

func TestNotifyTargetsStateOverrideWithoutCFOHome(t *testing.T) {
	worktree := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CFO_HOME", "")
	t.Setenv("CFO_STATE_OVERRIDE", stateDir)
	// With no CFO_HOME the home is the per-user one, here the test's own.
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Chdir(worktree)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", "Should I merge this?"}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}

	lines, err := state.TailStatus(stateDir, "g1", 1)
	if err != nil || len(lines) != 1 {
		t.Fatalf("status = %v, %v; want one blocked line in the override dir", lines, err)
	}
	if _, event := state.SplitStatus(lines[0]); event != "blocked: Should I merge this?" {
		t.Fatalf("status = %v, %v; want one blocked line in the override dir", lines, err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("notify polluted the worktree with a state dir: %v", err)
	}
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "notify" {
		t.Fatalf("wake records = %+v, want one notify in the override dir", records)
	}
}

// TestNotifyTargetsStateOverrideWithAGlobalCFOHome covers what changes once
// CFO_HOME is set at user scope: a goblin pane inherits it, so a goblin that
// used to resolve its home to its own worktree now resolves it to the fleet
// root. The wake queue must still land in the state directory the spawn
// pinned, which is what CFO_STATE_OVERRIDE - not CFO_HOME - has always
// decided.
func TestNotifyTargetsStateOverrideWithAGlobalCFOHome(t *testing.T) {
	worktree := t.TempDir()
	fleetRoot := t.TempDir()
	stateDir := filepath.Join(fleetRoot, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", fleetRoot)
	t.Setenv("CFO_STATE_OVERRIDE", stateDir)
	t.Chdir(worktree)

	runtime := defaultCommandRuntime()
	runtime.github = proofRunner{stdout: strings.Repeat("a", 40)}
	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--done", "--pr", "https://example.test/pr/1"}, &stdout, &stderr, runtime); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}

	lines, err := state.TailStatus(stateDir, "g1", 1)
	if err != nil || len(lines) != 1 {
		t.Fatalf("status = %v, %v; want one done line in the fleet state dir", lines, err)
	}
	if _, event := state.SplitStatus(lines[0]); event != "done: PR https://example.test/pr/1" {
		t.Fatalf("status = %v, %v; want one done line in the fleet state dir", lines, err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("notify polluted the worktree with a state dir: %v", err)
	}
}

func TestNotifyNormalizesControlCharactersInTheDetail(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", "Should I\nmerge this?"}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}

	stateDir := filepath.Join(dir, "state")
	lines, err := state.TailStatus(stateDir, "g1", 5)
	if err != nil || len(lines) != 1 {
		t.Fatalf("status = %v, %v; want one normalized blocked line", lines, err)
	}
	if _, event := state.SplitStatus(lines[0]); event != "blocked: Should I merge this?" {
		t.Fatalf("status = %v, %v; want one normalized blocked line", lines, err)
	}
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Detail != "blocked: Should I merge this?" {
		t.Fatalf("wake records = %+v, want one normalized notify detail", records)
	}
}

// On 2026-09-23 the CFO took three goblins' blocked questions as lost while
// cfo serve ran the board. Each had reached the wake queue the second its
// notify ran; only the CFO's rewake was missing. A blocked notify is in the
// queue when cfo notify returns, and on serve's board, whether or not serve
// holds the home.
func TestBlockedNotifyIsQueuedAtOnceWithAndWithoutServe(t *testing.T) {
	for _, serving := range []bool{false, true} {
		t.Run(map[bool]string{false: "without serve", true: "with serve"}[serving], func(t *testing.T) {
			dir := t.TempDir()
			h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
			if err := os.Mkdir(h.State, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFO_HOME", dir)
			t.Setenv("CFO_STATE_OVERRIDE", "")
			var board *supervisor.Service
			if serving {
				s, err := supervisor.Start(context.Background(), h, supervisor.Options{Example: true})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(s.Close)
				board = s
			}

			var stdout, stderr bytes.Buffer
			start := time.Now()
			if exit := runNotify([]string{"g1", "--blocked", "Which store? options: Postgres | SQLite"}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
				t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
			}
			// stall-accepted: it times the call, and the waits it rules out, such as the wake queue's five-second lock, are too short for a longer bound to tell apart.
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Errorf("cfo notify took %v, want it back at once", elapsed)
			}

			want := "blocked: Which store? options: Postgres | SQLite"
			records, err := wake.Pending(h.State)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 || records[0].Kind != "notify" || records[0].Key != "g1" || records[0].Detail != want || records[0].Time.Before(start.Add(-time.Second)) {
				t.Fatalf("wake records = %+v, want the question queued by the time notify returned", records)
			}
			if board != nil {
				snapshot, err := board.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if !slices.ContainsFunc(snapshot.Decisions, func(r wake.Record) bool { return r.Seq == records[0].Seq && r.Detail == want }) {
					t.Fatalf("board decisions = %+v, want serve to show the queued question", snapshot.Decisions)
				}
			}
		})
	}
}

// The Overlord, 2026-10-02: "it does seem like alert sent, complete delays
// can be improved to avoid lagging notifications". A report told the running
// supervisor nothing, so its boards showed it at their next refresh, up to
// fifteen seconds later. Every report tells it, and it tells its boards at
// once.
func TestNotifyTellsTheRunningSupervisorWhichTellsItsBoardsAtOnce(t *testing.T) {
	for name, args := range map[string][]string{
		"at work": {"g1", "--working", "the lint step"},
		"failed":  {"g1", "--failed", "the build broke"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange: a supervisor that has gone quiet after starting, well
			// before its next refresh is due.
			dir := t.TempDir()
			h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
			if err := os.Mkdir(h.State, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFO_HOME", dir)
			t.Setenv("CFO_STATE_OVERRIDE", "")
			board, err := supervisor.Start(context.Background(), h, supervisor.Options{Example: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(board.Close)
			started := time.Now()
			before := board.Revision()
			// stall-accepted: nothing is meant to change while it waits, so a stall can end it early but cannot fail the test, and the check below skips a run a stall slowed.
			for quiet := time.Now(); time.Since(quiet) < 2*time.Second; time.Sleep(50 * time.Millisecond) {
				if now := board.Revision(); now != before {
					before, quiet = now, time.Now()
				}
			}
			// stall-accepted: running out skips the test rather than failing it.
			if since := time.Since(started); since > 8*time.Second {
				t.Skipf("the supervisor took %s to go quiet, too near its refresh to tell a report from it", since.Round(time.Second))
			}

			// Act
			var stdout, stderr bytes.Buffer
			if exit := runNotify(args, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
				t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
			}

			// Assert
			// stall-accepted: it times the report, and the wait it rules out is the board's own refresh, due within 15 s, which a bound of 30 s would let pass.
			for deadline := time.Now().Add(3 * time.Second); board.Revision() == before; time.Sleep(20 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the supervisor told its boards nothing within three seconds of the report")
				}
			}
		})
	}
}

// A choice is the answer itself, because the Overlord picks from a plain list
// of answers: a choice that is only a letter or number is refused before
// anything is recorded, and answers written as phrases are not.
func TestNotifyRefusesAChoiceThatIsOnlyALetterOrNumber(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	for _, question := range []string{
		"Merge now? options: a | b",
		"Merge now? options: a (Recommended) | b",
		"Merge now? options: Merge now (Recommended) | 2",
		"Merge now? options: (c) | Wait",
		"Merge now? options: B) | Wait",
		"Merge now? options: 12 | Wait",
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify([]string{"g1", "--blocked", question}, &stdout, &stderr, defaultCommandRuntime()); exit != 2 || !strings.Contains(stderr.String(), "write the answer itself") {
			t.Fatalf("%q: exit=%d stderr=%q, want it refused with how to write the answer", question, exit, stderr.String())
		}
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("a refused notify recorded status %q", lines)
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 0 {
		t.Fatalf("a refused notify woke the CFO: %+v %v", records, err)
	}
	question := "Merge now? options: Fix it next (Recommended) | Keep 300 s | A plan"
	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", question}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%q, want answers written as phrases recorded", exit, stderr.String())
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != "blocked: "+question {
		t.Fatalf("wake records = %+v %v, want the question", records, err)
	}
}

// A question with choices in a report that wakes nobody reached nobody: on
// 2026-10-15 a goblin's pull request sat 15 minutes on one written into a
// working note (#984). Such a report is refused and told to go as a blocked
// notify, which raises a decision; a report that asks nothing is kept.
func TestNotifyRefusesAQuestionInAReportThatWakesNobody(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	asked := [][]string{
		{"g1", "--working", "PR 74 needs her helper. Options: Pearl adds it and I push 74 after (Recommended) | I add it inside 74 myself, in her files."},
		{"g1", "--working", "Should I push now? Push now | Wait for CI"},
		{"g1", "--waiting-on", "ci", "PR 45 checks; merge right after? options: Merge after (Recommended) | Hold it"},
	}
	kept := []struct {
		args []string
		line string
	}{
		{[]string{"g1", "--working", "piping `go test ./... | tee log` into the gate"}, "working: piping `go test ./... | tee log` into the gate"},
		{[]string{"g1", "--working", "Choice taken: keep 300 s, the shorter wait loses runs"}, "working: Choice taken: keep 300 s, the shorter wait loses runs"},
	}

	for _, args := range asked {
		// Act
		var stdout, stderr bytes.Buffer
		exit := runNotify(args, &stdout, &stderr, defaultCommandRuntime())

		// Assert
		if exit != 2 || !strings.Contains(stderr.String(), "--blocked") {
			t.Errorf("%q: exit=%d stderr=%q, want it refused and sent to --blocked", args, exit, stderr.String())
		}
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("a refused report recorded status %q", lines)
	}
	for _, c := range kept {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
			t.Fatalf("%q: exit=%d stderr=%q, want a report that asks nothing kept", c.args, exit, stderr.String())
		}
		lines, err := state.TailStatus(stateDir, "g1", 1)
		if _, event := state.SplitStatus(lines[0]); err != nil || event != c.line {
			t.Fatalf("status = %q %v, want %q", lines, err, c.line)
		}
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 0 {
		t.Fatalf("wake records = %+v %v, want none", records, err)
	}
}

// questionHome is a scratch home for a goblin's questions, with the home,
// its state and the projects root all pointed at scratch paths, and returns
// its state directory.
func questionHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	t.Setenv("CFO_STATE_OVERRIDE", stateDir)
	t.Setenv("CFO_PROJECTS_ROOT", filepath.Join(dir, "projects"))
	return stateDir
}

// clemsQuestion is wake 1001093 of 2026-10-12: a goblin's question with its
// details on lines after the choices.
const clemsQuestion = "May I restart my capped poll of the PR 1564 deploy, so I merge PR 1565 right after it ends? options: Restart the capped poll (Recommended) | Message me when the deploy ends\n" +
	"- PR 1565 is green in CI and mergeable.\n" +
	"- Deploy to Production run 38280093670 for PR 1564 is mid-rollout, and a merge now would cancel it.\n" +
	"- **Claude Code stopped my background deploy poll because the machine ran low on memory**."

// On 2026-10-12 a goblin put its details on lines after its choices, the
// notify joined its lines, and every reader took the details as the end of
// the last choice: cfo drain showed them inside choice 2 and cfo answer
// refused "Message me when the deploy ends" as no choice. The notify reads
// the choices off their own line, so the details stay the question's and
// each choice is only its answer, a hyphen in its own words included.
func TestNotifyKeepsDetailsAfterTheChoicesOutOfThem(t *testing.T) {
	for name, c := range map[string]struct {
		question string
		details  []string
		options  []string
	}{
		"details after the choices": {
			clemsQuestion,
			[]string{"May I restart my capped poll", "PR 1565 is green in CI", "a merge now would cancel it", "ran low on memory"},
			[]string{"Restart the capped poll (Recommended)", "Message me when the deploy ends"},
		},
		"a choice with a hyphen in its own words": {
			"Which timeout should the gate keep? options: Re-run the gate (Recommended) | Keep 300 s - for now\n- The gate timed out twice at 300 s.",
			[]string{"Which timeout should the gate keep?", "The gate timed out twice at 300 s."},
			[]string{"Re-run the gate (Recommended)", "Keep 300 s - for now"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			stateDir := questionHome(t)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runNotify([]string{"g1", "--blocked", c.question}, &stdout, &stderr, defaultCommandRuntime())

			// Assert
			if exit != 0 {
				t.Fatalf("exit=%d stderr=%q, want the question recorded", exit, stderr.String())
			}
			records, err := wake.Pending(stateDir)
			if err != nil || len(records) != 1 {
				t.Fatalf("wake records = %+v %v, want the one question", records, err)
			}
			// cfo answer and the board read the choices this way.
			question, options, asked := wake.Question(records[0])
			if !asked || !slices.Equal(options, c.options) {
				t.Errorf("choices = %q, want %q", options, c.options)
			}
			for _, detail := range c.details {
				if !strings.Contains(question, detail) {
					t.Errorf("question = %q, want it to keep %q", question, detail)
				}
			}
			var drained bytes.Buffer
			if exit := runDrain(home.Home{State: stateDir}, nil, &drained, &stderr); exit != 0 {
				t.Fatalf("drain exit=%d stderr=%q", exit, stderr.String())
			}
			if want := "2) " + c.options[1] + "\n"; !strings.Contains(drained.String(), want) {
				t.Errorf("drain = %q, want choice %q on a line of its own", drained.String(), want)
			}
		})
	}
}

// A question that writes the options marker twice cannot be read one way
// once details may follow the choices: a detail that names the marker after
// them took over the choices, as on 2026-10-01 one before them nearly did.
// A marker with fewer than two choices after it on its line is a detail
// that names it or offers nothing to pick. The notify refuses each at once,
// recording nothing, and says how to ask.
func TestNotifyRefusesAQuestionWhoseChoicesCannotBeTold(t *testing.T) {
	for name, question := range map[string]string{
		"the marker in a detail after the choices":  "Should a second marker be refused? options: Refuse it (Recommended) | Take the last\n- Today the options: marker is matched once.",
		"the marker in a detail before the choices": "Should a second marker be refused?\n- Today the options: marker is matched once.\n- **Blocking:** the choice list. options: Refuse it (Recommended) | Take the last",
		"the marker in a detail with no choices":    "Why does gh refuse the flag?\n- It says options: --watch is unknown.",
		"no choice after the marker on its line":    "Should a second marker be refused? options:\n- Refuse it\n- Take the last",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			stateDir := questionHome(t)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runNotify([]string{"g1", "--blocked", question}, &stdout, &stderr, defaultCommandRuntime())

			// Assert
			if exit != 2 || !strings.Contains(stderr.String(), `every detail before "options:"`) {
				t.Errorf("exit=%d stderr=%q, want it refused with how to ask", exit, stderr.String())
			}
			if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
				t.Errorf("a refused notify recorded status %q", lines)
			}
			if records, err := wake.Pending(stateDir); err != nil || len(records) != 0 {
				t.Errorf("a refused notify woke the CFO: %+v %v", records, err)
			}
		})
	}
}

// On 2026-10-12 a goblin sent its question again with its details first, to
// correct the choices the first one showed, and the CFO got two decisions
// for one question. The same question, sent while the first still waits
// unanswered, is that one decision: the goblin is told, and the CFO is not
// woken again. Once answered it is a new ask, and another question from the
// same goblin is always its own decision.
func TestNotifyAQuestionSentTwiceIsOneDecision(t *testing.T) {
	// Arrange
	stateDir := questionHome(t)
	corrected := "May I restart my capped poll of the PR 1564 deploy, so I merge PR 1565 right after it ends?\n" +
		"- PR 1565 is green in CI and mergeable.\n" +
		"- Deploy to Production run 38280093670 for PR 1564 is mid-rollout, and a merge now would cancel it.\n" +
		"- **Claude Code stopped my background deploy poll because the machine ran low on memory**. options: Restart the capped poll (Recommended) | Message me when the deploy ends"
	notify := func(question string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if exit := runNotify([]string{"g1", "--blocked", question}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
			t.Fatalf("exit=%d stderr=%q, want the question taken", exit, stderr.String())
		}
		return stdout.String()
	}
	decisions := func() []wake.Record {
		t.Helper()
		records, err := wake.Pending(stateDir)
		if err != nil {
			t.Fatal(err)
		}
		return slices.DeleteFunc(records, func(record wake.Record) bool {
			_, isDecision := wake.BlockingNotify(record)
			return !isDecision || record.Answered != ""
		})
	}
	notify(clemsQuestion)

	// Act
	said := notify(corrected)

	// Assert
	first := decisions()
	if len(first) != 1 {
		t.Fatalf("decisions = %+v, want the one question", first)
	}
	if want := fmt.Sprintf("already asked this as notify %d", first[0].Seq); !strings.Contains(said, want) {
		t.Errorf("stdout = %q, want the goblin told %q", said, want)
	}
	if err := wake.MarkAnswered(stateDir, first[0].Seq, wake.AnsweredByCFO, "Restart the capped poll"); err != nil {
		t.Fatal(err)
	}
	notify(corrected)
	notify("Should I update PR 1565 to current main now? options: Update it now (Recommended) | Wait for the deploy")
	if got := decisions(); len(got) != 2 || got[0].Seq <= first[0].Seq {
		t.Errorf("decisions = %+v, want the answered question asked again and the other question, each its own", got)
	}
}

// Review images are checked before anything is recorded: a wrong count or a
// path outside the task fails the whole notify, so the CFO is never woken
// with a question whose images the Overlord cannot see.
func TestNotifyImagesAreCheckedBeforeAnythingIsRecorded(t *testing.T) {
	dir := t.TempDir()
	stateDir, worktree := filepath.Join(dir, "state"), filepath.Join(dir, "work")
	for _, d := range []string{stateDir, worktree} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CFO_HOME", dir)
	// No native terminal, so the Command Center step stops at the goblin's
	// proof.
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "g1", Project: worktree, Worktree: worktree, Harness: "codex", Mode: "no-mistakes", Kind: "ship", SpawnGen: "g1"}); err != nil {
		t.Fatal(err)
	}
	var shot bytes.Buffer
	if err := png.Encode(&shot, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	inside, outside := filepath.Join(worktree, "grid.png"), filepath.Join(t.TempDir(), "list.png")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, shot.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	question := "Which layout? options: Grid | List"
	for name, c := range map[string]struct {
		args []string
		exit int
	}{
		"a failed notify":   {[]string{"--failed", question, "--image", inside, "--image", inside}, 2},
		"no choices":        {[]string{"--blocked", "Which layout?", "--image", inside}, 2},
		"one image too few": {[]string{"--blocked", question, "--image", inside}, 2},
		"outside the task":  {[]string{"--blocked", question, "--image", inside, "--image", outside}, 1},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(append([]string{"g1"}, c.args...), &stdout, &stderr, defaultCommandRuntime()); exit != c.exit {
			t.Fatalf("%s: exit=%d stderr=%q, want %d", name, exit, stderr.String(), c.exit)
		}
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("a refused notify recorded status %q", lines)
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 0 {
		t.Fatalf("a refused notify woke the CFO: %+v %v", records, err)
	}
	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--blocked", question, "--image", inside, "--image", filepath.Join(dir, "work", ".", "grid.png")}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%q, want the notify recorded", exit, stderr.String())
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != "blocked: "+question {
		t.Fatalf("wake records = %+v %v, want the question", records, err)
	}
}

// Working, and waiting on another task, CI, a deploy or memory, are status for the
// board and wake nobody; waiting on the Overlord wakes the CFO like a
// question, and a wait needs a valid target and a reason.
func TestNotifyWorkingAndWaitingOnWakeOnlyForTheOverlord(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	for name, c := range map[string]struct {
		args []string
		exit int
	}{
		"waiting on itself":    {[]string{"g1", "--waiting-on", "g1", "on me"}, 2},
		"waiting on a bad ID":  {[]string{"g1", "--waiting-on", "not/a/task", "why"}, 2},
		"waiting without why":  {[]string{"g1", "--waiting-on", "ci"}, 2},
		"working and done":     {[]string{"g1", "--working", "lint", "--done", "--pr", "https://example.com/pr/1"}, 2},
		"working with a stray": {[]string{"g1", "--working", "lint", "extra"}, 2},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime()); exit != c.exit {
			t.Errorf("%s: exit=%d stderr=%q, want %d", name, exit, stderr.String(), c.exit)
		}
	}
	for _, c := range []struct {
		args []string
		line string
	}{
		{[]string{"g1", "--working", "fixing the lint step"}, "working: fixing the lint step"},
		{[]string{"g1", "--waiting-on", "ci", "PR 45 checks"}, "waiting on ci: PR 45 checks"},
		// memory_ready names a goblin by this very line.
		{[]string{"g1", "--waiting-on", "memory", "the build needs 4 GB"}, "waiting on memory: the build needs 4 GB"},
		{[]string{"g1", "--waiting-on", "board-ui", "its API contract"}, "waiting on board-ui: its API contract"},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
			t.Fatalf("%v: exit=%d stderr=%q", c.args, exit, stderr.String())
		}
		lines, err := state.TailStatus(stateDir, "g1", 1)
		if _, event := state.SplitStatus(lines[0]); err != nil || event != c.line {
			t.Fatalf("status = %q %v, want %q", lines, err, c.line)
		}
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 0 {
		t.Fatalf("wake records = %+v %v, want none for working or a wait on a task or CI", records, err)
	}
	var stdout, stderr bytes.Buffer
	if exit := runNotify([]string{"g1", "--waiting-on", "overlord", "log in to Stripe"}, &stdout, &stderr, defaultCommandRuntime()); exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != "waiting on overlord: log in to Stripe" {
		t.Fatalf("wake records = %+v %v, want the wait on the Overlord", records, err)
	}
}

// fakeLavish puts a stand-in lavish-axi alone on PATH that opens any page at
// a fixed address.
func fakeLavish(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "@echo off\r\necho session:\r\necho   url: \"http://127.0.0.1:4387/session/f26e\"\r\necho   status: opened\r\n"
	if err := os.WriteFile(filepath.Join(bin, "lavish-axi.cmd"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Join(os.Getenv("SystemRoot"), "System32"))
}

// A wait on the Overlord can name the Lavish page he answers on: the page is
// checked and opened before anything is recorded, and its link travels with
// the wait to the CFO.
func TestNotifyWaitNamesTheLavishPageTheOverlordAnswersOn(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	page := filepath.Join(dir, "plan.html")
	notes := filepath.Join(dir, "notes.txt")
	for _, path := range []string{page, notes} {
		if err := os.WriteFile(path, []byte("<html></html>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fakeLavish(t)
	for name, c := range map[string]struct {
		args []string
		exit int
	}{
		"a page with a question":  {[]string{"g1", "--blocked", "which plan?", "--lavish", page}, 2},
		"a page with a CI wait":   {[]string{"g1", "--waiting-on", "ci", "checks", "--lavish", page}, 2},
		"a page that is missing":  {[]string{"g1", "--waiting-on", "overlord", "pick a plan", "--lavish", filepath.Join(dir, "gone.html")}, 2},
		"a page that is not HTML": {[]string{"g1", "--waiting-on", "overlord", "pick a plan", "--lavish", notes}, 2},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime()); exit != c.exit {
			t.Errorf("%s: exit=%d stderr=%q, want %d", name, exit, stderr.String(), c.exit)
		}
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("status = %q after refused notifies, want nothing recorded", lines)
	}

	// g1 runs in no native terminal, so the wait's item cannot be published and
	// nothing would watch the page: the notify fails loudly, and the CFO still
	// has the wait.
	var stdout, stderr bytes.Buffer
	exit := runNotify([]string{"g1", "--waiting-on", "overlord", "pick a plan", "--lavish", page}, &stdout, &stderr, defaultCommandRuntime())

	if exit != 1 || !strings.Contains(stderr.String(), "nothing watches the page "+page) || !strings.Contains(stderr.String(), "ask in text with --blocked") {
		t.Fatalf("exit=%d stderr=%q, want a failure naming the unwatched page and saying to ask in text", exit, stderr.String())
	}
	want := "waiting on overlord: pick a plan (page http://127.0.0.1:4387/session/f26e)"
	lines, err := state.TailStatus(stateDir, "g1", 1)
	if _, event := state.SplitStatus(lines[0]); err != nil || event != want {
		t.Fatalf("status = %q %v, want %q", lines, err, want)
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != want {
		t.Fatalf("wake records = %+v %v, want the wait with its page", records, err)
	}
}

// While AFK mode is on the CFO answers a goblin's page in the Overlord's
// place: the same wait reaches the CFO as the goblin's question, with the
// page's link and the choices it declares, and puts no item in his Command
// Center. While AFK mode is off it is his wait, as before.
func TestNotifyPageWaitIsTheCFOsDecisionWhileAFKModeIsOn(t *testing.T) {
	for name, isAFKOn := range map[string]bool{"on": true, "off": false} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			stateDir := filepath.Join(dir, "state")
			if err := os.Mkdir(stateDir, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFO_HOME", dir)
			t.Setenv("CFO_STATE_OVERRIDE", "")
			page := filepath.Join(dir, "mockups.html")
			choices := `<script type="application/json" data-lavish-choices>{"id": "mockups", "question": "Build all three?", "options": ["Build it as shown", "Change it"], "recommended": "Build it as shown"}</script>`
			if err := os.WriteFile(page, []byte("<html><body>"+choices+"</body></html>"), 0o644); err != nil {
				t.Fatal(err)
			}
			fakeLavish(t)
			if isAFKOn {
				if _, _, err := afk.TurnOn(stateDir, "the board", nil, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runNotify([]string{"g1", "--waiting-on", "overlord", "approve the mockups", "--lavish", page}, &stdout, &stderr, defaultCommandRuntime())

			// Assert
			want := "waiting on overlord: approve the mockups (page http://127.0.0.1:4387/session/f26e)"
			if isAFKOn {
				want = "blocked: approve the mockups (page http://127.0.0.1:4387/session/f26e) options: Build it as shown (Recommended) | Change it"
			}
			lines, err := state.TailStatus(stateDir, "g1", 1)
			if _, event := state.SplitStatus(lines[0]); err != nil || event != want {
				t.Fatalf("status = %q %v, want %q", lines, err, want)
			}
			if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != want {
				t.Fatalf("wake records = %+v %v, want %q", records, err, want)
			}
			// g1 runs in no native terminal, so the board takes nothing from it.
			// His wait cannot go unwatched, so it fails; the CFO's decision
			// does not need the board, so it does not.
			if isAFKOn != (exit == 0) || isAFKOn == strings.Contains(stderr.String(), "nothing watches the page") {
				t.Errorf("exit=%d stderr=%q, want the wait refused only while AFK mode is off", exit, stderr.String())
			}
			if entries, _ := os.ReadDir(filepath.Join(stateDir, "reviews-inbox")); len(entries) != 0 {
				t.Errorf("review inbox = %d files, want no item for him", len(entries))
			}
			if isAFKOn && strings.Contains(stdout.String(), "AFK mode is on") {
				t.Errorf("stdout = %q, want the goblin told nothing of AFK mode: the CFO answers its page", stdout.String())
			}
		})
	}
}

// Without lavish-axi the page cannot be shown, so the notify is refused before
// anything is recorded and the goblin asks in text instead.
func TestNotifyWaitWithAPageIsRefusedWithoutLavish(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	page := filepath.Join(dir, "plan.html")
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	var stdout, stderr bytes.Buffer
	exit := runNotify([]string{"g1", "--waiting-on", "overlord", "pick a plan", "--lavish", page}, &stdout, &stderr, defaultCommandRuntime())

	if exit != 1 || !strings.Contains(stderr.String(), "ask in text with --blocked") {
		t.Fatalf("exit=%d stderr=%q, want a refusal that says to ask in text", exit, stderr.String())
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("status = %q, want nothing recorded", lines)
	}
	if records, _ := wake.Pending(stateDir); len(records) != 0 {
		t.Fatalf("wake records = %+v, want none", records)
	}
}

// A goblin's wait on the Overlord while its own step card is open is that
// card on the Command Center, as a wait with a command is its run card: a
// second card would ask him for the same step again after his Done on the
// first. The wait still wakes the CFO and tells the goblin which card it is.
func TestNotifyWaitWhileAStepCardIsOpenOpensNoSecondCard(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	record := `{"credentials":[{"id":"cred-f0ed7ac18b74f587","by":"goblin","task":"g1","project":"throwaway","why":"Tick the App Group","state":"open"}]}`
	if err := os.WriteFile(filepath.Join(stateDir, ".supervisor.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runNotify([]string{"g1", "--waiting-on", "overlord", "tick the App Group"}, &stdout, &stderr, defaultCommandRuntime())

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "cfo auth request --wait-on cred-f0ed7ac18b74f587 --task g1") || strings.Contains(stderr.String(), "cannot show this wait") {
		t.Fatalf("stdout=%q stderr=%q, want the wait shown as its open step card alone and the goblin told to wait on it", stdout.String(), stderr.String())
	}
	var reviews []supervisor.Review
	entries, err := os.ReadDir(filepath.Join(stateDir, "reviews-inbox"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(stateDir, "reviews-inbox", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var published supervisor.Review
		if err := json.Unmarshal(data, &published); err != nil {
			t.Fatal(err)
		}
		reviews = append(reviews, published)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
	if err != nil {
		t.Fatal(err)
	}
	var board supervisor.Database
	if err := json.Unmarshal(data, &board); err != nil {
		t.Fatal(err)
	}
	reviews = append(reviews, board.Reviews...)
	if i := slices.IndexFunc(reviews, func(r supervisor.Review) bool { return strings.HasPrefix(r.ID, "waiting-g1-") }); i >= 0 {
		t.Fatalf("Command Center item %+v, want no second card for the wait", reviews[i])
	}
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != "waiting on overlord: tick the App Group" {
		t.Fatalf("wake records = %+v %v, want the wait on the Overlord", records, err)
	}
}

// A wait on the Overlord names the one link he goes to with --link, which his
// card opens: it travels with the wait to the CFO, and a link that is not a
// safe web link, or one on anything but a wait on him, is refused before
// anything is recorded.
func TestNotifyWaitGivesTheLinkTheOverlordGoesTo(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	link := "https://dash.cloudflare.com/precisiondocs/dns"
	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"a link on a question":  {[]string{"g1", "--blocked", "which plan?", "--link", link}, "--link goes with --waiting-on overlord"},
		"a link on a CI wait":   {[]string{"g1", "--waiting-on", "ci", "checks", "--link", link}, "--link goes with --waiting-on overlord"},
		"a bare host name":      {[]string{"g1", "--waiting-on", "overlord", "add the records", "--link", "mcp.precisiondocs.ai"}, "absolute URL"},
		"a link with a query":   {[]string{"g1", "--waiting-on", "overlord", "add the records", "--link", link + "?token=x"}, "query"},
		"a plain http web link": {[]string{"g1", "--waiting-on", "overlord", "add the records", "--link", "http://dash.cloudflare.com/dns"}, "https"},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime()); exit != 2 || !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%s: exit=%d stderr=%q, want 2 naming %q", name, exit, stderr.String(), c.says)
		}
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("status = %q after refused notifies, want nothing recorded", lines)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runNotify([]string{"g1", "--waiting-on", "overlord", "add the records", "--link", link}, &stdout, &stderr, defaultCommandRuntime())

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	want := "waiting on overlord: add the records (link " + link + ")"
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != want {
		t.Fatalf("wake records = %+v %v, want the wait with its link", records, err)
	}
}

// nativeGoblin records goblin id as a native task whose terminal is hosted in
// stateDir with this test process as its program, so what the test runs is
// proven to come from inside it and reaches the Command Center's inboxes.
func nativeGoblin(t *testing.T, stateDir, id string) {
	t.Helper()
	worktree := t.TempDir()
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: id, SpawnGen: "gen-1", Project: worktree, Worktree: worktree, Backend: "native", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	hostThisProcess(t, stateDir, id)
}

// plantJSON writes value as JSON to path, making its folder.
func plantJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// waitsInTheInbox returns the waits goblin g1 published for the Command
// Center, which stay in the review inbox while no board takes them.
func waitsInTheInbox(t *testing.T, stateDir string) []supervisor.Review {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(stateDir, "reviews-inbox"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var waits []supervisor.Review
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(stateDir, "reviews-inbox", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var published supervisor.Review
		if err := json.Unmarshal(data, &published); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(published.ID, "waiting-g1-") {
			waits = append(waits, published)
		}
	}
	return waits
}

// decisionsForG1 are the two decisions that can be open for goblin g1, each
// put where it is first seen: the goblin's own question with choices in the
// question inbox, which the board has yet to take, and the CFO's question for
// it (cfo question --for) on the board's record, where the CFO's pipe puts
// it. Each returns the decision's ID and first line.
var decisionsForG1 = map[string]func(t *testing.T, stateDir string) (string, string){
	"the goblin's own question": func(t *testing.T, stateDir string) (string, string) {
		q := supervisor.Question{ID: "notify-g1-3", Identity: strings.Repeat("g", 64), Text: "Which store?\n- Both are set up.", Options: []string{"SQLite", "Postgres"}, Status: "pending", Task: "g1", Generation: "gen-1", Seq: 3, CreatedAt: time.Now().UTC()}
		plantJSON(t, filepath.Join(stateDir, "questions-inbox", "notify-g1-3.json"), q)
		return q.ID, "Which store?"
	},
	"the CFO's question for it": func(t *testing.T, stateDir string) (string, string) {
		q := supervisor.Question{ID: "pd-connector-parity", Identity: strings.Repeat("c", 64), Text: "Build the 63 parity tools?\n- She sized them at three days.", Options: []string{"Build them", "End her task"}, Status: "pending", ForTask: "g1", CreatedAt: time.Now().UTC()}
		plantJSON(t, filepath.Join(stateDir, ".supervisor.json"), map[string]any{"questions": []supervisor.Question{q}})
		return q.ID, "Build the 63 parity tools?"
	},
}

// The CFO, 2026-10-17: a wait follows a decision only when the goblin
// declares it, since a need the Overlord never sees stalls the goblin and an
// extra card does not. While a decision for the goblin is open, a wait on him
// that declares neither --follows nor --separate is refused, recording
// nothing, and names each open decision by its ID and first line with both
// flags; so is a --follows that names no open decision, both flags at once,
// and --follows on a wait that opens a link, which is a card of its own.
func TestNotifyWaitBesideAnOpenDecisionMustDeclareWhatItIs(t *testing.T) {
	for decisionName, open := range decisionsForG1 {
		for name, c := range map[string]struct {
			flags func(id string) []string
			says  func(id, first string) []string
		}{
			"no declaration": {
				flags: func(string) []string { return nil },
				says:  func(id, first string) []string { return []string{id + ": " + first, "--follows <id>", "--separate"} },
			},
			"--follows naming no open decision": {
				flags: func(string) []string { return []string{"--follows", "notify-g1-99"} },
				says: func(id, first string) []string {
					return []string{"--follows notify-g1-99 names no decision open for g1", id + ": " + first}
				},
			},
			"--follows and --separate": {
				flags: func(id string) []string { return []string{"--follows", id, "--separate"} },
				says:  func(string, string) []string { return []string{"do not go together"} },
			},
			"--follows on a wait with a link": {
				flags: func(id string) []string {
					return []string{"--follows", id, "--link", "https://dashboard.stripe.com/login"}
				},
				says: func(string, string) []string { return []string{"give --separate instead"} },
			},
		} {
			t.Run(decisionName+", "+name, func(t *testing.T) {
				// Arrange
				stateDir := questionHome(t)
				id, first := open(t, stateDir)
				var stdout, stderr bytes.Buffer

				// Act
				exit := runNotify(append([]string{"g1", "--waiting-on", "overlord", "Sign in to Stripe in the browser I opened"}, c.flags(id)...), &stdout, &stderr, defaultCommandRuntime())

				// Assert
				if exit != 2 {
					t.Fatalf("exit=%d stderr=%q, want the wait refused", exit, stderr.String())
				}
				for _, said := range c.says(id, first) {
					if !strings.Contains(stderr.String(), said) {
						t.Errorf("stderr = %q, want it to say %q", stderr.String(), said)
					}
				}
				if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
					t.Errorf("a refused wait recorded status %q", lines)
				}
				if records, err := wake.Pending(stateDir); err != nil || len(records) != 0 {
					t.Errorf("a refused wait woke the CFO: %+v %v", records, err)
				}
				if waits := waitsInTheInbox(t, stateDir); len(waits) != 0 {
					t.Errorf("a refused wait published %+v", waits)
				}
			})
		}
	}
}

// A wait declared to be the open decision is published following it by its
// ID, so the board shows only the decision while it is open and its answer
// closes the wait; one declared --separate follows none and is a card of its
// own.
func TestNotifyWaitPublishesWhatItDeclared(t *testing.T) {
	for decisionName, open := range decisionsForG1 {
		for name, isFollowing := range map[string]bool{"--follows the decision": true, "--separate": false} {
			t.Run(decisionName+", "+name, func(t *testing.T) {
				// Arrange
				stateDir := questionHome(t)
				nativeGoblin(t, stateDir, "g1")
				id, _ := open(t, stateDir)
				flags, want := []string{"--separate"}, ""
				if isFollowing {
					flags, want = []string{"--follows", id}, id
				}
				var stdout, stderr bytes.Buffer

				// Act
				exit := runNotify(append([]string{"g1", "--waiting-on", "overlord", "Waiting for your answer on the store"}, flags...), &stdout, &stderr, defaultCommandRuntime())

				// Assert
				if exit != 0 || stderr.Len() != 0 {
					t.Fatalf("exit=%d stderr=%q, want the wait taken", exit, stderr.String())
				}
				if waits := waitsInTheInbox(t, stateDir); len(waits) != 1 || waits[0].Follows != want {
					t.Fatalf("published waits = %+v, want one following %q", waits, want)
				}
				if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != "waiting on overlord: Waiting for your answer on the store" {
					t.Fatalf("wake records = %+v %v, want the wait on the Overlord", records, err)
				}
			})
		}
	}
}

// With no decision open a wait on the Overlord needs no declaration and is a
// card of its own, as it is with --separate; --follows is refused, saying
// none is open, and either flag on anything but a wait on him is refused.
func TestNotifyWaitWithNoDecisionOpenIsACardOfItsOwn(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"--follows with none open": {[]string{"g1", "--waiting-on", "overlord", "Sign in to Stripe", "--follows", "notify-g1-3"}, "and none is"},
		"--follows on a question":  {[]string{"g1", "--blocked", "Which store? options: SQLite | Postgres", "--follows", "notify-g1-3"}, "go with --waiting-on overlord"},
		"--separate on a CI wait":  {[]string{"g1", "--waiting-on", "ci", "PR 45 checks", "--separate"}, "go with --waiting-on overlord"},
		"--separate on working":    {[]string{"g1", "--working", "fixing the lint step", "--separate"}, "go with --waiting-on overlord"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			stateDir := questionHome(t)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime())

			// Assert
			if exit != 2 || !strings.Contains(stderr.String(), c.says) {
				t.Fatalf("exit=%d stderr=%q, want 2 saying %q", exit, stderr.String(), c.says)
			}
			if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
				t.Errorf("a refused notify recorded status %q", lines)
			}
			if records, err := wake.Pending(stateDir); err != nil || len(records) != 0 {
				t.Errorf("a refused notify woke the CFO: %+v %v", records, err)
			}
		})
	}
	for name, flags := range map[string][]string{"no declaration": nil, "--separate": {"--separate"}} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			stateDir := questionHome(t)
			nativeGoblin(t, stateDir, "g1")
			var stdout, stderr bytes.Buffer

			// Act
			exit := runNotify(append([]string{"g1", "--waiting-on", "overlord", "Sign in to Stripe in the browser I opened"}, flags...), &stdout, &stderr, defaultCommandRuntime())

			// Assert
			if exit != 0 || stderr.Len() != 0 {
				t.Fatalf("exit=%d stderr=%q, want the wait taken", exit, stderr.String())
			}
			if waits := waitsInTheInbox(t, stateDir); len(waits) != 1 || waits[0].Follows != "" {
				t.Fatalf("published waits = %+v, want one card of its own", waits)
			}
		})
	}
}

// A wait or a question can carry a command for the Overlord to run with one
// click (his words, 2026-10-02: "run in powershell button"): --run names its
// file, checked before anything is recorded, and the wake tells the CFO a
// command rides with it.
func TestNotifyCarriesACommandForTheOverlordToRun(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	write := func(name, text string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	command := write("sign-in.ps1", "gh auth login\n")
	page := write("plan.html", "<html></html>")
	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"a command on a CI wait":        {[]string{"g1", "--waiting-on", "ci", "checks", "--run", command}, "--run goes with --waiting-on overlord or --blocked"},
		"a command on a working report": {[]string{"g1", "--working", "building", "--run", command}, "--run goes with --waiting-on overlord or --blocked"},
		"a command beside a page":       {[]string{"g1", "--waiting-on", "overlord", "sign in", "--lavish", page, "--run", command}, "--run and --lavish"},
		"a file that is not there":      {[]string{"g1", "--waiting-on", "overlord", "sign in", "--run", filepath.Join(dir, "missing.ps1")}, "--run"},
		"a file of another kind":        {[]string{"g1", "--waiting-on", "overlord", "sign in", "--run", write("sign-in.txt", "gh auth login\n")}, ".ps1"},
		"an empty command":              {[]string{"g1", "--waiting-on", "overlord", "sign in", "--run", write("empty.ps1", "  \n")}, "empty"},
		// A command his click would run nothing of, as repair-home-v0.5.3's
		// last line did on 2026-10-07, never reaches him.
		"a command that does not parse": {[]string{"g1", "--waiting-on", "overlord", "sign in", "--run", write("broken.ps1", "\"rules: $(@($settings.permissions.allow | Where-Object { $_ -match '^(Bash|PowerShell)\\(cfo ' }).Count)\"\n")}, "line 1"},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runNotify(c.args, &stdout, &stderr, defaultCommandRuntime()); exit != 2 || !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%s: exit=%d stderr=%q, want 2 naming %q", name, exit, stderr.String(), c.says)
		}
	}
	if lines, _ := state.TailStatus(stateDir, "g1", 1); len(lines) != 0 {
		t.Fatalf("status = %q after refused notifies, want nothing recorded", lines)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runNotify([]string{"g1", "--waiting-on", "overlord", "Sign in to GitHub so I can push", "--run", command}, &stdout, &stderr, defaultCommandRuntime())

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	want := "waiting on overlord: Sign in to GitHub so I can push (runs sign-in.ps1)"
	if records, err := wake.Pending(stateDir); err != nil || len(records) != 1 || records[0].Detail != want {
		t.Fatalf("wake records = %+v %v, want the wait naming its command", records, err)
	}
	// No goblin runs here, so the board cannot take the command, and the
	// notify says so instead of failing: the CFO has the wait.
	if !strings.Contains(stderr.String(), "the Command Center cannot show this command") {
		t.Errorf("stderr = %q, want it said that the command is not on the board", stderr.String())
	}
}

// While AFK mode is on a goblin that reports a wait on the Overlord is told he
// is away and to move to work that does not depend on it, so it does not sit
// all night on something only he can do. While it is off the notify says
// nothing of it.
func TestNotifyTellsAGoblinWaitingOnTheOverlordToMoveOnWhileAFKModeIsOn(t *testing.T) {
	for name, away := range map[string]bool{"on": true, "off": false} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			stateDir := filepath.Join(dir, "state")
			if err := os.Mkdir(stateDir, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFO_HOME", dir)
			if away {
				if _, _, err := afk.TurnOn(stateDir, "the board", nil, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runNotify([]string{"g1", "--waiting-on", "overlord", "log in to Stripe"}, &stdout, &stderr, defaultCommandRuntime())

			// Assert
			if exit != 0 || !strings.Contains(stdout.String(), "notified g1 waiting on overlord: log in to Stripe") {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want the wait reported", exit, stdout.String(), stderr.String())
			}
			told := strings.Contains(stdout.String(), "AFK mode is on") && strings.Contains(stdout.String(), "move to that next piece") && strings.Contains(stdout.String(), `cfo notify g1 --working`)
			if told != away {
				t.Errorf("stdout = %q, want the goblin told to move on only while AFK mode is on (%v)", stdout.String(), away)
			}
		})
	}
}

// helperHome is a home where goblin g1 has helper g1-h1, with a runtime that
// records what is typed into a terminal instead of typing it, and fails the
// way sendErr says.
func helperHome(t *testing.T, sendErr error) (home.Home, commandRuntime, *[][2]string) {
	t.Helper()
	h := testHome(t)
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, meta := range []state.TaskMeta{{ID: "g1"}, {ID: "g1-h1", Parent: "g1", Mode: "local-only"}} {
		meta.Window, meta.Harness, meta.Kind, meta.Backend, meta.Worktree = "native", "claude", "ship", "native", filepath.Join(h.Root, meta.ID)
		if err := state.WriteTaskMeta(h.State, meta); err != nil {
			t.Fatal(err)
		}
	}
	runtime := testCommandRuntimeForHome(h)
	var typed [][2]string
	runtime.sendText = func(_ context.Context, _ home.Home, target, text string) error {
		typed = append(typed, [2]string{target, text})
		return sendErr
	}
	return h, runtime, &typed
}

func TestAHelpersReportsReachItsParentAndNotTheCFO(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"g1-h1", "--done"}, []string{"Your helper g1-h1 is done", "cfo helper merge g1"}},
		{[]string{"g1-h1", "--blocked", "Which table?\n- users or accounts"}, []string{"Your helper g1-h1 asks: Which table?", "cfo send g1-h1"}},
		{[]string{"g1-h1", "--failed", "the build is broken"}, []string{"Your helper g1-h1 failed: the build is broken", "cfo kill g1-h1"}},
	}
	for _, c := range cases {
		t.Run(c.args[1], func(t *testing.T) {
			// Arrange
			h, runtime, typed := helperHome(t, nil)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runNotify(c.args, &stdout, &stderr, runtime)

			// Assert
			if exit != 0 {
				t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
			}
			if len(*typed) != 1 || (*typed)[0][0] != "g1" {
				t.Fatalf("typed = %q, want one report in g1's terminal", *typed)
			}
			for _, want := range c.want {
				if !strings.Contains((*typed)[0][1], want) {
					t.Errorf("the report %q does not say %q", (*typed)[0][1], want)
				}
			}
			if strings.Contains((*typed)[0][1], "\n") {
				t.Errorf("the report %q spans lines; a line break would submit it early", (*typed)[0][1])
			}
			if records, err := wake.Pending(h.State); err != nil || len(records) != 0 {
				t.Errorf("wake records = %+v, %v; want the CFO left out of a helper's report", records, err)
			}
			if lines, _ := state.TailStatus(h.State, "g1-h1", 1); len(lines) != 1 {
				t.Errorf("status = %v, want the report on the helper's own log", lines)
			}
		})
	}
}

func TestAHelpersReportWakesTheCFOWhenItsParentCannotTakeIt(t *testing.T) {
	// Arrange
	h, runtime, _ := helperHome(t, errors.New("g1's terminal has ended"))
	var stdout, stderr bytes.Buffer

	// Act
	exit := runNotify([]string{"g1-h1", "--done"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	records, err := wake.Pending(h.State)
	if err != nil || len(records) != 1 || records[0].Key != "g1-h1" || !strings.Contains(records[0].Detail, "could not reach its parent g1") || !strings.Contains(records[0].Detail, "terminal has ended") {
		t.Errorf("wake records = %+v, %v; want the undelivered report woken to the CFO with why", records, err)
	}
}

func TestOnlyAHelperReportsDoneWithoutAPullRequest(t *testing.T) {
	// Arrange
	h, runtime, typed := helperHome(t, nil)
	var stdout, stderr bytes.Buffer

	// Act
	exit := runNotify([]string{"g1", "--done"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 2 || !strings.Contains(stderr.String(), "--pr") || len(*typed) != 0 {
		t.Errorf("exit = %d, stderr = %q, typed = %q; want the parent's done refused without --pr", exit, stderr.String(), *typed)
	}
	if lines, _ := state.TailStatus(h.State, "g1", 1); len(lines) != 0 {
		t.Errorf("status = %v, want nothing recorded for a refused notify", lines)
	}
}

func TestAHelpersWorkingReportStaysOnTheBoard(t *testing.T) {
	_, runtime, typed := helperHome(t, nil)
	var stdout, stderr bytes.Buffer

	exit := runNotify([]string{"g1-h1", "--working", "writing the migration"}, &stdout, &stderr, runtime)

	if exit != 0 || len(*typed) != 0 {
		t.Errorf("exit = %d, typed = %q; want a working report recorded and typed nowhere", exit, *typed)
	}
}

// A goblin's done report names what it left running, so the CFO reads it in
// the wake that tells it the goblin is done, and the goblin is told to end
// what it no longer needs by its pid.
func TestNotifyDoneNamesWhatTheGoblinLeftRunning(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", dir)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	if err := state.WriteTaskMeta(filepath.Join(dir, "state"), state.TaskMeta{ID: "g1", SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}
	runtime := defaultCommandRuntime()
	runtime.github = proofRunner{stdout: strings.Repeat("a", 40)}
	runtime.leftRunning = func(_ home.Home, id string) ([]fleettree.Placed, error) {
		if id != "g1" {
			t.Errorf("read what %s left, want g1", id)
		}
		return []fleettree.Placed{
			{Process: fleettree.Process{PID: 400, ParentPID: 1, Exe: "node.exe"}, Owner: "g1", Command: "node server.js --port 3000"},
			{Process: fleettree.Process{PID: 401, ParentPID: 400, Exe: "chrome.exe"}, Owner: "g1", Command: "chrome.exe --headless=new"},
		}, nil
	}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runNotify([]string{"g1", "--done", "--pr", "https://github.com/o/r/pull/9"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
	want := `done: PR https://github.com/o/r/pull/9 (left running: pid 400 node.exe "node server.js --port 3000" (under it chrome.exe pid 401))`
	lines, err := state.TailStatus(filepath.Join(dir, "state"), "g1", 1)
	if err != nil || len(lines) != 1 || !strings.HasSuffix(lines[0], want) {
		t.Errorf("status = %q (%v), want it to end %q", lines, err, want)
	}
	records, err := wake.Pending(filepath.Join(dir, "state"))
	if err != nil || len(records) != 1 || records[0].Detail != want {
		t.Errorf("wakes = %+v (%v), want one saying %q", records, err, want)
	}
	if output := stdout.String(); !strings.Contains(output, "You left running pid 400 node.exe") || !strings.Contains(output, "Stop-Process -Id <pid>") {
		t.Errorf("stdout = %q, want the goblin told what it left and how to end it by its pid", output)
	}
}
