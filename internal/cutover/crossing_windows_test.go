package cutover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cutover/cutovertest"
)

var crossedAt = time.Date(2026, 11, 12, 5, 0, 0, 0, time.UTC)

func TestACrossingKeepsTheOldProgramsAsThisBuildAndLeavesTheLabels(t *testing.T) {
	// Arrange
	f := newFixture(t)
	root := f.machine.Root
	labels := strings.Join(f.labels["owner/repo"], ",")

	// Act
	plan, err := f.machine.PlanCrossing(crossedAt)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := f.machine.Apply(plan, crossedAt)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		oldName + ".exe":           "this build",
		oldName + "-hook.exe":      "this build's hook program",
		newCommand + ".exe":        "this build",
		newCommand + "-hook.exe":   "this build's hook program",
		newCommand + "-window.exe": "this build's window",
	} {
		if got := read(t, filepath.Join(root, "bin", name)); got != want {
			t.Errorf("bin\\%s is %q, want %q", name, got, want)
		}
	}
	if got := strings.Join(f.labels["owner/repo"], ","); got != labels {
		t.Errorf("the labels are %s, want them left for the supervisor as %s", got, labels)
	}
	crossing, isCrossed, err := ReadCrossing(filepath.Join(root, "state"))
	if err != nil || !isCrossed {
		t.Fatalf("the crossing's record reads %v, %v", isCrossed, err)
	}
	wantBridges := []string{filepath.Join(root, "bin", oldName+".exe"), filepath.Join(root, "bin", oldName+"-hook.exe")}
	if crossing.Folder != folder || !crossing.At.Equal(crossedAt) || strings.Join(crossing.Bridges, ",") != strings.Join(wantBridges, ",") || crossing.IsRelabelled {
		t.Errorf("the record is %+v, want the folder %s, the time %s and the bridges %v", crossing, folder, crossedAt, wantBridges)
	}
	if PreRename(root) {
		t.Errorf("the home still reads as one from before the rename")
	}
	if value, _, _ := f.env.Get(setting + "HOME"); value == "" {
		t.Errorf("the home setting was not moved to %sHOME", setting)
	}
}

func TestACrossingTakesTheHookProgramAnOldUpdaterDownloadedUnderTheOldName(t *testing.T) {
	// Arrange
	f := newFixture(t)
	build := filepath.Dir(f.machine.Program)
	if err := os.Rename(filepath.Join(build, newCommand+"-hook.exe"), filepath.Join(build, oldName+"-hook.exe")); err != nil {
		t.Fatal(err)
	}

	// Act
	plan, err := f.machine.PlanCrossing(crossedAt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.machine.Apply(plan, crossedAt)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{newCommand + "-hook.exe", oldName + "-hook.exe"} {
		if got := read(t, filepath.Join(f.machine.Root, "bin", name)); got != "this build's hook program" {
			t.Errorf("bin\\%s is %q, want this build's hook program", name, got)
		}
	}
}

func TestTheUndoOfACrossingAndItsLabelsRestoresTheMachineExactly(t *testing.T) {
	// Arrange
	f := newFixture(t)
	before := f.snapshot(t)
	plan, err := f.machine.PlanCrossing(crossedAt)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := f.machine.Apply(plan, crossedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.machine.Relabel(folder); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.labels["owner/repo"], ","); !strings.Contains(got, newName+": running") {
		t.Fatalf("the labels are %s after the supervisor renamed them", got)
	}

	// Act
	err = f.machine.Undo(folder)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if found := before.differences(f.snapshot(t)); len(found) > 0 {
		t.Fatalf("the undo left the machine changed:\n%s", strings.Join(found, "\n"))
	}
	if !PreRename(f.machine.Root) {
		t.Errorf("the home undone does not read as one from before the rename")
	}
}

func TestRemoveBridgesRemovesTheOldProgramsAndTheirSetAsideCopies(t *testing.T) {
	// Arrange
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	bridge := filepath.Join(bin, oldName+".exe")
	write(t, bridge, "this build")
	write(t, filepath.Join(bin, oldName+".exe.1794.update-old"), "the old build")
	write(t, filepath.Join(bin, newCommand+".exe"), "this build")

	// Act
	err := RemoveBridges(root, []string{bridge})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(bin)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != newCommand+".exe" {
		t.Fatalf("bin holds %v, want only %s.exe", entries, newCommand)
	}
}

// The update journal a release before the rename left is history once its
// update finished, and only then: an unfinished one is that release's way
// back, and this build's own journal is read as this build reads it.
func TestFinishedOldUpdateIsOnlyAFinishedJournalOfABuildBeforeTheRename(t *testing.T) {
	for name, testCase := range map[string]struct {
		journal func(bin, state string) []byte
		want    bool
	}{
		"none": {journal: nil, want: false},
		"an old build's, done": {journal: func(bin, state string) []byte {
			return oldJournal(t, bin, state, "done")
		}, want: true},
		"an old build's, rolled back": {journal: func(bin, state string) []byte {
			return oldJournal(t, bin, state, "rolled-back")
		}, want: true},
		"an old build's, stopped at the swap": {journal: func(bin, state string) []byte {
			return oldJournal(t, bin, state, "swapped")
		}, want: false},
		"this build's, done": {journal: func(bin, state string) []byte {
			return []byte(strings.Replace(string(oldJournal(t, bin, state, "done")), oldName+"-update.v1", newName+"-update.v1", 1))
		}, want: false},
		"unreadable": {journal: func(string, string) []byte { return []byte("not json") }, want: false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			state := filepath.Join(root, "state")
			if testCase.journal != nil {
				write(t, filepath.Join(state, "update", "journal.json"), string(testCase.journal(filepath.Join(root, "bin"), state)))
			}

			// Act
			got := FinishedOldUpdate(state)

			// Assert
			if got != testCase.want {
				t.Fatalf("FinishedOldUpdate is %v, want %v", got, testCase.want)
			}
		})
	}
}

// oldJournal is the journal a release before the rename wrote, at phase.
func oldJournal(t *testing.T, bin, state, phase string) []byte {
	t.Helper()
	data, err := cutovertest.FinishedUpdate(bin, state, crossedAt)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.Replace(string(data), `"phase": "done"`, `"phase": "`+phase+`"`, 1))
}

// A process that ends while it writes a program in the home's bin has set
// the program there aside and not yet written its replacement, and its
// record does not count that step as made. Its undo puts the program back
// all the same, with everything else.
func TestTheUndoOfACrossingThatEndedAsItWroteAProgramPutsTheProgramBack(t *testing.T) {
	// Arrange
	f := newFixture(t)
	before := f.snapshot(t)
	plan, err := f.machine.PlanCrossing(crossedAt)
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(f.machine.Root, "bin", oldName+".exe")
	ended := slices.IndexFunc(plan.Changes, func(change Change) bool { return change.Steps[0].Key == program })
	if ended < 0 {
		t.Fatalf("the crossing writes no %s", program)
	}
	folder, err := f.machine.Apply(Plan{Changes: plan.Changes[:ended+1]}, crossedAt)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(folder, UndoFile))), &record); err != nil {
		t.Fatal(err)
	}
	record["done"] = record["done"].(float64) - 1
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(folder, UndoFile), string(data))
	if err := os.Remove(program); err != nil {
		t.Fatal(err)
	}

	// Act
	err = f.machine.Undo(folder)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if found := before.differences(f.snapshot(t)); len(found) > 0 {
		t.Fatalf("the undo left the machine changed:\n%s", strings.Join(found, "\n"))
	}
}

// A program in the home's bin that cannot be set aside, as one a scanner
// holds open, fails the crossing at its step, before that step changed
// anything. The crossing then undoes every step it made before it, and the
// program's own step, which changed nothing, does not stop that undo.
func TestACrossingThatCannotSetAProgramAsideUndoesEverythingBeforeIt(t *testing.T) {
	// Arrange
	f := newFixture(t)
	before := f.snapshot(t)
	plan, err := f.machine.PlanCrossing(crossedAt)
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(f.machine.Root, "bin", oldName+".exe")
	name, err := syscall.UTF16PtrFromString(program)
	if err != nil {
		t.Fatal(err)
	}
	held, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(held)

	// Act
	folder, err := f.machine.Apply(plan, crossedAt)

	// Assert
	if err == nil || folder != "" {
		t.Fatalf("the crossing returned %q, %v; want it failed and undone", folder, err)
	}
	if found := before.differences(f.snapshot(t)); len(found) > 0 {
		t.Fatalf("the failed crossing left the machine changed:\n%s", strings.Join(found, "\n"))
	}
}
