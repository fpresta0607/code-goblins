package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const closedReport = "closed https://github.com/o/r/issues/1450 (decision 7: Close it, no PR)"

// A task a decision ruled finished closed its issue and has no gate or pull
// request to show for it, so its report is what says it finished: its idle
// terminal does not turn that into a request for input.
func TestATaskRuledFinishedReadsDoneWithTheIssueItClosed(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, hasSession := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/session=%t", harness, hasSession), func(t *testing.T) {
				// Arrange
				store, h := atWorkFixture(t, harness, "task-1")
				meta, err := state.ReadTaskMeta(h.State, "task-1")
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC()
				if hasSession {
					for _, moment := range []string{"start", "prompt", "settle"} {
						harnessHook(t, store, h, harness, meta.ID, moment, now.Add(-2*time.Minute))
					}
				}
				store.db.Tasks[meta.ID] = Evaluation{Generation: meta.SpawnGen, Phase: "review", Reason: "Manual task mode requires its existing review and delivery evidence", At: now.Add(-2 * time.Minute)}
				if err := store.save(); err != nil {
					t.Fatal(err)
				}
				fileReport(t, h, meta.ID, "done: "+closedReport, now.Add(-time.Minute))
				seeTerminal(t, h, meta.ID, monitor.HealthIdle)

				// Act
				got := snapshotTask(t, &Service{Store: store}, meta.ID)

				// Assert
				if got.Phase != "done" || got.Reason != closedReport || got.Report != "done" || got.ClosedIssue != "https://github.com/o/r/issues/1450" {
					t.Fatalf("task = %+v, want it done with what it closed", got)
				}
				if got.Verified || got.PR != "" || got.WaitingOn != "" {
					t.Fatalf("a closed issue invented delivery evidence: %+v", got)
				}
			})
		}
	}
}

// A done line of a top-level task that names no closed issue is no delivery,
// so the task still reads as its evaluation says.
func TestADoneLineThatNamesNoIssueDoesNotFinishATopLevelTask(t *testing.T) {
	// Arrange
	store, h := atWorkFixture(t, "codex", "task-1")
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store.db.Tasks[meta.ID] = Evaluation{Generation: meta.SpawnGen, Phase: "review", Reason: "Manual task mode requires its existing review and delivery evidence", At: now.Add(-2 * time.Minute)}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	fileReport(t, h, meta.ID, "done: closed the issue myself", now.Add(-time.Minute))
	seeTerminal(t, h, meta.ID, monitor.HealthIdle)

	// Act
	got := snapshotTask(t, &Service{Store: store}, meta.ID)

	// Assert
	if got.Phase == "done" || got.ClosedIssue != "" {
		t.Fatalf("task = %+v, want no delivery read from a line that names no issue", got)
	}
}

func TestHasDeliveredCountsAnIssueClosedInPlaceOfAPullRequest(t *testing.T) {
	for report, want := range map[string]bool{
		"done: " + closedReport:                  true,
		"done: PR https://github.com/o/r/pull/9": true,
		"done: closed the issue myself":          false,
		"working: closing the issue":             false,
	} {
		t.Run(report, func(t *testing.T) {
			// Arrange
			_, h := atWorkFixture(t, "claude", "task-1")
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			fileReport(t, h, meta.ID, report, time.Now().UTC())

			// Act
			got, err := HasDelivered(h.State, meta)

			// Assert
			if err != nil || got != want {
				t.Errorf("HasDelivered = %t, %v; want %t", got, err, want)
			}
		})
	}
}

// A task whose outcome record is gone still shows as finished from its status
// log, as one that delivered a pull request does.
func TestACompletedCardOfAClosedIssueReadsDoneFromItsStatusLog(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	now := time.Now().UTC()
	log := now.Add(-time.Minute).Format(time.RFC3339) + " done: " + closedReport + "\n" +
		now.Format(time.RFC3339) + " done: returned worktree via cfo cleanup\n"
	if err := os.WriteFile(filepath.Join(stateDir, "closed-task.status"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	tasks := finishedTasks(home.Home{State: stateDir, Data: t.TempDir()}, &keptReads{}, now)

	// Assert
	if len(tasks) != 1 || tasks[0].Phase != "done" || !strings.Contains(tasks[0].Reason, "https://github.com/o/r/issues/1450") || tasks[0].PR != "" {
		t.Fatalf("tasks = %+v, want one finished task that names the issue it closed", tasks)
	}
}

func TestTheAFKReportCountsAnIssueClosedInPlaceOfAPullRequest(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	since := time.Date(2026, 10, 7, 23, 48, 36, 0, time.UTC)
	inside := since.Add(time.Hour)
	stamp := inside.Format(time.RFC3339)
	log := stamp + " working: reading the issue\n" + stamp + " done: " + closedReport + "\n"
	path := filepath.Join(stateDir, "closed-task.status")
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, inside, inside); err != nil {
		t.Fatal(err)
	}

	// Act
	finished, err := doneBetween(stateDir, since, since.Add(80*time.Minute))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(finished) != 1 || finished[0].Task != "closed-task" || finished[0].Closed != "https://github.com/o/r/issues/1450" || finished[0].PR != "" || finished[0].Note != "reading the issue" {
		t.Errorf("finished = %+v, want the closed issue with the goblin's note and no pull request", finished)
	}
}
