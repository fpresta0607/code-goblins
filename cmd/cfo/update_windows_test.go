package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/update"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// updateHome is a scratch CFO home whose cfo.exe and goblins.exe are one
// build stand-in, with a candidate build waiting beside them.
type updateHome struct {
	t         *testing.T
	root      string
	state     string
	candidate string
	previous  []byte
	started   map[*exec.Cmd]time.Time
}

// writeBuild writes this test binary with build's marker to path.
func writeBuild(t *testing.T, path, build string) []byte {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(buildMarker+build+"\x00")...)
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return data
}

func newUpdateHome(t *testing.T, previous, candidate string) *updateHome {
	t.Helper()
	root := t.TempDir()
	u := &updateHome{t: t, root: root, state: filepath.Join(root, "state"), started: map[*exec.Cmd]time.Time{}}
	if err := os.MkdirAll(u.state, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range update.Aliases {
		u.previous = writeBuild(t, filepath.Join(root, name), previous)
	}
	u.candidate = filepath.Join(root, "cfo.exe.held-candidate")
	writeBuild(t, u.candidate, candidate)
	t.Cleanup(u.endAll)
	return u
}

// start runs program in the home, the way the live fleet runs it.
func (u *updateHome) start(program string, arguments ...string) *exec.Cmd {
	u.t.Helper()
	cmd := exec.Command(program, arguments...)
	cmd.Dir = u.root
	cmd.Env = append(os.Environ(), "CFO_TEST_UPDATE_ROOT="+u.root)
	if err := cmd.Start(); err != nil {
		u.t.Fatal(err)
	}
	start, _ := proc.StartTime(cmd.Process.Pid)
	u.started[cmd] = start
	go func() { _ = cmd.Wait() }()
	return cmd
}

// running reports whether a process this test started still runs.
func (u *updateHome) running(cmd *exec.Cmd) bool {
	return processIs(serveProcess{pid: cmd.Process.Pid, start: u.started[cmd]})
}

// serving starts the previous build's supervisor and a stand-in for the CFO's
// terminal host, and waits until the supervisor serves.
func (u *updateHome) serving() (supervisor, cfoHost *exec.Cmd) {
	u.t.Helper()
	supervisor = u.start(filepath.Join(u.root, "goblins.exe"), "serve", "--listen", "127.0.0.1:0")
	cfoHost = u.start(filepath.Join(u.root, "cfo.exe"), "host", "--id", "cfo")
	u.awaitBoard()
	return supervisor, cfoHost
}

// run runs the candidate's cfo update with arguments and the environment
// seams given, and returns its exit code and output.
func (u *updateHome) run(seams []string, arguments ...string) (int, string) {
	u.t.Helper()
	cmd := exec.Command(u.candidate, append([]string{"update"}, arguments...)...)
	cmd.Dir = u.root
	cmd.Env = append(append(os.Environ(), "CFO_TEST_UPDATE_ROOT="+u.root, "CFO_TEST_UPDATE_SERVE_WAIT=8s", "CFO_TEST_HANDOVER_WAIT=2s"), seams...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		u.t.Fatal(err)
	}
	return code, output.String()
}

// awaitBoard waits until the board record names a live supervisor that holds
// the watcher lock and whose board page answers.
func (u *updateHome) awaitBoard() boardRecord {
	u.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		record, err := readBoardRecord(u.state)
		if err == nil && boardAnswers(record, false) == nil {
			if holder, err := lock.ReadNamed(u.state, ".watch.lock"); err == nil && holder.PID == record.PID && holder.VerifiedAlive() {
				return record
			}
		}
		if time.Now().After(deadline) {
			u.t.Fatalf("no supervisor serves the board (record %+v, err %v)", record, err)
		}
	}
}

// aliasesAre checks both aliases hold want.
func (u *updateHome) aliasesAre(want []byte, what string) {
	u.t.Helper()
	for _, name := range update.Aliases {
		got, err := os.ReadFile(filepath.Join(u.root, name))
		if err != nil {
			u.t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			u.t.Fatalf("%s is not the %s build", name, what)
		}
	}
}

func (u *updateHome) previousServes() {
	u.t.Helper()
	record := u.awaitBoard()
	if boardAlive(context.Background(), record) == nil {
		u.t.Fatalf("the board answers /api/alive, so the candidate serves, not the previous build")
	}
	u.aliasesAre(u.previous, "previous")
}

func (u *updateHome) endAll() {
	if record, err := readBoardRecord(u.state); err == nil {
		_ = os.WriteFile(filepath.Join(u.state, "serve.stop"), []byte(`{"pid":`+strconv.Itoa(record.PID)+`}`), 0o600)
	}
	time.Sleep(300 * time.Millisecond)
	for cmd := range u.started {
		_ = cmd.Process.Kill()
	}
	// Supervisors the update started run outside this test's handles; the
	// board record names the last one.
	for _, found := range homeServesSince(home.Home{Root: u.root, State: u.state}, time.Time{}) {
		if process, err := os.FindProcess(found.pid); err == nil {
			_ = process.Kill()
			_ = process.Release()
		}
	}
	time.Sleep(500 * time.Millisecond)
}

// The update the cards run: the candidate takes over both aliases, its
// supervisor serves and answers as itself, the previous supervisor is gone,
// and the CFO's terminal host still runs, untouched.
func TestUpdateInstallsTheCandidateAndRestartsOnlyTheSupervisor(t *testing.T) {
	u := newUpdateHome(t, "previous", "candidate")
	oldSupervisor, cfoHost := u.serving()
	candidate, _ := os.ReadFile(u.candidate)

	code, output := u.run(nil)

	if code != updateInstalled {
		t.Fatalf("update exited %d:\n%s", code, output)
	}
	u.aliasesAre(candidate, "candidate")
	record := u.awaitBoard()
	if err := boardAlive(context.Background(), record); err != nil {
		t.Fatalf("the candidate's supervisor does not answer as itself: %v", err)
	}
	if u.running(oldSupervisor) {
		t.Error("the previous supervisor still runs")
	}
	if !u.running(cfoHost) {
		t.Fatal("the CFO's terminal host was ended")
	}
}

// A candidate whose supervisor never serves is rolled back: the previous
// build is put back under both aliases and its supervisor serves again.
func TestUpdateRollsBackACandidateThatNeverServes(t *testing.T) {
	for _, candidate := range []string{"silent", "crash"} {
		t.Run(candidate, func(t *testing.T) {
			u := newUpdateHome(t, "previous", candidate)
			_, cfoHost := u.serving()

			code, output := u.run(nil)

			if code != updateRolledBack {
				t.Fatalf("update exited %d, want %d:\n%s", code, updateRolledBack, output)
			}
			u.previousServes()
			if !u.running(cfoHost) {
				t.Fatal("the CFO's terminal host was ended")
			}
		})
	}
}

// The failure on 2026-10-01 was a process that ended part way. An update
// that ends at any step, including between starting the candidate's
// supervisor and recording it, and between moving an alias and replacing it,
// is finished by update --recover from the candidate's kept copy: the
// previous build serves again under both aliases.
func TestUpdateRecoversAfterTheProcessEndsAtEachStep(t *testing.T) {
	for _, step := range []string{"prepared", "stopped", "swapped", "started", "rolling-back"} {
		t.Run(step, func(t *testing.T) {
			u := newUpdateHome(t, "previous", "candidate")
			u.serving()
			seams := []string{"CFO_TEST_UPDATE_INTERRUPT=" + step}
			if step == "rolling-back" {
				seams = append(seams, "CFO_TEST_UPDATE_FAIL_RECORD=done")
			}

			code, output := u.run(seams)
			if code != 9 {
				t.Fatalf("the update did not end at %s (exit %d):\n%s", step, code, output)
			}
			journal, err := update.ReadJournal(u.state)
			if err != nil {
				t.Fatal(err)
			}
			u.candidate = journal.Copy

			code, output = u.run(nil, "--recover")

			if code != updateRolledBack {
				t.Fatalf("recover exited %d, want %d:\n%s", code, updateRolledBack, output)
			}
			u.previousServes()
		})
	}
}

// A previous-build supervisor that runs but never serves is ended, and only
// it, before the next try, so the next one can take the lock.
func TestRollbackEndsALiveButUnhealthyAttemptBeforeTryingAgain(t *testing.T) {
	u := newUpdateHome(t, "flaky", "crash")
	// The flaky build is silent only on its first serve, so the running
	// one counts as that first.
	if err := os.WriteFile(filepath.Join(u.state, "test-serve-starts"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	u.serving()
	if err := os.WriteFile(filepath.Join(u.state, "test-serve-starts"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, output := u.run(nil)

	if code != updateRolledBack {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	u.previousServes()
	journal, err := update.ReadJournal(u.state)
	if err != nil {
		t.Fatal(err)
	}
	if len(journal.Attempts) < 3 {
		t.Fatalf("attempts recorded %+v, want the candidate's and two of the previous build's", journal.Attempts)
	}
	for _, attempt := range journal.Attempts[1 : len(journal.Attempts)-1] {
		if processIs(serveProcess{pid: attempt.PID, start: attempt.Start}) {
			t.Errorf("the unhealthy attempt pid %d still runs", attempt.PID)
		}
	}
}

// An alias that cannot be put back leaves the previous build serving from
// its verified copy, and the update unfinished: update --recover repairs the
// aliases once it can, and only then is it done.
func TestARollbackThatCannotRestoreAnAliasStaysRecoverableUntilRepaired(t *testing.T) {
	u := newUpdateHome(t, "previous", "crash")
	u.serving()

	code, output := u.run([]string{"CFO_TEST_UPDATE_HOLD=goblins.exe"})

	if code != updateDegraded {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateDegraded, output)
	}
	u.awaitBoard()
	journal, err := update.ReadJournal(u.state)
	if err != nil || journal.Phase != update.Degraded || journal.Phase.Finished() {
		t.Fatalf("journal phase %q, %v; want the unfinished degraded phase", journal.Phase, err)
	}

	code, output = u.run(nil, "--recover")

	if code != updateRolledBack {
		t.Fatalf("recover exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	u.aliasesAre(u.previous, "previous")
}

// A journal that cannot be read, or that is not this home's, is never taken
// for permission: update and recover both refuse and change nothing.
func TestUpdateRefusesAJournalItCannotTrust(t *testing.T) {
	for name, journal := range map[string]string{
		"unreadable":      "not json",
		"another schema":  `{"schema":"cfo-update.v0","phase":"swapped"}`,
		"another home's":  `{"schema":"cfo-update.v1","root":"C:\\elsewhere","phase":"swapped","aliases":[]}`,
		"unfinished here": "",
	} {
		t.Run(name, func(t *testing.T) {
			u := newUpdateHome(t, "previous", "candidate")
			if journal == "" {
				code, _ := u.run([]string{"CFO_TEST_UPDATE_INTERRUPT=prepared"})
				if code != 9 {
					t.Fatal("could not leave an unfinished update")
				}
			} else {
				if err := os.MkdirAll(update.Dir(u.state), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(update.Dir(u.state), "journal.json"), []byte(journal), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(update.Dir(u.state), "journal.json"))

			code, output := u.run(nil)

			if code != 1 {
				t.Fatalf("update over a journal it cannot trust exited %d:\n%s", code, output)
			}
			u.aliasesAre(u.previous, "previous")
			after, _ := os.ReadFile(filepath.Join(update.Dir(u.state), "journal.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("the journal was overwritten")
			}
			if journal != "" {
				if code, output := u.run(nil, "--recover"); code != 1 {
					t.Fatalf("recover from a journal it cannot trust exited %d:\n%s", code, output)
				}
			}
		})
	}
}

// A success the journal cannot record is rolled back, so a later recover
// never puts the previous build over one that serves.
func TestUpdateRollsBackWhenItCannotRecordItsSuccess(t *testing.T) {
	u := newUpdateHome(t, "previous", "candidate")
	u.serving()

	code, output := u.run([]string{"CFO_TEST_UPDATE_FAIL_RECORD=done"})

	if code != updateRolledBack {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	u.previousServes()
}

// One update owns a home at a time.
func TestASecondUpdateOfAHomeIsRefusedWhileOneRuns(t *testing.T) {
	u := newUpdateHome(t, "previous", "candidate")
	u.serving()
	first := exec.Command(u.candidate, "update")
	first.Dir = u.root
	first.Env = append(os.Environ(), "CFO_TEST_UPDATE_ROOT="+u.root, "CFO_TEST_UPDATE_PAUSE=prepared")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	u.started[first] = time.Now()
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if journal, err := update.ReadJournal(u.state); err == nil && journal.Phase == update.Prepared {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first update never prepared")
		}
	}

	code, output := u.run(nil)

	if code != 1 || !strings.Contains(output, "another update of this home is running") {
		t.Fatalf("a second update exited %d:\n%s", code, output)
	}
}

// On 2026-10-01 the CFO's Stop hook held the watcher lock through an
// install, and a build older than the handover cannot take it. The rollback
// takes it over from such a hook, proved a watcher, before starting the
// previous build, which then serves.
func TestRollbackTakesTheLockFromAHookBeforeStartingThePreviousBuild(t *testing.T) {
	u := newUpdateHome(t, "previous", "crash")
	hook := u.start(filepath.Join(u.root, "cfo.exe"), "hook", "stop-autoarm")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if _, err := lock.AcquireNamedOwner(u.state, ".watch.lock", hook.Process.Pid, watch.WatcherSession); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the hook stand-in never held the lock")
		}
	}

	code, output := u.run(nil)

	if code != updateRolledBack {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	u.previousServes()
}

// The counter-example from 2026-10-01: an error during the swap itself
// still rolls back, and the previous build serves again.
func TestASwapThatFailsPartWayRollsBack(t *testing.T) {
	u := newUpdateHome(t, "previous", "candidate")
	u.serving()

	code, output := u.run([]string{"CFO_TEST_UPDATE_BREAK_SWAP=1"})

	if code != updateRolledBack {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	u.previousServes()
}

// Windows keeps an ended process's start time readable while any handle to
// it stays open, as the program that started it or a watching tool may hold.
// An ended supervisor is ended all the same.
func TestAnEndedProcessIsNotRunningWhileAHandleToItStaysOpen(t *testing.T) {
	child := exec.Command("cmd.exe", "/c", "exit 0")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Wait()
	start, ok := proc.StartTime(child.Process.Pid)
	if !ok {
		t.Fatal("the child has no start time")
	}
	ended := serveProcess{pid: child.Process.Pid, start: start}

	for deadline := time.Now().Add(10 * time.Second); processIs(ended); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a process that ended still reads as running while a handle to it is open")
		}
	}
	if _, ok := proc.StartTime(child.Process.Pid); !ok {
		t.Fatal("the premise failed: the ended process's start time is not readable through the held handle")
	}
}

// When a verified copy is the way back, the copy started is proved the
// previous build first: a copy whose content changed never runs, the copy
// still intact serves, and with none intact nothing runs and the board is
// reported down. Either way the update stays unfinished, so --recover can
// still finish it once a good copy is back.
func TestRollbackNeverRunsACopyWhoseContentChanged(t *testing.T) {
	for _, test := range []struct {
		name     string
		tampered string
		code     int
	}{
		{"one copy changed", "previous-goblins.exe", updateDegraded},
		{"both copies changed", "previous-cfo.exe,previous-goblins.exe", updateBoardDown},
	} {
		t.Run(test.name, func(t *testing.T) {
			u := newUpdateHome(t, "previous", "crash")
			writeBuild(t, filepath.Join(u.root, "tampered.exe"), "tampered")
			u.serving()

			code, output := u.run([]string{"CFO_TEST_UPDATE_HOLD=goblins.exe", "CFO_TEST_UPDATE_TAMPER=" + test.tampered})

			if code != test.code {
				t.Fatalf("update exited %d, want %d:\n%s", code, test.code, output)
			}
			if _, err := os.Stat(filepath.Join(u.state, "test-tampered-ran")); !os.IsNotExist(err) {
				t.Fatalf("a copy whose content changed ran (%v):\n%s", err, output)
			}
			if journal, err := update.ReadJournal(u.state); err != nil || journal.Phase.Finished() {
				t.Fatalf("journal phase %q, %v; want an unfinished update that --recover can still finish", journal.Phase, err)
			}
			if test.code == updateDegraded {
				u.awaitBoard()
			}
		})
	}
}

// A degraded update whose backup supervisor is gone, as after the machine
// restarted, is finished by update --recover only once both aliases are the
// previous build again and its board serves.
func TestRecoverAfterARestartFinishesADegradedUpdateWithTheBoardServing(t *testing.T) {
	u := newUpdateHome(t, "previous", "crash")
	u.serving()
	if code, output := u.run([]string{"CFO_TEST_UPDATE_HOLD=goblins.exe"}); code != updateDegraded {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateDegraded, output)
	}
	journal, err := update.ReadJournal(u.state)
	if err != nil {
		t.Fatal(err)
	}
	last := journal.Attempts[len(journal.Attempts)-1]
	if process, err := os.FindProcess(last.PID); err == nil {
		_ = process.Kill()
		_ = process.Release()
	}
	for deadline := time.Now().Add(10 * time.Second); processIs(serveProcess{pid: last.PID, start: last.Start}); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the backup's supervisor did not end")
		}
	}

	code, output := u.run(nil, "--recover")

	if code != updateRolledBack {
		t.Fatalf("recover exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	u.previousServes()
}

// The fleet runs scratch supervisors from the same binary for other states.
// A rollback ends only this home's: a supervisor of the same program, in the
// same folder, serving another state, and a terminal's host, keep running,
// even one started while the update ran.
func TestRollbackLeavesASupervisorOfAnotherStateRunning(t *testing.T) {
	u := newUpdateHome(t, "previous", "crash")
	_, cfoHost := u.serving()
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	updating := exec.Command(u.candidate, "update")
	updating.Dir = u.root
	updating.Env = append(os.Environ(), "CFO_TEST_UPDATE_ROOT="+u.root, "CFO_TEST_UPDATE_SERVE_WAIT=8s", "CFO_TEST_UPDATE_PAUSE=stopped")
	var output bytes.Buffer
	updating.Stdout, updating.Stderr = &output, &output
	if err := updating.Start(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if journal, err := update.ReadJournal(u.state); err == nil && journal.Phase == update.Stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the update never stopped the supervisor")
		}
	}
	foreign := exec.Command(filepath.Join(u.root, "goblins.exe"), "serve", "--listen", "127.0.0.1:0")
	foreign.Dir = u.root
	foreign.Env = append(os.Environ(), "CFO_TEST_UPDATE_ROOT="+other, "CFO_STATE_OVERRIDE="+filepath.Join(other, "state"))
	if err := foreign.Start(); err != nil {
		t.Fatal(err)
	}
	foreignStart, _ := proc.StartTime(foreign.Process.Pid)
	u.started[foreign] = foreignStart
	go func() { _ = foreign.Wait() }()

	err := updating.Wait()

	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != updateRolledBack {
		t.Fatalf("update ended %v, want exit %d:\n%s", err, updateRolledBack, output.String())
	}
	u.previousServes()
	if !u.running(foreign) {
		t.Fatal("the rollback ended a supervisor of another state")
	}
	if !u.running(cfoHost) {
		t.Fatal("the rollback ended the CFO's terminal host")
	}
}

// A journal attempt that names a process outside this home never authorizes
// ending it.
func TestRecoverLeavesAProcessAMalformedAttemptNamesRunning(t *testing.T) {
	u := newUpdateHome(t, "previous", "candidate")
	u.serving()
	if code, output := u.run([]string{"CFO_TEST_UPDATE_INTERRUPT=swapped"}); code != 9 {
		t.Fatalf("the update did not end at swapped (exit %d):\n%s", code, output)
	}
	stranger := exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 >NUL")
	if err := stranger.Start(); err != nil {
		t.Fatal(err)
	}
	strangerStart, _ := proc.StartTime(stranger.Process.Pid)
	u.started[stranger] = strangerStart
	go func() { _ = stranger.Wait() }()
	journal, err := update.ReadJournal(u.state)
	if err != nil {
		t.Fatal(err)
	}
	journal.Attempts = append(journal.Attempts, update.Attempt{PID: stranger.Process.Pid, Start: strangerStart, Program: `C:\Windows\System32\cmd.exe`}, update.Attempt{PID: stranger.Process.Pid, Start: strangerStart, Program: filepath.Join(u.root, "goblins.exe")})
	if err := update.Record(u.state, &journal, journal.Phase, ""); err != nil {
		t.Fatal(err)
	}
	u.candidate = journal.Copy

	code, output := u.run(nil, "--recover")

	if code != updateRolledBack {
		t.Fatalf("recover exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	u.previousServes()
	if !u.running(stranger) {
		t.Fatal("recover ended a process a malformed attempt named")
	}
}

// A supervisor can run from any folder with CFO_HOME naming its home, as from
// a user's own folder; it is this home's supervisor all the same, and the
// update stops it and serves the candidate.
func TestUpdateStopsTheHomesSupervisorStartedElsewhereWithCFOHome(t *testing.T) {
	u := newUpdateHome(t, "previous", "candidate")
	elsewhere := exec.Command(filepath.Join(u.root, "goblins.exe"), "serve", "--listen", "127.0.0.1:0")
	elsewhere.Dir = t.TempDir()
	elsewhere.Env = append(os.Environ(), "CFO_TEST_UPDATE_ROOT="+u.root, "CFO_HOME="+u.root)
	if err := elsewhere.Start(); err != nil {
		t.Fatal(err)
	}
	start, _ := proc.StartTime(elsewhere.Process.Pid)
	u.started[elsewhere] = start
	go func() { _ = elsewhere.Wait() }()
	u.awaitBoard()

	code, output := u.run(nil)

	if code != updateInstalled {
		t.Fatalf("update exited %d:\n%s", code, output)
	}
	if u.running(elsewhere) {
		t.Fatal("the home's supervisor started elsewhere still runs")
	}
}

// Whether a running supervisor is the previous build is read from the image
// it has loaded, never from the alias's current content: a candidate
// supervisor whose file was moved aside and replaced by the previous build is
// still the candidate.
func TestARunningSupervisorIsTheBuildItLoadedNotWhatItsAliasHoldsNow(t *testing.T) {
	u := newUpdateHome(t, "previous", "candidate")
	candidate, _ := os.ReadFile(u.candidate)
	journal := &update.Journal{Aliases: []update.Alias{{Name: "goblins.exe", Previous: hashOf(u.previous)}}}
	alias := filepath.Join(u.root, "goblins.exe")
	if err := os.WriteFile(alias+".update-new", candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(alias, alias+".previous-aside"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(alias+".update-new", alias); err != nil {
		t.Fatal(err)
	}
	candidateServe := u.start(alias, "serve", "--listen", "127.0.0.1:0")
	u.awaitBoard()
	running := serveProcess{pid: candidateServe.Process.Pid, start: u.started[candidateServe]}

	if err := os.Rename(alias, alias+".candidate-aside"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alias, u.previous, 0o755); err != nil {
		t.Fatal(err)
	}

	if runsPreviousBuild(running, journal) {
		t.Fatal("a candidate supervisor whose alias now holds the previous build was taken for the previous build")
	}
	previousServe := u.start(alias, "host", "--id", "previous")
	if !runsPreviousBuild(serveProcess{pid: previousServe.Process.Pid, start: u.started[previousServe]}, journal) {
		t.Fatal("a process running the previous build was not recognised as it")
	}
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
