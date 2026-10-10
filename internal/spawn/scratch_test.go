package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
)

// liveTempAt makes folder the /tmp of a running Git Bash for the test.
func liveTempAt(t *testing.T, folder string) {
	t.Helper()
	liveTemps = func(context.Context) ([]home.LiveTemp, error) {
		return []home.LiveTemp{{Runtime: `C:\Git\usr\bin`, Folder: folder}}, nil
	}
	t.Cleanup(func() { liveTemps = home.LiveTemps })
}

// A spawn that fails after its harness started a Git Bash may have made its
// scratch folder /tmp for every shell of the user: its teardown leaves the
// folder and says so, and removes the scratch folder of a spawn that did not.
func TestTeardownLeavesAScratchFolderThatIsALiveTmp(t *testing.T) {
	for name, isLiveTmp := range map[string]bool{"a running Git Bash has it as /tmp": true, "no Git Bash has it as /tmp": false} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)
			scratch := makeDir(t, filepath.Join(filepath.Dir(f.stateDir), "scratch", "task-7"))
			writeFile(t, filepath.Join(makeDir(t, filepath.Join(scratch, "go-build1")), "main.o"), "object")
			writeFile(t, filepath.Join(f.stateDir, "task-7.meta"), "{}")
			if isLiveTmp {
				liveTempAt(t, scratch)
			}

			// Act
			err := f.service.teardownLaunch(context.Background(), host.Record{}, f.project, f.worktree, scratch, "task-7")

			// Assert
			_, statErr := os.Stat(scratch)
			if isLiveTmp {
				if statErr != nil {
					t.Errorf("the scratch folder that is a live /tmp was removed: %v", statErr)
				}
				if err == nil || !strings.Contains(err.Error(), scratch) || !strings.Contains(err.Error(), "/tmp") {
					t.Errorf("teardown = %v, want it to say the folder %s was left as a live /tmp", err, scratch)
				}
				return
			}
			if err != nil || !os.IsNotExist(statErr) {
				t.Errorf("teardown = %v, scratch folder stat %v; want the folder removed", err, statErr)
			}
		})
	}
}

func TestCreateScratchMakesTheSharedFolderBesideIt(t *testing.T) {
	scratch := filepath.Join(t.TempDir(), "scratch", "task-7")

	if err := createScratch(scratch); err != nil {
		t.Fatal(err)
	}

	for _, folder := range []string{scratch, filepath.Join(filepath.Dir(scratch), home.SharedTempDir)} {
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			t.Errorf("stat %q = %v, want the folder", folder, err)
		}
	}
}

// A project's manifest cannot move the variables that decide where a goblin's
// temporary files go, TMP above all: Git Bash makes it /tmp.
func TestAManifestCannotRedirectTheTemporaryFolders(t *testing.T) {
	for _, name := range []string{"TMP", "TEMP", "TMPDIR", "GOTMPDIR", "tmp", "TmpDir"} {
		env := map[string]string{}

		mergeProvisionEnv(env, map[string]string{name: `C:\project\tmp`})

		if len(env) != 0 {
			t.Errorf("a manifest set %s = %q in the launch", name, env[name])
		}
	}
}
