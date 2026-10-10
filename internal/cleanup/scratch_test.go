package cleanup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// The 2026-10-09 defect: the first Git Bash after a restart was a goblin's,
// so its scratch folder was /tmp for every Git Bash of the user, and cleaning
// that task up took /tmp from all of them.
func TestCleanupLeavesAScratchFolderThatIsTheLiveMsysTmp(t *testing.T) {
	tests := []struct {
		name       string
		liveTemps  func(scratch string) ([]home.LiveTemp, error)
		isKept     bool
		wantOutput string
	}{
		{name: "a running Git Bash has it as /tmp", liveTemps: func(scratch string) ([]home.LiveTemp, error) {
			return []home.LiveTemp{{Runtime: `C:\Program Files\Git\usr\bin`, Folder: scratch}}, nil
		}, isKept: true, wantOutput: `is /tmp for every shell of the Git Bash in C:\Program Files\Git\usr\bin`},
		{name: "a running Git Bash has another folder as /tmp", liveTemps: func(string) ([]home.LiveTemp, error) {
			return []home.LiveTemp{{Runtime: `C:\Program Files\Git\usr\bin`, Folder: `C:\Users\someone\AppData\Local\Temp`}}, nil
		}},
		{name: "no Git Bash runs", liveTemps: func(string) ([]home.LiveTemp, error) { return nil, nil }},
		{name: "a running Git Bash could not be asked", liveTemps: func(string) ([]home.LiveTemp, error) {
			return nil, errors.New("cygpath.exe is missing")
		}, isKept: true, wantOutput: "cygpath.exe is missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			task := newHomeTask(t)
			liveTemps = func(context.Context, string) ([]home.LiveTemp, error) { return test.liveTemps(task.scratch) }
			t.Cleanup(func() { liveTemps = home.LiveTemps })

			// Act
			result, err := task.service.Cleanup(context.Background(), "g1")

			// Assert: the task is retired either way, and only its scratch
			// folder waits.
			if err != nil {
				t.Fatalf("Cleanup: %v", err)
			}
			if _, err := state.ReadTaskMeta(task.stateDir, "g1"); err == nil {
				t.Error("the task's record survived its cleanup")
			}
			if exists(t, task.worktree) {
				t.Error("the task's worktree survived its cleanup")
			}
			if got := exists(t, task.scratch); got != test.isKept {
				t.Errorf("the scratch folder is there: %v, want %v\n%s", got, test.isKept, result.Output)
			}
			if test.isKept && (!strings.Contains(result.Output, test.wantOutput) || !strings.Contains(result.Output, "left the scratch folder "+task.scratch+" in place")) {
				t.Errorf("Output = %q, want it to say the scratch folder was left in place and why (%s)", result.Output, test.wantOutput)
			}
			if !test.isKept && strings.Contains(result.Output, "/tmp") {
				t.Errorf("Output = %q, want nothing about /tmp for a folder it removed", result.Output)
			}
		})
	}
}
