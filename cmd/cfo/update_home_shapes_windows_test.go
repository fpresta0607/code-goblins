package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// newRootUpdateHome is a home a build before bin set up: its build at its
// root and no bin, with the candidate in a download folder of its own.
func newRootUpdateHome(t *testing.T, previous, candidate string) *updateHome {
	t.Helper()
	root := filepath.Join(t.TempDir(), "home")
	u := &updateHome{t: t, root: root, bin: filepath.Join(root, home.BinDir), programs: root, state: filepath.Join(root, "state"), started: map[*exec.Cmd]time.Time{}}
	if err := os.MkdirAll(u.state, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range update.Aliases {
		u.previous = writeBuild(t, filepath.Join(root, name), previous)
	}
	standin.RemoveAtCleanup(t, root)
	t.Cleanup(u.endAll)
	u.downloadCandidate(candidate)
	return u
}

// downloadCandidate puts the candidate build in a download folder beside the
// home, as the Overlord saves a release's cfo.exe, and returns its content.
func (u *updateHome) downloadCandidate(build string) []byte {
	u.t.Helper()
	download := filepath.Join(filepath.Dir(u.root), "download")
	if err := os.MkdirAll(download, 0o755); err != nil {
		u.t.Fatal(err)
	}
	standin.RemoveAtCleanup(u.t, download)
	u.candidate = filepath.Join(download, "cfo.exe")
	return writeBuild(u.t, u.candidate, build)
}

// finishedJournal leaves in the home's state the journal of a finished update
// of the programs in folder, made with the state folder state, shaped as the
// one the update that installed the Overlord's build left: done, its verified
// copies beside it, and the files it moved aside long gone.
func (u *updateHome) finishedJournal(folder, state string) {
	u.t.Helper()
	started := time.Date(2026, 10, 3, 2, 54, 4, 0, time.UTC)
	journal := update.Journal{Schema: "cfo-update.v1", Root: folder, Phase: update.Done, Candidate: `C:\dev\cg-install-build\8c10c45b\cfo.exe`,
		Copy: filepath.Join(update.Dir(state), "candidate.exe"), Hash: strings.Repeat("75", 32), Started: started, Updated: started.Add(7 * time.Second), Outcome: "the candidate serves",
		Attempts: []update.Attempt{{PID: 27216, Start: started.Add(6 * time.Second), Program: filepath.Join(folder, "goblins.exe")}}}
	for _, name := range update.Aliases {
		journal.Aliases = append(journal.Aliases, update.Alias{Name: name, Previous: strings.Repeat("28", 32), Backup: filepath.Join(update.Dir(state), "previous-"+name),
			Aside: []string{filepath.Join(folder, name+".1790996050558252600.update-old")}})
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		u.t.Fatal(err)
	}
	if err := os.MkdirAll(update.Dir(u.state), 0o700); err != nil {
		u.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(update.Dir(u.state), "journal.json"), data, 0o600); err != nil {
		u.t.Fatal(err)
	}
	for _, name := range []string{"candidate.exe", "previous-cfo.exe", "previous-goblins.exe"} {
		if err := os.WriteFile(filepath.Join(update.Dir(u.state), name), []byte("an earlier update's "+name), 0o600); err != nil {
			u.t.Fatal(err)
		}
	}
}

// servesWithNoConsoleWindow checks the supervisor serving the board was
// started as TestDetachedStartGivesAHiddenConsoleOfItsOwn requires: with a
// console of its own and no window, which Windows could otherwise show or
// hand to Windows Terminal as the default terminal, where closing it would
// end the supervisor.
func (u *updateHome) servesWithNoConsoleWindow() {
	u.t.Helper()
	record := u.awaitBoard()
	if console, err := os.ReadFile(filepath.Join(u.state, "test-serve-console-"+strconv.Itoa(record.PID))); err != nil || string(console) != "processes=1 window=false visible=false" {
		u.t.Errorf("the supervisor (pid %d) was not started with a console of its own and no window: %s (%v)", record.PID, console, err)
	}
}

// installedHere checks the update installed the candidate where the home
// keeps its build, its board serves it with no console window, only the
// board was restarted, and the journal it left is this update's.
func (u *updateHome) installedHere(code int, output string, candidate []byte, oldSupervisor, cfoHost *exec.Cmd) {
	u.t.Helper()
	if code != updateInstalled {
		u.t.Fatalf("update exited %d:\n%s", code, output)
	}
	u.aliasesAre(candidate, "candidate")
	if err := boardAlive(context.Background(), u.awaitBoard()); err != nil {
		u.t.Errorf("the candidate's supervisor does not answer as itself (%v):\n%s", err, output)
	}
	u.servesWithNoConsoleWindow()
	if u.running(oldSupervisor) {
		u.t.Error("the previous supervisor still runs")
	}
	if !u.running(cfoHost) {
		u.t.Error("the CFO's terminal host was ended")
	}
	journal, err := update.ReadJournal(u.state)
	if err != nil || journal.Phase != update.Done || !sameHomePath(journal.Root, u.programs) {
		u.t.Errorf("the journal is %+v (%v), want this update of %s, done", journal, err, u.programs)
	}
}

// The Overlord's machine: a checkout an older build made the home, its build
// at its root and no bin, CFO_HOME naming it, its board and the CFO's
// terminal running from that root, the journal of the finished update that
// installed that build, and no marker, which that build did not need. A
// release's cfo.exe run from a download folder installs itself where the
// home keeps its build, takes that journal as history, marks the checkout as
// the primary home its commands and hooks look for, brings its desktop window
// beside goblins.exe, restarts only the board, and leaves every file of the
// checkout and its fleet as it was.
func TestUpdateInstallsIntoACheckoutHomeWithItsBuildAtItsRoot(t *testing.T) {
	// Arrange
	k := newKeptHome(t)
	candidate := k.downloadCandidate("candidate")
	window := []byte("the release's window")
	if err := os.WriteFile(filepath.Join(filepath.Dir(k.candidate), "goblins-window.exe"), window, 0o755); err != nil {
		t.Fatal(err)
	}
	k.finishedJournal(k.root, k.state)

	// Act
	code, output := k.run([]string{"CFO_TEST_UPDATE_RESOLVE=1", "CFO_HOME=" + k.root, "CFO_STATE_OVERRIDE="})

	// Assert
	if strings.Contains(output, k.bin) {
		t.Errorf("the update took %s for where this home keeps its build:\n%s", k.bin, output)
	}
	k.installedHere(code, output, candidate, k.supervisor, k.cfoHost)
	if _, err := os.Stat(k.bin); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the update made %s (%v), which the home does not use", k.bin, err)
	}
	if !home.IsPrimary(home.Home{Root: k.root, State: k.state}) {
		t.Errorf("the checkout is not the primary home after the update, so its commands and hooks refuse it:\n%s", output)
	}
	if got, err := os.ReadFile(filepath.Join(k.root, "goblins-window.exe")); err != nil || !bytes.Equal(got, window) {
		t.Errorf("the desktop window beside goblins.exe is %q (%v), want the release's", got, err)
	}
	for name, content := range k.files() {
		if got, err := os.ReadFile(filepath.Join(k.root, filepath.FromSlash(name))); err != nil || string(got) != content {
			t.Errorf("%s = %q (%v), want it left as %q", name, got, err, content)
		}
	}
}

// Every shape of home an update meets is updated where it keeps its build:
// bin, or the root of a home a build before bin set up. A finished update's
// journal is history, whatever folder it names: one from before bin names
// the root, and one from before the home moved names where it was.
func TestUpdateInstallsWhereEachHomeShapeKeepsItsBuild(t *testing.T) {
	withBin := func(t *testing.T) *updateHome { return newUpdateHome(t, "previous", "candidate") }
	atRoot := func(t *testing.T) *updateHome { return newRootUpdateHome(t, "previous", "candidate") }
	for _, test := range []struct {
		name    string
		home    func(t *testing.T) *updateHome
		journal func(u *updateHome)
	}{
		{"a per-user home with bin", withBin, nil},
		{"an older home with its build at its root", atRoot, nil},
		{"a home with bin whose last update, before bin, swapped its root", withBin, func(u *updateHome) { u.finishedJournal(u.root, u.state) }},
		{"a home with bin moved since its last update finished", withBin, func(u *updateHome) { u.finishedJournal(`C:\elsewhere\bin`, `C:\elsewhere\state`) }},
		{"an older home moved since its last update finished", atRoot, func(u *updateHome) { u.finishedJournal(`C:\elsewhere`, `C:\elsewhere\state`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			u := test.home(t)
			oldSupervisor, cfoHost := u.serving()
			if test.journal != nil {
				test.journal(u)
			}
			candidate, err := os.ReadFile(u.candidate)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			code, output := u.run(nil)

			// Assert
			u.installedHere(code, output, candidate, oldSupervisor, cfoHost)
		})
	}
}

// An update of a home whose build is at its root that stops part way is
// recovered there: its recovery puts the previous build back at the root and
// starts the board from the goblins.exe there.
func TestAnUpdateAtAHomesRootRecoversThere(t *testing.T) {
	// Arrange
	u := newRootUpdateHome(t, "previous", "candidate")
	u.serving()
	if code, output := u.run([]string{"CFO_TEST_UPDATE_INTERRUPT=swapped"}); code != 9 {
		t.Fatalf("could not leave an update stopped once swapped (exit %d):\n%s", code, output)
	}

	// Act
	code, output := u.run(nil, "--recover")

	// Assert
	if code != updateRolledBack {
		t.Fatalf("recover exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	u.previousServes()
	u.servesWithNoConsoleWindow()
	record := u.awaitBoard()
	start, ok := proc.StartTime(record.PID)
	if !ok {
		t.Fatalf("the board's supervisor (pid %d) ended", record.PID)
	}
	identity, err := proc.Identify(record.PID, start)
	if err != nil || !sameHomePath(identity.Image, filepath.Join(u.root, "goblins.exe")) {
		t.Errorf("the board runs %s (%v), want the goblins.exe at the home's root", identity.Image, err)
	}
}
