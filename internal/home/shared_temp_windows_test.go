package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

func TestProcessImagesListsThisProcess(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	images, err := processImages()

	if err != nil {
		t.Fatal(err)
	}
	for _, image := range images {
		if strings.EqualFold(filepath.Base(image), filepath.Base(self)) && fsx.SamePath(image, self) {
			return
		}
	}
	t.Fatalf("%d running programs listed, none of them this test's own %s", len(images), self)
}

// A cygpath that turns out to be its runtime's first process makes its TMP
// the /tmp of every shell that starts while it runs, so the folder it is
// given is the user's own temporary folder as Windows names it, not what the
// environment of the process that asks says, which for a goblin started
// before TMP named the shared folder is its scratch folder.
func TestTheRuntimesAreAskedFromTheUsersOwnTemporaryFolder(t *testing.T) {
	scratch := filepath.Join(t.TempDir(), "scratch", "task-1")
	for _, name := range []string{"TMP", "TEMP", "LOCALAPPDATA"} {
		t.Setenv(name, scratch)
	}

	temp, err := userTemp()

	if err != nil {
		t.Fatal(err)
	}
	if rel, relErr := filepath.Rel(scratch, temp); relErr == nil && filepath.IsLocal(rel) {
		t.Fatalf("userTemp() = %s, which follows the environment into the task's scratch folder %s", temp, scratch)
	}
	if info, statErr := os.Stat(temp); statErr != nil || !info.IsDir() || !strings.EqualFold(filepath.Base(temp), "Temp") {
		t.Errorf("userTemp() = %s, stat %v, want the user's existing Temp folder", temp, statErr)
	}
}
