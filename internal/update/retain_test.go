package update

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// builds counts the distinct builds of cfo among the files in bin, its
// aliases and the copies moved aside.
func builds(t *testing.T, bin string) (map[string]bool, int) {
	t.Helper()
	entries, err := os.ReadDir(bin)
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[string]bool{}
	files := 0
	for _, entry := range entries {
		name := entry.Name()
		if name != "cfo.exe" && name != "goblins.exe" && !asideCopy.MatchString(name) {
			continue
		}
		files++
		hash, err := HashFile(filepath.Join(bin, name))
		if err != nil {
			t.Fatal(err)
		}
		hashes[hash] = true
	}
	return hashes, files
}

// Every update moves the build it replaces aside, and a copy something still
// runs, such as a goblin's terminal host, outlives the update's own clean-up:
// 122 of them once filled a home's root. The Overlord, 2026-10-08: "no
// redudant exe files". Whatever number of updates runs, bin keeps the current
// build alone, under its two names, and the last update's rollback still puts
// the build it replaced back from the update's own verified copies.
func TestBinKeepsTheCurrentBuildAloneAfterAnyNumberOfUpdates(t *testing.T) {
	// Arrange
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	stateDir := filepath.Join(home, "state")
	for _, dir := range []string{bin, stateDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range Aliases {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("build 0"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var journal *Journal
	for i := 1; i <= 6; i++ {
		candidate := filepath.Join(t.TempDir(), "cfo.exe")
		if err := os.WriteFile(candidate, []byte(fmt.Sprintf("build %d", i)), 0o755); err != nil {
			t.Fatal(err)
		}

		// Act: an update whose clean-up could not remove the copies it moved
		// aside, because something still ran them, then the pass that
		// removes them once nothing does.
		var err error
		if journal, err = Prepare(bin, stateDir, candidate); err != nil {
			t.Fatalf("update %d: Prepare: %v", i, err)
		}
		if err := Swap(journal); err != nil {
			t.Fatalf("update %d: Swap: %v", i, err)
		}
		if err := Record(stateDir, journal, Done, "serves"); err != nil {
			t.Fatal(err)
		}
		RemoveAsideCopies(bin)

		// Assert
		if hashes, files := builds(t, bin); len(hashes) != 1 || files != len(Aliases) {
			t.Fatalf("after update %d bin holds %d builds in %d files, want the current build alone in its %d aliases", i, len(hashes), files, len(Aliases))
		}
	}

	// The rollback restores from the update's verified copies, which the
	// removal never touches.
	if err := Restore(journal); err != nil {
		t.Fatalf("Restore after the removal: %v", err)
	}
	for _, name := range Aliases {
		if got, err := os.ReadFile(filepath.Join(bin, name)); err != nil || string(got) != "build 5" {
			t.Errorf("%s after the rollback = %q, %v; want build 5 back", name, got, err)
		}
	}
}

// Every copy of a program moved aside goes, whichever way it was named, a
// copy an older install moved again under a second .old among them, which
// once stayed in bin for good; what an update in progress staged and every
// other file stay.
func TestRemoveAsideCopiesRemovesEveryCopyMovedAsideAndNothingElse(t *testing.T) {
	// Arrange
	bin := t.TempDir()
	aside := []string{"cfo.exe.1791449158834959100.update-old", "goblins.exe.ZWLFUCC5NGUANWZOS4MPE2XO2L.old", "cfo.exe.OFXG3O7Z2WTAOETUQUBHOQCWTS.old.GHVTLRQU5WOWXKXQU6QWXUHANI.old", "goblins-window.exe.abc.old", "cfo.exe.held-66714dea"}
	kept := []string{"cfo.exe.update-new", "goblins.exe.update-restore", "cfo.exe", "goblins.exe", "goblins-window.exe", "notes.txt", "cfo.exe.bak"}
	for _, name := range append(append([]string{}, aside...), kept...) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	freed, left := RemoveAsideCopies(bin)

	// Assert
	for _, name := range aside {
		if _, err := os.Stat(filepath.Join(bin, name)); !os.IsNotExist(err) {
			t.Errorf("%s, a copy moved aside, survived: %v", name, err)
		}
	}
	for _, name := range kept {
		if _, err := os.Stat(filepath.Join(bin, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
	var want int64
	for _, name := range aside {
		want += int64(len(name))
	}
	if freed != want || len(left) != 0 {
		t.Errorf("RemoveAsideCopies = %d bytes freed, %v left; want %d and none", freed, left, want)
	}
}
