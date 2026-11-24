package supervisor

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// holdTask makes id a task of project that names issue number of acme/widgets,
// paused when isPaused and live otherwise.
func holdTask(t *testing.T, h home.Home, id, project string, number int, isPaused bool) {
	t.Helper()
	writeFile(t, filepath.Join(h.Data, id, "brief.md"), fmt.Sprintf("# Brief %s\n\n## Task\n\nFix https://github.com/acme/widgets/issues/%d.\n", id, number))
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: id, Project: project, Harness: "codex", Backend: "native", SpawnGen: "g1"}); err != nil {
		t.Fatal(err)
	}
	if !isPaused {
		return
	}
	pause, err := state.NewPauseCondition("overlord", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: id, Generation: "g1", Operation: "pause-1", Action: "pause", Phase: "paused", Started: time.Now(), Pause: &pause}); err != nil {
		t.Fatal(err)
	}
}

func TestTheSchedulerHoldsAStartThatNamesAnIssueWorkOfItsProjectNames(t *testing.T) {
	cases := []struct {
		name           string
		holderNumber   int
		isOtherProject bool // the holder is of another project
		isHolderPaused bool
		isReviewed     bool
		wantHeld       string // the holder's state in the wake, empty when the start is not held
	}{
		{name: "a paused task of the project", holderNumber: 30, isHolderPaused: true, isReviewed: true, wantHeld: "paused"},
		{name: "a live task of the project", holderNumber: 30, isReviewed: true, wantHeld: "live"},
		{name: "a task of another project", holderNumber: 30, isOtherProject: true, isHolderPaused: true, isReviewed: true},
		{name: "a task that names another issue", holderNumber: 31, isHolderPaused: true, isReviewed: true},
		{name: "no work review", holderNumber: 30, isHolderPaused: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 16*gigabyte, spawner)
			checkout := t.TempDir()
			resolved := 0
			if c.isReviewed {
				handler.Service.Options.WorkReview = &WorkReview{Resolve: func(string) (string, string, error) {
					resolved++
					return "acme/widgets", checkout, nil
				}}
			}
			queueBriefedTask(t, h, "- [ ] next-task - Ship it (repo: widgets)", strings.Replace(plainBrief, "Ship it.", "Ship it: https://github.com/acme/widgets/issues/30.", 1))
			holderProject := checkout
			if c.isOtherProject {
				holderProject = t.TempDir()
			}
			holdTask(t, h, "holder-task", holderProject, c.holderNumber, c.isHolderPaused)

			// Act: two readings, so a second wake would show.
			now := time.Now().UTC()
			starts, readings := 1, 1
			if c.wantHeld != "" {
				starts, readings = 0, 2
			}
			for reading := range readings {
				if err := handler.Service.checkFleet(t.Context(), now.Add(time.Duration(reading)*time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			calls := awaitCalls(t, spawner, starts)
			awaitSettled(t, handler.Service)

			// Assert
			var wakes []string
			for _, record := range fleetWakeRecords(t, h, "notify") {
				if record.Key == "next-task" && strings.HasPrefix(record.Detail, "start_held:") {
					wakes = append(wakes, record.Detail)
				}
			}
			if len(calls) != starts {
				t.Fatalf("dispatches=%v, want %d", calls, starts)
			}
			if c.wantHeld == "" {
				if len(wakes) != 0 {
					t.Fatalf("wakes=%q, want no hold for a start that goes ahead", wakes)
				}
				return
			}
			want := "the start of next-task is held because holder-task (" + c.wantHeld + ") of the same project names acme/widgets#30; fold next-task into holder-task"
			if len(wakes) != 1 || !strings.Contains(wakes[0], want) || resolved != 1 {
				t.Fatalf("wakes=%q after %d resolutions, want one containing %q after one", wakes, resolved, want)
			}
		})
	}
}
