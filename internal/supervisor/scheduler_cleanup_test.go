package supervisor

import (
	"errors"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// On 2026-10-09 the scheduler resumed a paused goblin while the CFO cleaned
// it up. The cleanup wins now, and the resume it refused is no failure of the
// goblin's: its task is gone, or goes once the cleanup ends, so the board
// shows no failed resume and the CFO is not told of one. A resume that fails
// for any other reason is still a failure.
func TestAnAutomaticResumeRefusedForItsTasksCleanupIsNoFailure(t *testing.T) {
	const id = "paused-task"
	tests := []struct {
		name string
		// output is what cfo resume printed before it exited 1.
		output string
		// isRetired has the cleanup retire the task's record while the resume
		// runs.
		isRetired bool
		isFailure bool
	}{
		{name: "the cleanup still holds the task", output: "Error: " + lock.ErrHeld.Error() + ": " + state.CleanupPurpose(id) + ", pid 4242 on HOST since 2026-10-09T20:15:02Z\n"},
		{name: "the cleanup retired the task first", output: "Error: read meta: open " + id + ".meta: The system cannot find the file specified.\n", isRetired: true},
		{name: "the resume failed for another reason", output: "Error: the harness did not start\n", isFailure: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{output: test.output, err: errors.New("cfo exited 1")}
			s, h := helperBoard(t, 8*gigabyte, spawner)
			pausedGoblin(t, h, id, "memory", "", time.Now().UTC().Add(-time.Hour))
			if test.isRetired {
				spawner.during = func(args []string) {
					if args[0] == "resume" {
						if err := state.RemoveTaskMeta(h.State, id); err != nil {
							t.Error(err)
						}
					}
				}
			}
			record, err := state.ReadLifecycle(h.State, id)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			if err := s.resumeAutomatically(record); err != nil {
				t.Fatal(err)
			}
			awaitDispatch(t, s, spawner, 1)

			// Assert
			s.starts.Lock()
			failure, isFailed := s.changeErrors[id]
			s.starts.Unlock()
			if isFailed != test.isFailure {
				t.Errorf("the refused resume is recorded as a failure: %v (%q), want %v", isFailed, failure.Message, test.isFailure)
			}
		})
	}
}
