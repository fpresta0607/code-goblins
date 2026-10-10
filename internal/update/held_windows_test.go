package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// heldFor opens path with open and keeps it open for half a second, as a
// reader that is done in a moment does. The channel it returns closes once
// the file is let go.
func heldFor(t *testing.T, open func(string) (*os.File, error), path string) chan struct{} {
	t.Helper()
	reader, err := open(path)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(500 * time.Millisecond)
		reader.Close()
		close(released)
	}()
	return released
}

// Someone has the journal open at the moment the update records its next
// step. The update that bounds the work reads it every quarter second until
// it says prepared, the janitor reads it on its sweeps, and a virus scanner
// reads a file it just saw. Windows replaces no file while anyone has it
// open, even a reader that shares it for deletion, and the journal got one
// try. On 2026-10-10 an Update on main rolled back right after it stopped the
// supervisor, saying only "Access is denied.", and another was refused before
// it changed anything. The record waits the reader out.
func TestRecordWaitsOutAReaderThatHasTheJournalOpen(t *testing.T) {
	for name, open := range map[string]func(string) (*os.File, error){
		"the update that bounds the work, which reads it as every fleet program does": fsx.Open,
		"a virus scanner, which does not share it for deletion":                       os.Open,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			root, stateDir, candidate := installedHome(t)
			journal, err := Prepare(root, stateDir, candidate)
			if err != nil {
				t.Fatal(err)
			}
			released := heldFor(t, open, journalPath(stateDir))

			// Act
			err = Record(stateDir, journal, Stopped, "")
			<-released

			// Assert
			if err != nil {
				t.Fatalf("Record while a reader had the journal open for half a second: %v", err)
			}
			if recorded, err := ReadJournal(stateDir); err != nil || recorded.Phase != Stopped {
				t.Errorf("the journal says %q (%v), want %q", recorded.Phase, err, Stopped)
			}
		})
	}
}

// The wait is bounded, and a journal that stays held is named: the error
// that ends the update says which file was refused, where "Access is denied."
// alone left it to be guessed. The journal keeps what it said, and no
// half-written copy stays beside it.
func TestRecordNamesAJournalAReaderNeverLetsGo(t *testing.T) {
	// Arrange
	root, stateDir, candidate := installedHome(t)
	journal, err := Prepare(root, stateDir, candidate)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(journalPath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	began := time.Now()

	// Act
	err = Record(stateDir, journal, Stopped, "")

	// Assert
	if err == nil {
		t.Fatal("Record replaced a journal a reader never let go of")
	}
	if !strings.Contains(err.Error(), journalPath(stateDir)) {
		t.Errorf("the error %q does not name %s", err, journalPath(stateDir))
	}
	if took := time.Since(began); took > 30*time.Second {
		t.Errorf("Record took %s to give up, so the wait is not bounded", took)
	}
	if recorded, err := ReadJournal(stateDir); err != nil || recorded.Phase != Prepared {
		t.Errorf("the journal says %q (%v), want it left at %q", recorded.Phase, err, Prepared)
	}
	if left, _ := filepath.Glob(journalPath(stateDir) + ".*"); len(left) != 0 {
		t.Errorf("the refused record left %v beside the journal", left)
	}
}

// The swap comes right after the supervisor stopped, when a virus scanner
// can still be reading the build it ran from, or the candidate staged beside
// it. A scanner's read does not share a file for deletion, so the rename is
// refused while it lasts, and the one try each rename got rolled the update
// back. The swap waits the scanner out.
func TestSwapWaitsOutAScannerThatHoldsABuild(t *testing.T) {
	for name, held := range map[string]func(root string) string{
		"the installed build it moves aside":    func(root string) string { return filepath.Join(root, "cfo.exe") },
		"the staged candidate it puts in place": func(root string) string { return staged(root, "goblins.exe") },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			root, stateDir, candidate := installedHome(t)
			journal, err := Prepare(root, stateDir, candidate)
			if err != nil {
				t.Fatal(err)
			}
			released := heldFor(t, os.Open, held(root))

			// Act
			err = Swap(journal)
			<-released

			// Assert
			if err != nil {
				t.Fatalf("Swap while a scanner held a build for half a second: %v", err)
			}
			for _, alias := range Aliases {
				holds(t, filepath.Join(root, alias), "candidate build")
			}
		})
	}
}

// The way back waits the same way: a scanner still reading the candidate the
// update just put in place does not stop the previous build from being put
// back over it.
func TestRestoreWaitsOutAScannerThatHoldsABuild(t *testing.T) {
	// Arrange
	root, stateDir, candidate := installedHome(t)
	journal, err := Prepare(root, stateDir, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := Swap(journal); err != nil {
		t.Fatal(err)
	}
	released := heldFor(t, os.Open, filepath.Join(root, "goblins.exe"))

	// Act
	err = Restore(journal)
	<-released

	// Assert
	if err != nil {
		t.Fatalf("Restore while a scanner held a build for half a second: %v", err)
	}
	for _, alias := range Aliases {
		holds(t, filepath.Join(root, alias), "previous build")
	}
}
