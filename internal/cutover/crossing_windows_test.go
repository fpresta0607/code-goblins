package cutover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
