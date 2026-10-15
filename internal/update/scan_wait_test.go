package update

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// On 2026-10-15 an update rolled back twice in a row, a minute each time,
// while the machine's virus scanner engine hung. Prepare wrote the staged
// copies of the candidate and never opened them, so the first open of each
// came in the swap, after the supervisor stopped, and waited for the hung
// scanner until the update's bound ended it with the board down (#922).
// Prepare reads each staged copy once instead, so the scanner's wait comes
// while the board still serves, and an update the scanner holds past its
// bound ends with nothing changed.
func TestPrepareWaitsForTheScannerOnEachStagedCopyBeforeItIsPrepared(t *testing.T) {
	// Arrange
	root, stateDir, candidate := installedHome(t)
	var read []string
	var preparedAtRead []bool
	previous := readStaged
	readStaged = func(path string) ([]byte, error) {
		read = append(read, path)
		journal, err := ReadJournal(stateDir)
		preparedAtRead = append(preparedAtRead, err == nil && journal.Phase == Prepared)
		return previous(path)
	}
	t.Cleanup(func() { readStaged = previous })

	// Act
	journal, err := Prepare(root, stateDir, candidate)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, name := range Aliases {
		want = append(want, staged(root, name))
	}
	if !slices.Equal(read, want) {
		t.Fatalf("prepare read the staged copies %v, want each once: %v", read, want)
	}
	if slices.Contains(preparedAtRead, true) {
		t.Fatalf("the journal said prepared at a staged read (%v), want every read before it", preparedAtRead)
	}
	if journal.Phase != Prepared {
		t.Fatalf("phase = %s, want prepared once the reads are done", journal.Phase)
	}
}

// A staged copy that reads back as other bytes than the candidate's is no
// copy to swap in, so prepare stops before the journal says prepared.
func TestPrepareRefusesAStagedCopyThatReadsBackAsAnotherBuild(t *testing.T) {
	// Arrange
	root, stateDir, candidate := installedHome(t)
	previous := readStaged
	readStaged = func(string) ([]byte, error) { return []byte("another build"), nil }
	t.Cleanup(func() { readStaged = previous })

	// Act
	_, err := Prepare(root, stateDir, candidate)

	// Assert
	if err == nil || !strings.Contains(err.Error(), filepath.Base(staged(root, Aliases[0]))) {
		t.Fatalf("err = %v, want the staged copy named", err)
	}
	if journal, readErr := ReadJournal(stateDir); readErr == nil && journal.Phase == Prepared {
		t.Fatal("the journal says prepared over a staged copy that is not the candidate")
	} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatal(readErr)
	}
}
