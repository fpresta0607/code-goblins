package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// keptReport is the line goblins notify --done --report records for a local-only
// task: its report as the home keeps it, data/<id>/report.md.
func keptReport(h home.Home, id string) (line, path string) {
	path = filepath.Join(h.Data, id, "report.md")
	return "done: report " + path, path
}

// A local-only task opens no pull request, so the report it kept is what
// says it finished: its idle terminal does not turn that into a request for
// input. Its card stayed working with its work finished (#1675).
func TestALocalOnlyTaskReadsDoneWithTheReportItKept(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, hasSession := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/session=%t", harness, hasSession), func(t *testing.T) {
				// Arrange
				store, h := atWorkFixture(t, harness, "task-1")
				meta, err := state.ReadTaskMeta(h.State, "task-1")
				if err != nil {
					t.Fatal(err)
				}
				meta.Mode = "local-only"
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
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
				line, path := keptReport(h, meta.ID)
				fileReport(t, h, meta.ID, line, now.Add(-time.Minute))
				seeTerminal(t, h, meta.ID, monitor.HealthIdle)

				// Act
				got := snapshotTask(t, &Service{Store: store}, meta.ID)

				// Assert
				if got.Phase != "done" || got.Reason != "report "+path || got.Report != "done" || got.KeptReport != path {
					t.Fatalf("task = %+v, want it done with the report it kept", got)
				}
				if got.Verified || got.PR != "" || got.WaitingOn != "" || got.ClosedIssue != "" {
					t.Fatalf("a kept report invented delivery evidence: %+v", got)
				}
			})
		}
	}
}

// A restart leaves down a goblin whose latest report kept its report, as it
// does one that delivered a pull request, and brings back one whose done
// line names some other file.
func TestHasDeliveredCountsTheReportALocalOnlyTaskKept(t *testing.T) {
	for _, test := range []struct {
		name   string
		report func(h home.Home) string
		want   bool
	}{
		{"its kept report", func(h home.Home) string { line, _ := keptReport(h, "task-1"); return line }, true},
		{"another file", func(h home.Home) string { return "done: report " + filepath.Join(h.Data, "task-1", "notes.md") }, false},
		{"a report it is writing", func(h home.Home) string { _, path := keptReport(h, "task-1"); return "working: writing " + path }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			_, h := atWorkFixture(t, "claude", "task-1")
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			fileReport(t, h, meta.ID, test.report(h), time.Now().UTC())

			// Act
			got, err := HasDelivered(h.State, meta)

			// Assert
			if err != nil || got != test.want {
				t.Errorf("HasDelivered = %t, %v; want %t", got, err, test.want)
			}
		})
	}
}

// A local-only task whose outcome record is gone still shows as finished
// from its status log, as one that closed its issue does.
func TestACompletedCardOfALocalOnlyReportReadsDoneFromItsStatusLog(t *testing.T) {
	// Arrange
	h := home.Home{State: t.TempDir(), Data: t.TempDir()}
	now := time.Now().UTC()
	line, path := keptReport(h, "scout-task")
	log := now.Add(-time.Minute).Format(time.RFC3339) + " " + line + "\n" +
		now.Format(time.RFC3339) + " done: returned worktree via goblins cleanup\n"
	if err := os.WriteFile(filepath.Join(h.State, "scout-task.status"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	tasks := finishedTasks(h, &keptReads{}, now)

	// Assert
	if len(tasks) != 1 || tasks[0].Phase != "done" || tasks[0].Reason != "Reported "+path+"; task cleaned up" || tasks[0].PR != "" {
		t.Fatalf("tasks = %+v, want one finished task that names the report it kept", tasks)
	}
}

func TestTheAFKReportCountsTheReportALocalOnlyTaskKept(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	line, path := keptReport(home.Home{Data: t.TempDir()}, "scout-task")
	since := time.Date(2026, 10, 7, 23, 48, 36, 0, time.UTC)
	inside := since.Add(time.Hour)
	stamp := inside.Format(time.RFC3339)
	log := stamp + " working: proving the strays\n" + stamp + " " + line + "\n"
	statusPath := filepath.Join(stateDir, "scout-task.status")
	if err := os.WriteFile(statusPath, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(statusPath, inside, inside); err != nil {
		t.Fatal(err)
	}

	// Act
	finished, err := doneBetween(stateDir, since, since.Add(80*time.Minute))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(finished) != 1 || finished[0].Task != "scout-task" || finished[0].Report != path || finished[0].PR != "" || finished[0].Closed != "" || finished[0].Note != "proving the strays" {
		t.Errorf("finished = %+v, want the kept report with the goblin's note and no pull request", finished)
	}
}
