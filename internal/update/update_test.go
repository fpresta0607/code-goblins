package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// installedHome is a home whose aliases hold build "previous", with a
// candidate build beside it, as a verified candidate waits to be installed.
func installedHome(t *testing.T) (root, stateDir, candidate string) {
	t.Helper()
	root = t.TempDir()
	stateDir = filepath.Join(root, "state")
	for _, name := range Aliases {
		if err := os.WriteFile(filepath.Join(root, name), []byte("previous build"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	candidate = filepath.Join(root, "cfo.exe.held-candidate")
	if err := os.WriteFile(candidate, []byte("candidate build"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, stateDir, candidate
}

func holds(t *testing.T, path, content string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != content {
		t.Fatalf("%s holds %q, want %q", path, data, content)
	}
}

// Before anything live changes the journal names the candidate, a copy of
// it, and verified copies of the build installed, so the way back never
// depends on the aliases themselves.
func TestPrepareRecordsTheWayBackBeforeAnythingLiveChanges(t *testing.T) {
	root, stateDir, candidate := installedHome(t)

	journal, err := Prepare(root, stateDir, candidate)

	if err != nil {
		t.Fatal(err)
	}
	recorded, err := ReadJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Phase != Prepared || recorded.Candidate != candidate || len(recorded.Aliases) != len(Aliases) {
		t.Fatalf("journal = %+v, want the prepared update of both aliases", recorded)
	}
	holds(t, recorded.Copy, "candidate build")
	for _, alias := range journal.Aliases {
		holds(t, alias.Backup, "previous build")
		holds(t, filepath.Join(root, alias.Name), "previous build")
	}
}

func TestSwapPutsTheCandidateUnderEveryAlias(t *testing.T) {
	root, stateDir, candidate := installedHome(t)
	journal, err := Prepare(root, stateDir, candidate)
	if err != nil {
		t.Fatal(err)
	}

	if err := Swap(journal); err != nil {
		t.Fatal(err)
	}

	for _, name := range Aliases {
		holds(t, filepath.Join(root, name), "candidate build")
	}
}

// Two renames are not one atomic step. An update that ended between them,
// or between moving an alias aside and putting the candidate in its place,
// leaves aliases that disagree or are missing; restoring puts the previous
// build back under every one from the verified copies, which it keeps.
func TestRestoreAfterASwapThatStoppedPartWay(t *testing.T) {
	for name, interrupt := range map[string]func(t *testing.T, root string){
		"between the two aliases": func(t *testing.T, root string) {
			if err := moveAside(filepath.Join(root, "cfo.exe"), &Alias{}); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(staged(root, "cfo.exe"), filepath.Join(root, "cfo.exe")); err != nil {
				t.Fatal(err)
			}
		},
		"with an alias moved aside and not yet replaced": func(t *testing.T, root string) {
			if err := moveAside(filepath.Join(root, "goblins.exe"), &Alias{}); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root, stateDir, candidate := installedHome(t)
			journal, err := Prepare(root, stateDir, candidate)
			if err != nil {
				t.Fatal(err)
			}
			interrupt(t, root)
			recorded, err := ReadJournal(stateDir)
			if err != nil {
				t.Fatal(err)
			}

			if err := Restore(&recorded); err != nil {
				t.Fatal(err)
			}
			if err := Restore(&recorded); err != nil {
				t.Fatalf("a second restore failed: %v", err)
			}

			for _, alias := range journal.Aliases {
				holds(t, filepath.Join(root, alias.Name), "previous build")
				holds(t, alias.Backup, "previous build")
			}
		})
	}
}

// The counter-example from 2026-10-01: an error during the swap itself must
// still leave the way back. A swap that fails on the second alias returns
// the error, and the restore after it puts both back.
func TestASwapThatFailsPartWayIsRestored(t *testing.T) {
	root, stateDir, candidate := installedHome(t)
	journal, err := Prepare(root, stateDir, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(staged(root, "goblins.exe")); err != nil {
		t.Fatal(err)
	}

	if err := Swap(journal); err == nil {
		t.Fatal("a swap whose second candidate was gone reported success")
	}
	holds(t, filepath.Join(root, "cfo.exe"), "candidate build")

	if err := Restore(journal); err != nil {
		t.Fatal(err)
	}
	for _, name := range Aliases {
		holds(t, filepath.Join(root, name), "previous build")
	}
}

// A backup whose content no longer matches what was recorded is refused
// rather than installed: the way back puts back only the verified build.
func TestRestoreRefusesABackupThatChanged(t *testing.T) {
	root, stateDir, candidate := installedHome(t)
	journal, err := Prepare(root, stateDir, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := Swap(journal); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal.Aliases[0].Backup, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Restore(journal); err == nil {
		t.Fatal("a restore from a backup that changed reported success")
	}
	holds(t, filepath.Join(root, journal.Aliases[0].Name), "candidate build")
}

// A held build cannot be written, replaced or deleted, yet still starts, so
// what runs is what was hashed.
func TestAHeldBuildCannotChangeButStillRuns(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	build := filepath.Join(t.TempDir(), "previous-goblins.exe")
	if err := os.WriteFile(build, data, 0o755); err != nil {
		t.Fatal(err)
	}
	want, err := HashFile(build)
	if err != nil {
		t.Fatal(err)
	}

	held, hash, err := Hold(build)

	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if hash != want {
		t.Fatalf("held hash %s, want %s", hash, want)
	}
	if err := os.WriteFile(build, []byte("tampered"), 0o755); err == nil {
		t.Fatal("a held build was written")
	}
	if err := os.Rename(build, build+".moved"); err == nil {
		t.Fatal("a held build was moved")
	}
	if err := os.Remove(build); err == nil {
		t.Fatal("a held build was deleted")
	}
	if output, err := exec.Command(build, "-test.run=^$").CombinedOutput(); err != nil {
		t.Fatalf("a held build did not run: %v\n%s", err, output)
	}
}

func TestPrepareRefusesACandidateThatIsAlreadyInstalled(t *testing.T) {
	root, stateDir, _ := installedHome(t)
	if _, err := Prepare(root, stateDir, filepath.Join(root, "cfo.exe")); err == nil {
		t.Fatal("Prepare accepted the installed build as its own candidate")
	}
}

// Clean-up removes only what this update moved aside: an older build moved
// aside by an earlier install, or a file of the user's, stays.
func TestCleanUpRemovesOnlyWhatThisUpdateMovedAside(t *testing.T) {
	root, stateDir, candidate := installedHome(t)
	theirs := filepath.Join(root, "cfo.exe.1234.old")
	if err := os.WriteFile(theirs, []byte("an earlier install's build"), 0o755); err != nil {
		t.Fatal(err)
	}
	journal, err := Prepare(root, stateDir, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := Swap(journal); err != nil {
		t.Fatal(err)
	}
	moved := append([]string{}, journal.Aliases[0].Aside...)

	CleanUp(journal)

	holds(t, theirs, "an earlier install's build")
	for _, path := range moved {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s, which this update moved aside, is still there: %v", path, err)
		}
	}
}

// A journal names a file moved aside only as moveAside writes it, in this
// home's root under the alias's own name, so clean-up never removes a file a
// journal edited to name it.
func TestValidateAcceptsOnlyTheFilesAnUpdateMovesAside(t *testing.T) {
	root, stateDir, candidate := installedHome(t)
	journal, err := Prepare(root, stateDir, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := Swap(journal); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		aside string
		valid bool
	}{
		{"as moved aside", journal.Aliases[0].Aside[0], true},
		{"the root in other case", filepath.Join(strings.ToUpper(root), "cfo.exe.1.update-old"), true},
		{"outside the home", filepath.Join(t.TempDir(), "cfo.exe.1.update-old"), false},
		{"another alias's", filepath.Join(root, "goblins.exe.1.update-old"), false},
		{"no number", filepath.Join(root, "cfo.exe..update-old"), false},
		{"a word for the number", filepath.Join(root, "cfo.exe.abc.update-old"), false},
		{"an extra suffix", filepath.Join(root, "cfo.exe.1.update-old.txt"), false},
		{"an extra path element", root + `\state\..\cfo.exe.1.update-old`, false},
		{"in a folder of the root", filepath.Join(root, "state", "cfo.exe.1.update-old"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			edited := *journal
			edited.Aliases = append([]Alias{}, journal.Aliases...)
			edited.Aliases[0].Aside = []string{test.aside}

			err := Validate(edited, root, stateDir)

			if (err == nil) != test.valid {
				t.Fatalf("Validate with aside %s = %v, want valid %v", test.aside, err, test.valid)
			}
		})
	}
}
