package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	return newUpdateHomeIn(t, t.TempDir(), previous, candidate)
}

func newUpdateHomeIn(t *testing.T, root, previous, candidate string) *updateHome {
	t.Helper()
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

// updateFiles is every file under the home's state\update but the update's
// own lock, by name.
func (u *updateHome) updateFiles() map[string][]byte {
	u.t.Helper()
	files := map[string][]byte{}
	entries, _ := os.ReadDir(update.Dir(u.state))
	for _, entry := range entries {
		if entry.Name() == ".lock" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(update.Dir(u.state), entry.Name()))
		if err != nil {
			u.t.Fatal(err)
		}
		files[entry.Name()] = data
	}
	return files
}

// unchanged checks every file under state\update is still before.
func (u *updateHome) unchanged(before map[string][]byte) {
	u.t.Helper()
	after := u.updateFiles()
	if len(after) != len(before) {
		u.t.Fatalf("state\\update holds %d files, want the %d it held", len(after), len(before))
	}
	for name, data := range before {
		if !bytes.Equal(after[name], data) {
			u.t.Fatalf("%s under state\\update was changed", name)
		}
	}
}

// The ways an update refuses an earlier journal, by what it tells the user.
const (
	// recoverFirst: an unfinished update of this home, finished by its
	// recovery line.
	recoverFirst = "recover first"
	// untrusted: this home's journal, but one no update writes.
	untrusted = "untrusted"
	// movedHome: a finished update of another home, which may be this home
	// before it was moved.
	movedHome = "moved home"
	// othersWayBack: an unfinished update of another home, whose journal is
	// that update's way back and is never to be moved aside.
	othersWayBack = "another's way back"
)

// A journal that cannot be read, or that is not this home's, finished or not,
// is never taken for permission: update and recover both refuse and change
// nothing, the journal and every copy included. Only an unfinished update of
// this home is answered with a recovery line, and that line is this home's
// own; a malformed journal of this home is one that cannot be trusted, and
// only a finished journal of another home may be moved aside.
func TestUpdateRefusesAJournalItCannotTrust(t *testing.T) {
	for _, test := range []struct {
		name    string
		journal string
		refusal string
	}{
		{"unreadable", "not json", untrusted},
		{"another schema", `{"schema":"cfo-update.v0","root":"ROOT","phase":"swapped"}`, untrusted},
		{"another alias set", `{"schema":"cfo-update.v1","root":"ROOT","phase":"done","candidate_copy":"STATE\\update\\candidate.exe","aliases":[]}`, untrusted},
		{"an edited copy", `{"schema":"cfo-update.v1","root":"ROOT","phase":"swapped","candidate_copy":"C:\\elsewhere\\x.exe","aliases":[]}`, untrusted},
		{"another home's", `{"schema":"cfo-update.v1","root":"C:\\elsewhere","phase":"swapped","aliases":[]}`, othersWayBack},
		{"another home's finished", `{"schema":"cfo-update.v1","root":"C:\\elsewhere","phase":"done","candidate_copy":"C:\\elsewhere\\state\\update\\candidate.exe","aliases":[]}`, movedHome},
		{"unfinished here", "", recoverFirst},
	} {
		t.Run(test.name, func(t *testing.T) {
			u := newUpdateHome(t, "previous", "candidate")
			if test.journal == "" {
				code, _ := u.run([]string{"CFO_TEST_UPDATE_INTERRUPT=prepared"})
				if code != 9 {
					t.Fatal("could not leave an unfinished update")
				}
			} else {
				if err := os.MkdirAll(update.Dir(u.state), 0o700); err != nil {
					t.Fatal(err)
				}
				journal := strings.NewReplacer("ROOT", strings.ReplaceAll(u.root, `\`, `\\`), "STATE", strings.ReplaceAll(u.state, `\`, `\\`)).Replace(test.journal)
				if err := os.WriteFile(filepath.Join(update.Dir(u.state), "journal.json"), []byte(journal), 0o600); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"candidate.exe", "previous-cfo.exe", "previous-goblins.exe"} {
					if err := os.WriteFile(filepath.Join(update.Dir(u.state), name), []byte("an earlier update's "+name), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := u.updateFiles()

			code, output := u.run(nil)

			if code != 1 {
				t.Fatalf("update over a journal it cannot trust exited %d:\n%s", code, output)
			}
			u.aliasesAre(u.previous, "previous")
			u.unchanged(before)
			if line := recoverCommand(home.Home{Root: u.root, State: u.state}); (test.refusal == recoverFirst) != strings.Contains(output, line) {
				t.Fatalf("the recovery line %s is printed only for an unfinished update of this home:\n%s", line, output)
			}
			if test.refusal != recoverFirst && strings.Contains(output, "--recover") {
				t.Fatalf("a recovery line was printed for a journal that is not this home's:\n%s", output)
			}
			if (test.refusal == untrusted) != strings.Contains(output, "cannot be trusted") {
				t.Fatalf("only a malformed journal of this home is refused as one that cannot be trusted:\n%s", output)
			}
			if (test.refusal == movedHome) != strings.Contains(output, "aside") {
				t.Fatalf("only a finished journal of another home may be moved aside:\n%s", output)
			}
			if (test.refusal == movedHome || test.refusal == othersWayBack) && (!strings.Contains(output, `C:\elsewhere`) || !strings.Contains(output, u.root)) {
				t.Fatalf("the refusal does not name both homes:\n%s", output)
			}
			if test.refusal != recoverFirst {
				if code, output := u.run(nil, "--recover"); code != 1 {
					t.Fatalf("recover from a journal it cannot trust exited %d:\n%s", code, output)
				}
			}
		})
	}
}

// strangerServing starts a supervisor that holds this home's watcher lock and
// serves its board but cannot be proved this home's: by the state it runs
// with, it serves another.
func (u *updateHome) strangerServing() *exec.Cmd {
	u.t.Helper()
	stranger := exec.Command(filepath.Join(u.root, "goblins.exe"), "serve", "--listen", "127.0.0.1:0")
	stranger.Dir = u.root
	stranger.Env = append(os.Environ(), "CFO_TEST_UPDATE_ROOT="+u.root, "CFO_STATE_OVERRIDE="+filepath.Join(u.t.TempDir(), "state"))
	if err := stranger.Start(); err != nil {
		u.t.Fatal(err)
	}
	start, _ := proc.StartTime(stranger.Process.Pid)
	u.started[stranger] = start
	go func() { _ = stranger.Wait() }()
	u.awaitBoard()
	return stranger
}

// An update stops only this home's own supervisor. One holding this home's
// watcher lock that is not proved this home's, here one serving another
// state, is refused before anything changes, and it keeps serving.
func TestUpdateRefusesASupervisorItCannotProveThisHomes(t *testing.T) {
	u := newUpdateHome(t, "previous", "candidate")
	stranger := u.strangerServing()

	code, output := u.run(nil)

	if code != 1 || !strings.Contains(output, "pid "+strconv.Itoa(stranger.Process.Pid)) {
		t.Fatalf("update exited %d, want a refusal naming pid %d:\n%s", code, stranger.Process.Pid, output)
	}
	entries, _ := os.ReadDir(update.Dir(u.state))
	for _, entry := range entries {
		if entry.Name() != ".lock" {
			t.Errorf("the refused update wrote %s", entry.Name())
		}
	}
	u.aliasesAre(u.previous, "previous")
	if staged, _ := filepath.Glob(filepath.Join(u.root, "*.update-*")); len(staged) != 0 {
		t.Errorf("the refused update touched the aliases: %v", staged)
	}
	if !u.running(stranger) {
		t.Fatal("the refused update ended a supervisor it could not prove this home's")
	}
	u.awaitBoard()
}

// Recovery stops only this home's own supervisor too. With an update ended
// part way and a supervisor that cannot be proved this home's holding the
// lock and serving, recovery refuses before anything changes, names it to be
// stopped, leaves the update unfinished, and never reports the board down.
func TestRecoverRefusesASupervisorItCannotProveThisHomes(t *testing.T) {
	u := newUpdateHome(t, "previous", "candidate")
	u.serving()
	if code, output := u.run([]string{"CFO_TEST_UPDATE_INTERRUPT=swapped"}); code != 9 {
		t.Fatalf("the update did not end at swapped (exit %d):\n%s", code, output)
	}
	journal, err := update.ReadJournal(u.state)
	if err != nil {
		t.Fatal(err)
	}
	u.candidate = journal.Copy
	candidate, _ := os.ReadFile(journal.Copy)
	stranger := u.strangerServing()
	before := u.updateFiles()

	code, output := u.run(nil, "--recover")

	if code != 1 || !strings.Contains(output, "pid "+strconv.Itoa(stranger.Process.Pid)) || !strings.Contains(output, "goblins stop") {
		t.Fatalf("recover exited %d, want a refusal naming pid %d to stop:\n%s", code, stranger.Process.Pid, output)
	}
	if strings.Contains(output, "BOARD IS DOWN") {
		t.Fatalf("recover reported the board down while a supervisor serves it:\n%s", output)
	}
	u.aliasesAre(candidate, "candidate")
	u.unchanged(before)
	if journal, err := update.ReadJournal(u.state); err != nil || journal.Phase.Finished() {
		t.Fatalf("journal phase %q, %v; want the update still unfinished", journal.Phase, err)
	}
	if !u.running(stranger) {
		t.Fatal("recover ended a supervisor it could not prove this home's")
	}
	u.awaitBoard()
}

// The files a rollback moved aside go only once the rollback is durably
// recorded. When the journal cannot record it, the previous build still
// serves, every file stays, and the output says the update is unfinished with
// the line that finishes it, which then does, and cleans up.
func TestARollbackItCannotRecordKeepsItsFilesAndStaysRecoverable(t *testing.T) {
	u := newUpdateHome(t, "previous", "crash")
	u.start(filepath.Join(u.root, "goblins.exe"), "serve", "--listen", "127.0.0.1:0")
	u.awaitBoard()

	code, output := u.run([]string{"CFO_TEST_UPDATE_FAIL_RECORD=rolled-back"})

	if code != updateRolledBack {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	u.previousServes()
	line := recoverCommand(home.Home{Root: u.root, State: u.state})
	if !strings.Contains(output, "could not record the rollback") || !strings.Contains(output, line) {
		t.Fatalf("the output does not say the rollback is unrecorded with the line %s:\n%s", line, output)
	}
	journal, err := update.ReadJournal(u.state)
	if err != nil || journal.Phase.Finished() {
		t.Fatalf("journal phase %q, %v; want the update still unfinished", journal.Phase, err)
	}
	var aside []string
	for _, alias := range journal.Aliases {
		aside = append(aside, alias.Aside...)
	}
	if len(aside) == 0 {
		t.Fatal("the journal tracks no file moved aside")
	}
	for _, path := range aside {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s, moved aside by an unrecorded rollback, is gone: %v", path, err)
		}
	}
	u.candidate = journal.Copy

	code, output = u.run(nil, "--recover")

	if code != updateRolledBack {
		t.Fatalf("recover exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	u.previousServes()
	for _, path := range aside {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s is still there once the rollback is recorded: %v", path, err)
		}
	}
}

// Recovery removes the files its journal says were moved aside, so a journal
// that names any other file, outside the home, under another alias's name or
// without a number, is refused before anything changes, and that file and
// everything under state\update stay as they are.
func TestRecoverRefusesAJournalNamingAFileAnUpdateNeverMovedAside(t *testing.T) {
	for _, test := range []struct {
		name  string
		aside func(u *updateHome) string
	}{
		{"outside the home", func(u *updateHome) string { return filepath.Join(u.t.TempDir(), "sentinel.txt") }},
		{"another alias's", func(u *updateHome) string { return filepath.Join(u.root, "goblins.exe.1.update-old") }},
		{"no number", func(u *updateHome) string { return filepath.Join(u.root, "cfo.exe.abc.update-old") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			u := newUpdateHome(t, "previous", "candidate")
			u.serving()
			if code, output := u.run([]string{"CFO_TEST_UPDATE_INTERRUPT=swapped"}); code != 9 {
				t.Fatalf("the update did not end at swapped (exit %d):\n%s", code, output)
			}
			journal, err := update.ReadJournal(u.state)
			if err != nil {
				t.Fatal(err)
			}
			sentinel := test.aside(u)
			if err := os.WriteFile(sentinel, []byte("not this update's"), 0o600); err != nil {
				t.Fatal(err)
			}
			journal.Aliases[0].Aside = append(journal.Aliases[0].Aside, sentinel)
			if err := update.Record(u.state, &journal, journal.Phase, ""); err != nil {
				t.Fatal(err)
			}
			u.candidate = journal.Copy
			before := u.updateFiles()

			code, output := u.run(nil, "--recover")

			if code != 1 {
				t.Fatalf("recover exited %d, want 1:\n%s", code, output)
			}
			u.unchanged(before)
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != "not this update's" {
				t.Fatalf("the file the journal named was changed or removed (%v)", err)
			}
		})
	}
}

// A rollback removes what it moved aside or staged, and only that: an
// earlier install's files and the user's stay.
func TestARollbackRemovesOnlyTheFilesItMovedAside(t *testing.T) {
	u := newUpdateHome(t, "previous", "crash")
	kept := map[string][]byte{}
	for _, name := range []string{"cfo.exe.1.update-old", "goblins.exe.1.update-old", "my-build.exe"} {
		kept[name] = []byte("not this update's " + name)
		if err := os.WriteFile(filepath.Join(u.root, name), kept[name], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	u.start(filepath.Join(u.root, "goblins.exe"), "serve", "--listen", "127.0.0.1:0")
	u.awaitBoard()

	code, output := u.run(nil)

	if code != updateRolledBack {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateRolledBack, output)
	}
	u.previousServes()
	for name, want := range kept {
		if got, err := os.ReadFile(filepath.Join(u.root, name)); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s, not this update's, was changed or removed (%v)", name, err)
		}
	}
	for _, pattern := range []string{"*.update-old", "*.update-new", "*.update-restore"} {
		matches, _ := filepath.Glob(filepath.Join(u.root, pattern))
		for _, match := range matches {
			if _, ok := kept[filepath.Base(match)]; !ok {
				t.Errorf("the rollback left %s behind", filepath.Base(match))
			}
		}
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

// environmentWithout is this process's environment less the named variables,
// compared without regard to case as Windows does.
func environmentWithout(names ...string) []string {
	var kept []string
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		isDropped := false
		for _, dropped := range names {
			isDropped = isDropped || strings.EqualFold(name, dropped)
		}
		if !isDropped {
			kept = append(kept, variable)
		}
	}
	return kept
}

// relativeHomes are the ways to name a home relative to the folder an update
// runs in, the folder that holds the home: its root by CFO_HOME, or its state
// by CFO_STATE_OVERRIDE.
var relativeHomes = []struct {
	name        string
	environment func(u *updateHome) []string
}{
	{"a relative CFO_HOME", func(*updateHome) []string { return []string{"CFO_HOME=my-home"} }},
	{"a relative CFO_STATE_OVERRIDE", func(u *updateHome) []string {
		return []string{"CFO_HOME=" + u.root, `CFO_STATE_OVERRIDE=my-home\state`}
	}},
}

// A supervisor's home and state are where its own file operations land: a
// relative CFO_HOME or CFO_STATE_OVERRIDE resolves against the folder it runs
// in, so one started from the folder that holds the home with relative names
// is this home's, and a relative name that means another state is not.
func TestAHomesSupervisorIsKnownByWhereItsRelativeNamesResolve(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "my-home")
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	for _, test := range []struct {
		name        string
		directory   string
		environment []string
		isHomes     bool
	}{
		{"a relative CFO_HOME from the folder that holds it", parent, []string{"CFO_HOME=my-home"}, true},
		{"a relative CFO_STATE_OVERRIDE from the folder that holds it", parent, []string{"CFO_HOME=" + root, `CFO_STATE_OVERRIDE=my-home\state`}, true},
		{"both relative from the folder that holds it", parent, []string{"CFO_HOME=my-home", `CFO_STATE_OVERRIDE=my-home\state`}, true},
		{"a relative state that means another state", parent, []string{"CFO_HOME=" + root, `CFO_STATE_OVERRIDE=other\state`}, false},
		{"the same relative state from the home's own root", root, []string{`CFO_STATE_OVERRIDE=my-home\state`}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			program := filepath.Join(root, "goblins.exe")
			identity := proc.Identity{Image: program, Arguments: []string{program, "serve", "--listen", "127.0.0.1:0"}, Directory: test.directory, Environment: test.environment}

			err := homeServe(h)(identity)

			if test.isHomes && err != nil {
				t.Fatalf("this home's supervisor was refused: %v", err)
			}
			if !test.isHomes && err == nil {
				t.Fatal("a supervisor of another state was taken for this home's")
			}
		})
	}
}

// An update whose home is named relative to the folder it runs in installs
// into that home and state: the supervisors it starts run in the home's root,
// where the same relative name means another place, so it pins the home and
// state for them; that other place stays untouched. The supervisor already
// running may have been started either way: by absolute names in the home's
// root, or from the folder that holds the home with the same relative names.
func TestAnUpdateNamedByRelativePathsInstallsIntoItsHome(t *testing.T) {
	for _, test := range relativeHomes {
		for _, isRelativeSupervisor := range []bool{false, true} {
			name := test.name + ", the running supervisor started by absolute names"
			if isRelativeSupervisor {
				name = test.name + ", the running supervisor started by the same relative names"
			}
			t.Run(name, func(t *testing.T) {
				parent := t.TempDir()
				u := newUpdateHomeIn(t, filepath.Join(parent, "my-home"), "previous", "candidate")
				if isRelativeSupervisor {
					running := exec.Command(filepath.Join(u.root, "goblins.exe"), "serve", "--listen", "127.0.0.1:0")
					running.Dir = parent
					running.Env = append(environmentWithout("CFO_HOME", "CFO_STATE_OVERRIDE", "CFO_TEST_UPDATE_ROOT"), test.environment(u)...)
					running.Env = append(running.Env, "CFO_TEST_UPDATE_RESOLVE=1")
					if err := running.Start(); err != nil {
						t.Fatal(err)
					}
					started, _ := proc.StartTime(running.Process.Pid)
					u.started[running] = started
					go func() { _ = running.Wait() }()
					u.awaitBoard()
				} else {
					u.serving()
				}
				candidate, _ := os.ReadFile(u.candidate)
				updating := exec.Command(u.candidate, "update")
				updating.Dir = parent
				updating.Env = append(environmentWithout("CFO_HOME", "CFO_STATE_OVERRIDE", "CFO_TEST_UPDATE_ROOT"), test.environment(u)...)
				updating.Env = append(updating.Env, "CFO_TEST_UPDATE_RESOLVE=1", "CFO_TEST_UPDATE_SERVE_WAIT=8s", "CFO_TEST_HANDOVER_WAIT=2s")

				output, err := updating.CombinedOutput()

				if err != nil {
					t.Fatalf("update ended %v, want it installed:\n%s", err, output)
				}
				u.aliasesAre(candidate, "candidate")
				if err := boardAlive(context.Background(), u.awaitBoard()); err != nil {
					t.Fatalf("the candidate's supervisor does not answer as this home's: %v", err)
				}
				if _, err := os.Stat(filepath.Join(u.root, "my-home")); !os.IsNotExist(err) {
					t.Errorf("the update reached %s, the place the relative name means from the home's root (%v)", filepath.Join(u.root, "my-home"), err)
				}
			})
		}
	}
}

// An update named by relative paths that ends part way prints a recovery
// line that, pasted in another folder, recovers that home and state, and
// leaves the place the relative names mean from that folder untouched.
func TestARelativelyNamedUpdateRecoversFromAnotherFolder(t *testing.T) {
	for _, test := range relativeHomes {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			u := newUpdateHomeIn(t, filepath.Join(parent, "my-home"), "previous", "candidate")
			u.serving()
			updating := exec.Command(u.candidate, "update")
			updating.Dir = parent
			updating.Env = append(environmentWithout("CFO_HOME", "CFO_STATE_OVERRIDE", "CFO_TEST_UPDATE_ROOT"), test.environment(u)...)
			updating.Env = append(updating.Env, "CFO_TEST_UPDATE_RESOLVE=1", "CFO_TEST_UPDATE_SERVE_WAIT=8s", "CFO_TEST_HANDOVER_WAIT=2s", "CFO_TEST_UPDATE_INTERRUPT=swapped")
			output, updateErr := updating.CombinedOutput()
			var line string
			printed := strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n")
			for at := 0; at+1 < len(printed); at++ {
				if strings.HasSuffix(printed[at], "Windows PowerShell:") {
					line = strings.TrimSpace(printed[at+1])
				}
			}
			if line == "" {
				t.Fatalf("the update (%v) printed no recovery line:\n%s", updateErr, output)
			}
			elsewhere := t.TempDir()
			pasted := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", line+"; exit $LASTEXITCODE")
			pasted.Dir = elsewhere
			pasted.Env = append(environmentWithout("CFO_HOME", "CFO_STATE_OVERRIDE", "CFO_TEST_UPDATE_ROOT"), "CFO_TEST_UPDATE_RESOLVE=1", "CFO_TEST_UPDATE_SERVE_WAIT=8s", "CFO_TEST_HANDOVER_WAIT=2s")

			recovered, err := pasted.CombinedOutput()

			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != updateRolledBack {
				t.Fatalf("the pasted line %s ended %v, want exit %d:\n%s", line, err, updateRolledBack, recovered)
			}
			u.previousServes()
			if _, err := os.Stat(filepath.Join(elsewhere, "my-home")); !os.IsNotExist(err) {
				t.Errorf("the recovery reached %s, the place the relative name means from the folder it was pasted in (%v)", filepath.Join(elsewhere, "my-home"), err)
			}
		})
	}
}

// The seam that holds an alias through a rollback holds it for certain or
// fails: it waits out another handle that still has the file, as Windows can
// for a candidate's image just after its process exits, and once it holds
// the file no other open succeeds; a file never let go fails it, so a test
// can never pass without the condition it meant to set up.
func TestTheHoldSeamHoldsTheAliasOrFails(t *testing.T) {
	for _, test := range []struct {
		name      string
		releaseIn time.Duration
		isHeld    bool
	}{
		{"another handle lets go in time", 300 * time.Millisecond, true},
		{"another handle never lets go", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "goblins.exe")
			if err := os.WriteFile(path, []byte("an alias"), 0o755); err != nil {
				t.Fatal(err)
			}
			other, err := holdExclusively(path, 0)
			if err != nil {
				t.Fatal(err)
			}
			released := make(chan struct{})
			go func() {
				defer close(released)
				if test.isHeld {
					time.Sleep(test.releaseIn)
				} else {
					time.Sleep(1500 * time.Millisecond)
				}
				_ = syscall.CloseHandle(other)
			}()

			held, err := holdExclusively(path, time.Second)
			<-released

			if !test.isHeld {
				if err == nil {
					_ = syscall.CloseHandle(held)
					t.Fatal("the seam reported holding a file another handle never let go")
				}
				if !errors.Is(err, errorSharingViolation) {
					t.Fatalf("the seam failed with %v, want a sharing violation", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("the seam did not hold a file let go in time: %v", err)
			}
			defer syscall.CloseHandle(held)
			if third, err := holdExclusively(path, 0); err == nil {
				_ = syscall.CloseHandle(third)
				t.Fatal("the premise failed: the held file opened again")
			}
		})
	}
}

// writeWatchLock writes holder as this home's watcher lock record, as a
// holder of it would.
func writeWatchLock(t *testing.T, stateDir string, holder lock.Info) {
	t.Helper()
	data, err := json.Marshal(holder)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, ".watch.lock"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Before an update or a recovery changes anything it reads the watcher lock
// as the lock package does: a supervisor that may still run but cannot be
// verified, as one on another host is, and a record the system cannot read
// are refused; a record whose content stays empty or malformed through the
// lock's grace is a crash orphan serve reclaims, and is passed over, as are a
// supervisor proved to have ended and a pid now another process's; a watcher
// is left to serve's handover.
func TestTheUpdatePreflightRefusesASupervisorItCannotVerify(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	started, ok := proc.StartTime(os.Getpid())
	if !ok {
		t.Fatal("no start time for this process")
	}
	ended := exec.Command("cmd", "/c", "exit 0")
	if err := ended.Run(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		record     []byte
		holder     *lock.Info
		isHeldOpen bool
		isRefused  bool
	}{
		{"no lock", nil, nil, false, false},
		{"malformed content through the grace", []byte("{not a lock record"), nil, false, false},
		{"an empty record through the grace", []byte{}, nil, false, false},
		{"a record the system cannot read", []byte("{not a lock record"), nil, true, true},
		{"a supervisor on another host", nil, &lock.Info{PID: os.Getpid(), OwnerPID: os.Getpid(), Session: "exclusive-spawn", Start: started, Hostname: "another-host"}, false, true},
		{"a supervisor that ended", nil, &lock.Info{PID: ended.Process.Pid, OwnerPID: ended.Process.Pid, Session: "exclusive-spawn", Start: time.Now().Add(-time.Minute), Hostname: hostname}, false, false},
		{"a pid now another process's", nil, &lock.Info{PID: os.Getpid(), OwnerPID: os.Getpid(), Session: "exclusive-spawn", Start: started.Add(-time.Hour), Hostname: hostname}, false, false},
		{"a watcher", nil, &lock.Info{PID: os.Getpid(), OwnerPID: os.Getpid(), Session: "watch", Start: started, Hostname: hostname}, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state")}
			if err := os.MkdirAll(h.State, 0o700); err != nil {
				t.Fatal(err)
			}
			if test.record != nil {
				if err := os.WriteFile(filepath.Join(h.State, ".watch.lock"), test.record, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.holder != nil {
				writeWatchLock(t, h.State, *test.holder)
			}
			if test.isHeldOpen {
				held, err := holdExclusively(filepath.Join(h.State, ".watch.lock"), 0)
				if err != nil {
					t.Fatal(err)
				}
				defer syscall.CloseHandle(held)
			}

			err := unprovedSupervisor(h)

			if test.isRefused && err == nil {
				t.Fatal("the preflight passed a lock it cannot verify")
			}
			if !test.isRefused && err != nil {
				t.Fatalf("the preflight refused: %v", err)
			}
		})
	}
}

// A supervisor on another host holding this home's lock refuses the update
// before it writes anything: no journal, no copies, the aliases as they were.
func TestAnUpdateRefusedByAnUnverifiableSupervisorChangesNothing(t *testing.T) {
	u := newUpdateHome(t, "previous", "candidate")
	started, ok := proc.StartTime(os.Getpid())
	if !ok {
		t.Fatal("no start time for this process")
	}
	writeWatchLock(t, u.state, lock.Info{PID: os.Getpid(), OwnerPID: os.Getpid(), Session: "exclusive-spawn", Start: started, Hostname: "another-host"})

	code, output := u.run(nil)

	if code != 1 {
		t.Fatalf("update exited %d, want 1:\n%s", code, output)
	}
	for _, name := range []string{"journal.json", "candidate.exe", "previous-cfo.exe", "previous-goblins.exe"} {
		if _, err := os.Stat(filepath.Join(update.Dir(u.state), name)); !os.IsNotExist(err) {
			t.Errorf("the refused update wrote %s (%v)", name, err)
		}
	}
	u.aliasesAre(u.previous, "previous")
}

// A watcher lock record left empty, as an unsynced write leaves it when the
// machine loses power just after a supervisor takes the lock, is a crash
// orphan the next supervisor's lock reclaims: it never blocks an update, nor
// the recovery of an update that ended at swapped.
func TestAnEmptyWatchLockRecordLeftByAPowerLossIsReclaimed(t *testing.T) {
	t.Run("install", func(t *testing.T) {
		u := newUpdateHome(t, "previous", "candidate")
		candidate, err := os.ReadFile(u.candidate)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(u.state, ".watch.lock"), nil, 0o600); err != nil {
			t.Fatal(err)
		}

		code, output := u.run(nil)

		if code != updateInstalled {
			t.Fatalf("update exited %d, want %d:\n%s", code, updateInstalled, output)
		}
		u.aliasesAre(candidate, "candidate")
		if err := boardAlive(context.Background(), u.awaitBoard()); err != nil {
			t.Fatalf("the candidate's supervisor does not answer as itself: %v", err)
		}
	})
	t.Run("recover after swapped", func(t *testing.T) {
		u := newUpdateHome(t, "previous", "candidate")
		u.serving()
		code, output := u.run([]string{"CFO_TEST_UPDATE_INTERRUPT=swapped"})
		if code != 9 {
			t.Fatalf("the update did not end at swapped (exit %d):\n%s", code, output)
		}
		journal, err := update.ReadJournal(u.state)
		if err != nil {
			t.Fatal(err)
		}
		u.candidate = journal.Copy
		if err := os.WriteFile(filepath.Join(u.state, ".watch.lock"), nil, 0o600); err != nil {
			t.Fatal(err)
		}

		code, output = u.run(nil, "--recover")

		if code != updateRolledBack {
			t.Fatalf("recover exited %d, want %d:\n%s", code, updateRolledBack, output)
		}
		u.previousServes()
	})
}

// A record still empty when first read but written within the lock's grace
// is judged by what it then says: a supervisor on another host is refused.
func TestThePreflightJudgesARecordWrittenWithinTheGrace(t *testing.T) {
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, ".watch.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	started, ok := proc.StartTime(os.Getpid())
	if !ok {
		t.Fatal("no start time for this process")
	}
	record, err := json.Marshal(lock.Info{PID: os.Getpid(), OwnerPID: os.Getpid(), Session: "exclusive-spawn", Start: started, Hostname: "another-host"})
	if err != nil {
		t.Fatal(err)
	}
	var writeErr error
	written := make(chan struct{})
	go func() {
		defer close(written)
		time.Sleep(60 * time.Millisecond)
		writeErr = os.WriteFile(filepath.Join(h.State, ".watch.lock"), record, 0o600)
	}()

	err = unprovedSupervisor(h)
	<-written

	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if err == nil || !strings.Contains(err.Error(), "another-host") {
		t.Fatalf("the preflight returned %v, want a refusal naming the supervisor on another-host", err)
	}
}

// A watcher lock record the system cannot read, held open by another handle
// that shares nothing, proves nothing about its holder: the update is
// refused before it writes anything.
func TestAnUpdateRefusesAWatchLockRecordItCannotRead(t *testing.T) {
	u := newUpdateHome(t, "previous", "candidate")
	path := filepath.Join(u.state, ".watch.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := holdExclusively(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(held)

	code, output := u.run(nil)

	if code != 1 {
		t.Fatalf("update exited %d, want 1:\n%s", code, output)
	}
	if !strings.Contains(output, "cannot be read") {
		t.Errorf("the refusal does not say the record cannot be read:\n%s", output)
	}
	for _, name := range []string{"journal.json", "candidate.exe", "previous-cfo.exe", "previous-goblins.exe"} {
		if _, err := os.Stat(filepath.Join(update.Dir(u.state), name)); !os.IsNotExist(err) {
			t.Errorf("the refused update wrote %s (%v)", name, err)
		}
	}
	u.aliasesAre(u.previous, "previous")
}

// The recovery line names the home, its exact state and the candidate's kept
// copy in that state, all from the home alone, as PowerShell literal strings,
// so a path with a quote or a space pastes as it is.
func TestRecoverCommandQuotesForPowerShell(t *testing.T) {
	root := `C:\Users\O'Brien\Code Goblins`
	for _, test := range []struct {
		name  string
		state string
		want  string
	}{
		{"the home's own state", root + `\state`, `$env:CFO_HOME = 'C:\Users\O''Brien\Code Goblins'; $env:CFO_STATE_OVERRIDE = 'C:\Users\O''Brien\Code Goblins\state'; & 'C:\Users\O''Brien\Code Goblins\state\update\candidate.exe' update --recover`},
		{"a state elsewhere", `D:\fleet's state`, `$env:CFO_HOME = 'C:\Users\O''Brien\Code Goblins'; $env:CFO_STATE_OVERRIDE = 'D:\fleet''s state'; & 'D:\fleet''s state\update\candidate.exe' update --recover`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := recoverCommand(home.Home{Root: root, State: test.state})

			if got != test.want {
				t.Fatalf("recoverCommand = %s\nwant            %s", got, test.want)
			}
		})
	}
}

// The line an unfinished update prints is the recovery: pasted into Windows
// PowerShell in another folder, with no CFO home in its environment, another
// fleet's state inherited in CFO_STATE_OVERRIDE, and both cfo.exe and
// goblins.exe gone, it puts the previous build back and its board serves. The
// home's path has a quote and a space in it.
func TestThePrintedRecoveryLineRecoversFromAnotherFolder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "O'Brien goblins")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	u := newUpdateHomeIn(t, root, "previous", "crash")
	u.serving()
	code, output := u.run([]string{"CFO_TEST_UPDATE_HOLD=goblins.exe"})
	if code != updateDegraded {
		t.Fatalf("update exited %d, want %d:\n%s", code, updateDegraded, output)
	}
	var line string
	printed := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for at := 0; at+1 < len(printed); at++ {
		if strings.HasSuffix(printed[at], "in Windows PowerShell:") {
			line = strings.TrimSpace(printed[at+1])
		}
	}
	if line == "" {
		t.Fatalf("the update printed no recovery line:\n%s", output)
	}
	for _, name := range update.Aliases {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	var environment []string
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if !strings.EqualFold(name, "CFO_HOME") && !strings.EqualFold(name, "CFO_STATE_OVERRIDE") && !strings.EqualFold(name, "CFO_TEST_UPDATE_ROOT") {
			environment = append(environment, variable)
		}
	}
	pasted := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", line+"; exit $LASTEXITCODE")
	pasted.Dir = t.TempDir()
	inherited := filepath.Join(t.TempDir(), "state")
	pasted.Env = append(environment, "CFO_STATE_OVERRIDE="+inherited, "CFO_TEST_UPDATE_RESOLVE=1", "CFO_TEST_UPDATE_SERVE_WAIT=8s", "CFO_TEST_HANDOVER_WAIT=2s")

	recovered, err := pasted.CombinedOutput()

	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != updateRolledBack {
		t.Fatalf("the pasted line %s ended %v, want exit %d:\n%s", line, err, updateRolledBack, recovered)
	}
	u.previousServes()
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
