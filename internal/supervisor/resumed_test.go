package supervisor

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// resumedFixture writes native task "task" in session; its record of resume
// "auto-resume-1", begun at started, whose relaunch started resumed and which
// was recorded failed three minutes and a second later; and its status log of
// lines, each stamped at its time. A session other than resumed is one a
// later start, which records no resume, began.
func resumedFixture(t *testing.T, session, resumed string, started time.Time, lines map[time.Time]string) (*HTTP, string) {
	t.Helper()
	handler, h := orderBoard(t)
	meta := state.TaskMeta{ID: "task", SpawnGen: session, Backend: "native"}
	if session == resumed {
		meta.ResumeOperation = "auto-resume-1"
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	record := state.Lifecycle{ID: meta.ID, Generation: resumed, RequestGeneration: "s1", Operation: "auto-resume-1", Action: "resume", Phase: "failed", Started: started, Updated: started.Add(3*time.Minute + time.Second), Reason: "Pause condition cleared", Problems: []string{"spawn: native terminal task never showed its harness working on the instruction"}, NoticeSent: true}
	if err := state.WriteLifecycle(h.State, record); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	for _, stamp := range slices.SortedFunc(maps.Keys(lines), time.Time.Compare) {
		fmt.Fprintf(&log, "%s %s\n", stamp.UTC().Format(time.RFC3339), lines[stamp])
	}
	writeFile(t, state.StatusPath(h.State, meta.ID), log.String())
	return handler, h.State
}

// boardTask is task "task" as the board draws it.
func boardTask(t *testing.T, handler *HTTP) Task {
	t.Helper()
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == "task" })
	if index < 0 {
		t.Fatalf("the board shows no task: %+v", snapshot.Tasks)
	}
	return snapshot.Tasks[index]
}

// sessionAt names the session a start began at.
func sessionAt(at time.Time) string {
	return fmt.Sprintf("s%d", at.UnixNano())
}

// On 2026-10-20 the board drew Teddy as "Resume failed" for hours while he
// worked: his resume began at 13:07, was recorded failed at 13:10, and he
// reported working from the resumed session at 13:11. A failed resume whose
// goblin reports from the session it started reads as resumed, on its record
// and on the board, and so does one whose session a later start replaced, as
// a switch replaced Teddy's at 15:23. One without such a report, with reports
// only from before it began or from a later session, stays failed, and so
// does one whose only report is the working line its own relaunch wrote
// before it failed, as Codex's on hooks it did not trust.
func TestAFailedResumeReadsResumedOnceItsGoblinReportsFromItsSession(t *testing.T) {
	started := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	resumed, switched := sessionAt(started.Add(30*time.Second)), sessionAt(started.Add(90*time.Minute))
	failure := map[time.Time]string{
		started.Add(-time.Hour - 30*time.Second): "working: before the pause",
		started.Add(-time.Hour - 20*time.Second): "blocked: before the pause?",
		started.Add(-time.Hour - 10*time.Second): "done: PR https://github.com/example/repo/pull/1",
		started.Add(-time.Hour):                  "lifecycle-paused: memory",
		started.Add(3 * time.Minute):             "failed: spawn: native terminal task never showed its harness working on the instruction",
		started.Add(3*time.Minute + time.Second): "lifecycle-failed: Pause condition cleared",
	}
	with := func(reports map[time.Duration]string) map[time.Time]string {
		lines := maps.Clone(failure)
		for after, line := range reports {
			lines[started.Add(after)] = line
		}
		return lines
	}
	for _, test := range []struct {
		name      string
		session   string
		lines     map[time.Time]string
		wantPhase string
		wantBoard string
	}{
		{name: "a working report after the failure", session: resumed, lines: with(map[time.Duration]string{5 * time.Minute: "working: Back after restart"}), wantPhase: "running", wantBoard: "working"},
		{name: "a question asked from the session", session: resumed, lines: with(map[time.Duration]string{5 * time.Minute: "blocked: Which project gets the token?"}), wantPhase: "running", wantBoard: "blocked"},
		{name: "a wait reported from the session", session: resumed, lines: with(map[time.Duration]string{5 * time.Minute: "waiting on ci: the pull request's checks"}), wantPhase: "running", wantBoard: "waiting"},
		{name: "reports from a session a later switch replaced", session: switched, lines: with(map[time.Duration]string{5 * time.Minute: "working: Back after restart", 90*time.Minute + 30*time.Second: "switched: claude -> claude", 95 * time.Minute: "done: PR https://github.com/example/repo/pull/2"}), wantPhase: "running"},
		{name: "only the resume's own failure", session: resumed, lines: failure, wantPhase: "failed"},
		{name: "only the working line its relaunch wrote", session: resumed, lines: with(map[time.Duration]string{time.Minute: "working: codex started without trusting its hooks, so they do not run (Hooks need review). Only the Overlord trusts hooks."}), wantPhase: "failed"},
		{name: "a failure the goblin reported", session: resumed, lines: with(map[time.Duration]string{5 * time.Minute: "failed: the tests do not build"}), wantPhase: "failed"},
		{name: "reports only from a later session", session: switched, lines: with(map[time.Duration]string{90*time.Minute + 30*time.Second: "switched: claude -> claude", 95 * time.Minute: "working: on the handoff"}), wantPhase: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			handler, stateDir := resumedFixture(t, test.session, resumed, started, test.lines)
			if before := boardTask(t, handler); test.session == resumed && (before.Lifecycle == nil || before.Lifecycle.Action != "resume" || before.Lifecycle.Phase != "failed") {
				t.Fatalf("before = %+v, want the board to draw the failed resume, or this proves nothing", before.Lifecycle)
			}

			// Act
			err := finishTakenResumes(stateDir)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			record, err := state.ReadLifecycle(stateDir, "task")
			if err != nil || record.Phase != test.wantPhase || record.Action != "resume" || record.Operation != "auto-resume-1" || record.Generation != resumed {
				t.Fatalf("record = %+v, %v; want the resume of %s %s", record, err, resumed, test.wantPhase)
			}
			lines, _ := state.TailStatus(stateDir, "task", 1)
			isFinished := len(lines) == 1 && strings.Contains(lines[0], " lifecycle-running: Pause condition cleared; its goblin reported from the session this resume started")
			if isFinished != (test.wantPhase == "running") {
				t.Errorf("last status line = %q, want a resume finished only when it resumed", lines)
			}
			if test.wantBoard == "" {
				return
			}
			if after := boardTask(t, handler); after.Lifecycle != nil || after.Phase != test.wantBoard {
				t.Errorf("the board draws %s with %+v, want %s as its goblin reported, with no failed resume", after.Phase, after.Lifecycle, test.wantBoard)
			}
		})
	}
}

// A pause, resume or stop under way holds the task's lifecycle lock and owns
// its record, so a failed resume it holds is left to it and finished at a
// later reading, once the lock is free.
func TestAFailedResumeHeldByALifecycleOperationIsLeftToIt(t *testing.T) {
	// Arrange
	started := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	resumed := sessionAt(started.Add(30 * time.Second))
	_, stateDir := resumedFixture(t, resumed, resumed, started, map[time.Time]string{started.Add(5 * time.Minute): "working: Back after restart"})
	name := state.LifecycleLockName("task")
	if _, err := lock.AcquireExclusiveNamed(stateDir, name); err != nil {
		t.Fatal(err)
	}

	// Act
	held := finishTakenResumes(stateDir)
	heldRecord, heldErr := state.ReadLifecycle(stateDir, "task")
	if err := lock.ReleaseExclusiveNamed(stateDir, name); err != nil {
		t.Fatal(err)
	}
	freed := finishTakenResumes(stateDir)

	// Assert
	if held != nil || heldErr != nil || heldRecord.Phase != "failed" {
		t.Errorf("under the lock: %v, record %+v, %v; want it left failed", held, heldRecord, heldErr)
	}
	if record, err := state.ReadLifecycle(stateDir, "task"); freed != nil || err != nil || record.Phase != "running" {
		t.Errorf("once the lock is free: %v, record %+v, %v; want it running", freed, record, err)
	}
}

// A failed resume finished as running types nothing more into its goblin's
// terminal, and the instruction it typed carried only the board messages kept
// before it began. So each board message kept for it goes to the CFO with its
// words and the resume's start, as a message no goblin could take does, and a
// note that holds no board message wakes nobody.
func TestAFinishedResumeHandsTheMessagesKeptForItToTheCFO(t *testing.T) {
	started := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	resumed := sessionAt(started.Add(30 * time.Second))
	answer := "The Overlord answered your question: use the blue one"
	for _, test := range []struct {
		name string
		note string
		want []string
	}{
		{name: "two board messages", note: strings.Join([]string{answer, fromOverlord("use the staging token"), fromOverlord("then rerun the migrations")}, "\n"), want: []string{"use the staging token", "then rerun the migrations"}},
		{name: "no board message", note: answer},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			_, stateDir := resumedFixture(t, resumed, resumed, started, map[time.Time]string{
				started.Add(3*time.Minute + time.Second): "lifecycle-failed: Pause condition cleared",
				started.Add(5 * time.Minute):             "working: Back after restart",
			})
			record, err := state.ReadLifecycle(stateDir, "task")
			if err != nil {
				t.Fatal(err)
			}
			record.ResumeNote = test.note
			if err := state.WriteLifecycle(stateDir, record); err != nil {
				t.Fatal(err)
			}

			// Act
			err = finishTakenResumes(stateDir)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if record, err := state.ReadLifecycle(stateDir, "task"); err != nil || record.Phase != "running" {
				t.Fatalf("record = %+v, %v; want the resume finished as running", record, err)
			}
			pending, err := wake.Pending(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			var told []string
			for _, entry := range pending {
				if entry.Kind == "notify" && entry.Key == "task" {
					told = append(told, entry.Detail)
				}
			}
			if len(told) != len(test.want) {
				t.Fatalf("the CFO was told %q, want each of %q", told, test.want)
			}
			for index, words := range test.want {
				if !strings.HasPrefix(told[index], "message not delivered: ") || !strings.Contains(told[index], started.Format(time.RFC3339)) || !strings.HasSuffix(told[index], "The Overlord wrote: "+words) {
					t.Errorf("the CFO was told %q, want the message %q with the resume's start %s", told[index], words, started.Format(time.RFC3339))
				}
			}
		})
	}
}
