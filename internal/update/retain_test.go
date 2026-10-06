package update

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
// 122 of them once filled a home's root. Whatever number of updates runs, bin
// keeps the current build and the two before it, and the last update's
// rollback still puts the build it replaced back.
func TestBinKeepsTheCurrentBuildAndTwoBeforeItAfterAnyNumberOfUpdates(t *testing.T) {
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
		// aside, because something still ran them.
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
		time.Sleep(20 * time.Millisecond)
		KeepRecent(bin, KeptBuilds)

		// Assert
		hashes, files := builds(t, bin)
		if len(hashes) > KeptBuilds+1 {
			t.Fatalf("after update %d bin holds %d builds in %d files, want at most %d", i, len(hashes), files, KeptBuilds+1)
		}
	}
	for _, kept := range []string{"build 5", "build 4"} {
		sum, err := hashOf(strings.NewReader(kept))
		if err != nil {
			t.Fatal(err)
		}
		if hashes, _ := builds(t, bin); !hashes[sum] {
			t.Errorf("bin no longer holds %s, one of the two builds before the current one", kept)
		}
	}

	// The rollback restores from the update's verified copies, which the
	// pruning never touches.
	if err := Restore(journal); err != nil {
		t.Fatalf("Restore after pruning: %v", err)
	}
	for _, name := range Aliases {
		if got, err := os.ReadFile(filepath.Join(bin, name)); err != nil || string(got) != "build 5" {
			t.Errorf("%s after the rollback = %q, %v; want build 5 back", name, got, err)
		}
	}
}

func TestKeepRecentLeavesWhatAnUpdateInProgressStaged(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"cfo.exe.update-new", "goblins.exe.update-restore", "cfo.exe", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	KeepRecent(bin, 0)

	for _, name := range []string{"cfo.exe.update-new", "goblins.exe.update-restore", "cfo.exe", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(bin, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
}
