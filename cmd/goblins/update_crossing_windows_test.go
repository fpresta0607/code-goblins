package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cutover"
	"github.com/fpresta0607/code-goblins/internal/cutover/cutovertest"
	"github.com/fpresta0607/code-goblins/internal/installbase"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// newPreRenameHome is an update home a release before the rename set up: its
// bin holds that release's program beside goblins.exe, both the previous
// build, and the user's settings name the home only by the old name.
func newPreRenameHome(t *testing.T, candidate string) *updateHome {
	t.Helper()
	u := newUpdateHome(t, "previous", candidate)
	writeBuild(t, filepath.Join(u.bin, cutovertest.Program), "previous")
	settings, err := json.Marshal(map[string]string{cutovertest.HomeSetting: u.root})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(u.root, standInSettingsFile), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	return u
}

// settings are the user's settings the stand-in builds' moves read and wrote.
func (u *updateHome) settings() map[string]string {
	u.t.Helper()
	values, err := standInSettings(filepath.Join(u.root, standInSettingsFile)).read()
	if err != nil {
		u.t.Fatal(err)
	}
	return values
}

// isStillPreRename checks the home is one from before the rename as it was:
// its old program the previous build, no record of a move, and the user's
// settings naming it only by the old name.
func (u *updateHome) isStillPreRename() {
	u.t.Helper()
	if !cutover.PreRename(u.root) {
		u.t.Fatal("the home no longer reads as one from before the rename")
	}
	if got, err := os.ReadFile(filepath.Join(u.bin, cutovertest.Program)); err != nil || !bytes.Equal(got, u.previous) {
		u.t.Fatalf("bin\\%s is not the previous build (%v)", cutovertest.Program, err)
	}
	if got := u.settings(); len(got) != 1 || got[cutovertest.HomeSetting] != u.root {
		u.t.Fatalf("the user's settings are %v, want only %s naming the home, as before", got, cutovertest.HomeSetting)
	}
}

// Every release up to v0.5.18 leaves the journal of its last update, which
// names its own program among two aliases. This build reads that journal as
// history once its update finished, so a home such a release updated before
// updates to goblins and moves to the new names in the same update.
func TestAnUpdateMovesAHomeWhoseLastUpdateABuildBeforeTheRenameMade(t *testing.T) {
	// Arrange
	u := newPreRenameHome(t, "candidate")
	journal, err := cutovertest.FinishedUpdate(u.bin, u.state, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(update.Dir(u.state), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(update.Dir(u.state), "journal.json"), journal, 0o600); err != nil {
		t.Fatal(err)
	}
	u.serving()
	candidate, err := os.ReadFile(u.candidate)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	code, output := u.run(nil)

	// Assert
	if code != updateInstalled {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateInstalled, output)
	}
	u.aliasesAre(candidate, "candidate")
	if cutover.PreRename(u.root) {
		t.Fatalf("the home still reads as one from before the rename:\n%s", output)
	}
	crossing, isCrossed, err := cutover.ReadCrossing(u.state)
	if err != nil || !isCrossed {
		t.Fatalf("the record of the move reads %v, %v", isCrossed, err)
	}
	if _, err := os.Stat(filepath.Join(crossing.Folder, cutover.UndoFile)); err != nil {
		t.Fatalf("the move's folder %s holds no way back: %v", crossing.Folder, err)
	}
	if got := u.settings(); got[installbase.HomeVariable] != u.root || len(got) != 1 {
		t.Fatalf("the user's settings are %v, want only %s naming the home", got, installbase.HomeVariable)
	}
	if !strings.Contains(output, "goblins cutover --undo '"+crossing.Folder+"'") {
		t.Fatalf("the update does not name the way back:\n%s", output)
	}
}

// A process ended part way through the move, as the update's bound ends one,
// leaves the journal naming the move's undo folder, so update --recover
// undoes what the move made before it puts the previous build back.
func TestARecoveryUndoesAMoveToGoblinsThatEndedPartWay(t *testing.T) {
	// Arrange
	u := newPreRenameHome(t, "candidate")
	u.serving()
	if code, output := u.run([]string{"GOBLINS_TEST_CROSSING_ENDS=1"}); code != 9 {
		t.Fatalf("the update did not end inside the move (exit %d):\n%s", code, output)
	}
	if got, err := os.ReadFile(filepath.Join(u.bin, cutovertest.Program)); err == nil && bytes.Equal(got, u.previous) {
		t.Fatal("the move ended before it changed the home's programs, so this test proves nothing")
	}
	journal, err := update.ReadJournal(u.state)
	if err != nil {
		t.Fatal(err)
	}
	u.candidate = journal.Copy

	// Act
	code, output := u.run(nil, "--recover")

	// Assert
	if code != updateRolledBack {
		t.Fatalf("recover exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	u.previousServes()
	u.isStillPreRename()
}

// A rollback hands the previous build, one from before the rename, the home
// by the old name it finds it by, whether the update failed before the move
// or after it, which the rollback undoes first; the home is not in the usual
// folder, so the build would not find it otherwise.
func TestARollbackHandsABuildFromBeforeTheRenameItsHomeByTheOldName(t *testing.T) {
	for name, testCase := range map[string]struct {
		candidate string
		seams     []string
	}{
		"the swap fails before the move": {candidate: "candidate", seams: []string{"GOBLINS_TEST_UPDATE_BREAK_SWAP=1"}},
		"the candidate does not start":   {candidate: "crash"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			u := newPreRenameHome(t, testCase.candidate)
			u.serving()
			handed := filepath.Join(u.state, oldHomeHandedFile)
			_ = os.Remove(handed)

			// Act
			code, output := u.run(testCase.seams)

			// Assert
			if code != updateRolledBack {
				t.Fatalf("update exited %d, want %d:\n%s", code, updateRolledBack, output)
			}
			u.previousServes()
			u.isStillPreRename()
			if got, err := os.ReadFile(handed); err != nil || !strings.EqualFold(filepath.Clean(string(got)), filepath.Clean(u.root)) {
				t.Fatalf("the previous build was handed the home %q (%v), want %s by the old name", got, err, u.root)
			}
		})
	}
}
