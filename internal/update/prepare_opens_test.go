package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// updatedOnce is a home an update already ran in once, from build installed
// to build first, which holds what that update left: its copy of the
// candidate and its backups of the build before.
func updatedOnce(t *testing.T, installed, first string) (root, stateDir string) {
	t.Helper()
	root = t.TempDir()
	stateDir = filepath.Join(root, "state")
	for _, name := range Aliases {
		if err := os.WriteFile(filepath.Join(root, name), []byte(installed), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	journal, err := Prepare(root, stateDir, candidateBuild(t, first))
	if err != nil {
		t.Fatal(err)
	}
	if err := Swap(journal); err != nil {
		t.Fatal(err)
	}
	CleanUp(journal)
	return root, stateDir
}

// candidateBuild is a candidate that holds content, in a folder of its own.
func candidateBuild(t *testing.T, content string) string {
	t.Helper()
	candidate := filepath.Join(t.TempDir(), "cfo.exe")
	if err := os.WriteFile(candidate, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return candidate
}

// What prepare opens decides how long it takes. A virus scanner reads a
// program whole the first time it is opened after it was written, and that
// open waits for it: on 2026-10-10 the first open of a 12 MB program just
// written took 2.7 to 6.0 s, where writing, flushing and renaming it took
// under half a second together and random bytes of that size opened at once.
// Prepare opened thirteen files. Among them were the copy of the candidate it
// had just written, to stage it, and the backups the last update wrote and
// nothing had opened since, to see whether they could be kept. A 31 MB build
// took 45 to 57 s to prepare, against a bound of one minute. Now it opens
// three, the candidate and each installed program, which have run and so were
// scanned long before.
func TestPrepareOpensOnlyTheCandidateAndTheInstalledPrograms(t *testing.T) {
	// Arrange
	root, stateDir := updatedOnce(t, "the build before", "the build installed now")
	candidate := candidateBuild(t, "the candidate of this update")
	opened := fsx.Opens()

	// Act
	journal, err := Prepare(root, stateDir, candidate)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := fsx.Opens() - opened; got != int64(1+len(Aliases)) {
		t.Errorf("prepare opened %d files, want %d: the candidate and each installed program", got, 1+len(Aliases))
	}
	holds(t, journal.Copy, "the candidate of this update")
	for _, alias := range journal.Aliases {
		holds(t, alias.Backup, "the build installed now")
		holds(t, staged(root, alias.Name), "the candidate of this update")
		holds(t, filepath.Join(root, alias.Name), "the build installed now")
	}
}

// A copy the last update left is told from this update's by its size before
// anything else, so one of the very same size is still read, and replaced
// when it holds another build.
func TestPrepareReplacesACopyOfTheSameSizeThatHoldsAnotherBuild(t *testing.T) {
	// Arrange
	root, stateDir := updatedOnce(t, "build one", "build two")
	candidate := candidateBuild(t, "build six")

	// Act
	journal, err := Prepare(root, stateDir, candidate)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	holds(t, journal.Copy, "build six")
	for _, alias := range journal.Aliases {
		holds(t, alias.Backup, "build two")
		holds(t, staged(root, alias.Name), "build six")
	}
}

// An update tried again with the same candidate, as after one that stopped
// before it swapped, writes nothing it already holds: every copy is held open
// here as a reader that never lets go holds it, which refuses a replace, and
// prepare still ends well.
func TestPrepareAgainWritesNothingItAlreadyHolds(t *testing.T) {
	// Arrange
	root, stateDir, candidate := installedHome(t)
	first, err := Prepare(root, stateDir, candidate)
	if err != nil {
		t.Fatal(err)
	}
	copies := []string{first.Copy}
	for _, alias := range first.Aliases {
		copies = append(copies, alias.Backup, staged(root, alias.Name))
	}
	for _, path := range copies {
		reader, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
	}

	// Act
	_, err = Prepare(root, stateDir, candidate)

	// Assert
	if err != nil {
		t.Fatalf("prepare again with every copy held open: %v", err)
	}
}
