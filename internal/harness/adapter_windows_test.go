package harness

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// msysTemp is the folder a Git Bash started with env makes /tmp for every
// shell of the user when it is the first to start: its msys runtime asks
// Windows for the temporary folder, which is TMP, else TEMP.
func msysTemp(env map[string]string) string {
	if env["TMP"] != "" {
		return env["TMP"]
	}
	return env["TEMP"]
}

// On 2026-10-09 the first Git Bash after a restart was a goblin's, whose TMP
// was its scratch folder, so that folder was /tmp for every Git Bash on the
// machine, and cleaning the task up took /tmp from all of them.
func TestLaunchDoesNotMakeTheScratchFolderTheMsysTempFolder(t *testing.T) {
	const scratch = `C:\home\scratch\task-1`
	for kind, adapter := range DefaultRegistry().Adapters {
		// Arrange
		if kind == Pi {
			runner := &fakeRunner{run: func(execx.Request) (execx.Result, error) {
				return execx.Result{Stdout: []byte("  --tui-mode <mode>  TUI mode\n")}, nil
			}}
			if err := adapter.Validate(context.Background(), runner); err != nil {
				t.Fatalf("Validate(pi): %v", err)
			}
		}

		// Act
		launch, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task-1`, Scratch: scratch})

		// Assert
		if err != nil {
			t.Fatalf("Build(%s): %v", kind, err)
		}
		temp := msysTemp(launch.Env)
		if rel, err := filepath.Rel(scratch, temp); temp == "" || err == nil && filepath.IsLocal(rel) {
			t.Errorf("%s: a Git Bash started in this terminal makes %q /tmp, the task's scratch folder %s or a folder in it, which goes when the task is cleaned up", kind, temp, scratch)
		}
		if want := `C:\home\scratch\.tmp`; temp != want {
			t.Errorf("%s: the msys temporary folder is %q, want the one folder every goblin shares, %s", kind, temp, want)
		}
		// The task's own build output, test homes and logs still go with it.
		for _, name := range []string{"GOTMPDIR", "TEMP", "TMPDIR"} {
			if launch.Env[name] != scratch {
				t.Errorf("%s: %s = %q, want the task's own scratch folder %s", kind, name, launch.Env[name], scratch)
			}
		}
	}
}
