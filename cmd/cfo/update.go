package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// Exit codes of cfo update.
const (
	// updateInstalled: the candidate serves.
	updateInstalled = 0
	// updateRolledBack: the previous build was put back and serves.
	updateRolledBack = 3
	// updateBoardDown: neither build serves.
	updateBoardDown = 4
	// updateDegraded: the previous build serves from its verified copy,
	// but an alias still needs repair by update --recover.
	updateDegraded = 5
	// updateHomeIncomplete: the candidate serves, but the home's refresh failed.
	updateHomeIncomplete = 6
)

// Bounds on the steps of an update.
var (
	// updateStopWait is how long a supervisor asked to stop has before it
	// is ended.
	updateStopWait = 30 * time.Second
	// updateServeWait is how long a supervisor started has to serve,
	// including taking the watcher lock over from a watcher.
	updateServeWait = 90 * time.Second
	// updateServeTries is how often the previous build's supervisor is
	// started before the update gives up on it.
	updateServeTries = 3
)

// Seams a test ends the process at, or makes a journal write fail at, to
// leave an update interrupted exactly there.
var (
	updateInterrupt = func(step string) {}
	recordUpdate    = update.Record
)

// serveProcess is a supervisor by pid and start time, so it is never
// mistaken for a process that reused its pid.
type serveProcess struct {
	pid   int
	start time.Time
}

// runUpdate is cfo update, run by a verified candidate build to install it as
// this home's cfo.exe and goblins.exe, or with --recover to finish an update
// that ended part way by putting the previous build back. It restarts only
// the supervisor, never a goblin's or the CFO's terminal, and leaves a board
// answering either way. Run as the home's installed build it updates the home
// from the newest published release instead, which installs itself the same
// way.
func runUpdate(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("update", flag.ContinueOnError)
	f.SetOutput(stderr)
	recover := f.Bool("recover", false, "finish an update that ended part way by putting the previous build back")
	check := f.Bool("check", false, "say whether a newer release is published, and change nothing")
	to := f.String("to", "", "the release to update to, refused unless it is still the newest one published")
	pressed := f.String("run", "", "the Update item the Overlord pressed in the Command Center, whose grant this run takes in place of his terminal")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if h, err = pinHome(h); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := os.MkdirAll(update.Dir(h.State), 0o700); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	program, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	installed := !*recover && installedBuild(h, program)
	if !installed && (*check || *to != "" || *pressed != "") || *pressed != "" && (*to == "" || *check) {
		fmt.Fprintln(stderr, "cfo update: --check, --to and --run update a home from a release, so run them as the home's own goblins or cfo, and --run goes with --to")
		return 2
	}
	if installed {
		return releaseUpdate(h, *check, *to, *pressed, stdout, stderr)
	}
	// One update owns a home at a time, and its recovery the same.
	if _, err := lock.AcquireExclusiveNamed(update.Dir(h.State), ".lock"); err != nil {
		fmt.Fprintf(stderr, "cfo update: another update of this home is running: %v\n", err)
		return 1
	}
	defer lock.ReleaseExclusiveNamed(update.Dir(h.State), ".lock")

	if *recover {
		return recoverUpdate(h, stdout, stderr)
	}
	return installUpdate(h, stdout, stderr)
}

// installUpdate installs the running binary, a candidate build that is not
// one of the home's installed programs.
func installUpdate(h home.Home, stdout, stderr io.Writer) int {
	candidate, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// An earlier update that did not finish, or whose journal cannot be
	// read or is not this home's, is never overwritten: its verified copies
	// are the way back.
	switch last, err := update.ReadJournal(h.State); {
	case errors.Is(err, os.ErrNotExist):
	case err == nil && !sameHomePath(last.Root, h.Bin()) && last.Phase.Finished():
		fmt.Fprintf(stderr, "cfo update: the last update's journal is for the home %s, not this home %s; nothing was changed. If this home was moved, move the old journal in %s aside by hand.\n", last.Root, h.Bin(), update.Dir(h.State))
		return 1
	case err == nil && !sameHomePath(last.Root, h.Bin()):
		fmt.Fprintf(stderr, "cfo update: the last update's journal is for the home %s, not this home %s, and that update stopped at %s; nothing was changed, and its journal and copies in %s are kept as its way back\n", last.Root, h.Bin(), last.Phase, update.Dir(h.State))
		return 1
	default:
		if err == nil {
			err = update.Validate(last, h.Bin(), h.State)
		}
		if err != nil {
			fmt.Fprintf(stderr, "cfo update: the last update's journal cannot be trusted (%v); nothing was changed, and journal.json, candidate.exe and the previous-*.exe copies in %s are kept as they are\n", err, update.Dir(h.State))
			return 1
		}
		if !last.Phase.Finished() {
			fmt.Fprintf(stderr, "cfo update: an earlier update stopped at %s; finish it first in Windows PowerShell:\n  %s\n", last.Phase, recoverCommand(h))
			return 1
		}
	}
	// A home that keeps its binaries at its root predates bin: an update
	// there would have no installed build to back up, so it is laid out by
	// a move first.
	if _, err := os.Stat(filepath.Join(h.Bin(), "cfo.exe")); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "cfo update: %s holds no cfo.exe; a home a build before bin set up keeps its binaries elsewhere, so lay it out with this build's cfo home move first; nothing was changed\n", h.Bin())
		return 1
	}
	// The update stops only this home's own supervisor, so one it cannot
	// prove is refused before anything changes, never left serving behind a
	// rollback that cannot start the previous build.
	if err := unprovedSupervisor(h); err != nil {
		fmt.Fprintf(stderr, "cfo update: %v; an update stops only this home's own supervisor, so nothing was changed\n", err)
		return 1
	}

	journal, err := update.Prepare(h.Bin(), h.State, candidate)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	updateInterrupt("prepared")
	// An update trusts a build because the Overlord ran it; nothing here
	// checks who built it, so it says so and names what he can check.
	fmt.Fprintf(stdout, "Installing the build that runs this command, SHA-256 %s. An update checks no code signature, so the build is yours to check: compare that SHA-256 with the build you made or were given.\n", journal.Hash)
	fmt.Fprintf(stdout, "Prepared: the previous build is backed up in %s. If this update stops part way, this puts it back, pasted into Windows PowerShell:\n  %s\n", update.Dir(h.State), recoverCommand(h))

	// The supervisor is restarted on the address the one stopped here serves,
	// so a home on an address of its own keeps it whatever shell runs the
	// update.
	address := boardAddress()
	if running, ok := homeSupervisor(h.State); ok {
		if record, err := readBoardRecord(h.State); err == nil && record.PID == running.pid {
			address = strings.TrimPrefix(record.URL, "http://")
		}
		fmt.Fprintf(stdout, "Stopping the supervisor (pid %d).\n", running.pid)
		if err := endSupervisor(h, running); err != nil {
			return rollBack(h, journal, address, fmt.Errorf("stop the supervisor: %w", err), stdout, stderr)
		}
	}
	if err := recordUpdate(h.State, journal, update.Stopped, ""); err != nil {
		return rollBack(h, journal, address, err, stdout, stderr)
	}
	updateInterrupt("stopped")

	if err := update.Swap(journal); err != nil {
		return rollBack(h, journal, address, err, stdout, stderr)
	}
	if err := recordUpdate(h.State, journal, update.Swapped, ""); err != nil {
		return rollBack(h, journal, address, err, stdout, stderr)
	}
	updateInterrupt("swapped")

	started, err := startSupervisor(h, filepath.Join(h.Bin(), "goblins.exe"), address)
	if err != nil {
		return rollBack(h, journal, address, err, stdout, stderr)
	}
	updateInterrupt("started")
	journal.Attempts = append(journal.Attempts, update.Attempt{PID: started.pid, Start: started.start, Program: filepath.Join(h.Bin(), "goblins.exe")})
	if err := recordUpdate(h.State, journal, update.Swapped, ""); err != nil {
		return rollBack(h, journal, address, err, stdout, stderr)
	}
	if err := awaitSupervisor(h.State, started, true); err != nil {
		return rollBack(h, journal, address, err, stdout, stderr)
	}
	// A success the journal cannot record is not one: a later --recover
	// would put the previous build back over a build that serves, so the
	// update rolls back now instead.
	if err := recordUpdate(h.State, journal, update.Done, "the candidate serves"); err != nil {
		return rollBack(h, journal, address, fmt.Errorf("record the update as done: %w", err), stdout, stderr)
	}
	update.CleanUp(journal)
	for _, kept := range update.KeepRecent(h.Bin(), update.KeptBuilds) {
		fmt.Fprintf(stdout, "Kept %s: something still runs it, and the janitor removes it once nothing does.\n", kept)
	}
	fmt.Fprintf(stdout, "Updated: cfo.exe and goblins.exe in %s are %s, and its supervisor (pid %d) serves the board.\n", h.Bin(), journal.Hash, started.pid)
	// The desktop window beside the candidate follows the update into the
	// home. It takes no part in it: the window of the previous build shows
	// this build's board, so one that could not be replaced leaves the
	// update done.
	if err := install.CarryWindow(h.Root, candidate, stdout); err != nil {
		fmt.Fprintf(stderr, "cfo update: the update is done, but the desktop window in %s was not replaced, and the one there keeps working: %v\n", h.Bin(), err)
	}
	return updateInstalled
}

// recoverUpdate finishes an update that ended part way by putting the
// previous build back and starting its supervisor, deciding from what each
// alias holds rather than from how far the journal says it got. A journal it
// cannot read, or that is not this home's, changes nothing.
func recoverUpdate(h home.Home, stdout, stderr io.Writer) int {
	journal, err := update.ReadJournal(h.State)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stdout, "No update of the home %s to recover.\n", h.Root)
		return 0
	}
	if err == nil {
		err = update.Validate(journal, h.Bin(), h.State)
	}
	if err != nil {
		fmt.Fprintf(stderr, "cfo update: the update's journal cannot be trusted (%v); nothing was changed\n", err)
		return 1
	}
	if journal.Phase.Finished() {
		fmt.Fprintf(stdout, "The last update of this home finished: %s.\n", journal.Outcome)
		return 0
	}
	if err := unprovedSupervisor(h); err != nil {
		fmt.Fprintf(stderr, "cfo update: %v; recovery stops only this home's own supervisor, so nothing was changed. Stop it (goblins stop), then run the recovery line again in Windows PowerShell:\n  %s\n", err, recoverCommand(h))
		return 1
	}
	// The previous build starts on the address this home's board record
	// names, the one it last served on whether or not that supervisor still
	// answers, and on the board's own only when no record can be read.
	address := boardAddress()
	if record, err := readBoardRecord(h.State); err == nil {
		address = strings.TrimPrefix(record.URL, "http://")
	}
	return rollBack(h, &journal, address, fmt.Errorf("the update stopped at %s", journal.Phase), stdout, stderr)
}

// rollBack puts the previous build back and starts its supervisor on
// address, and says whether the board serves. The verified backups are copied
// back, never moved, so a rollback that stops part way can run again.
func rollBack(h home.Home, journal *update.Journal, address string, cause error, stdout, stderr io.Writer) int {
	fmt.Fprintf(stderr, "cfo update: %v; putting the previous build back\n", cause)
	_ = recordUpdate(h.State, journal, update.RollingBack, cause.Error())
	updateInterrupt("rolling-back")
	endHomeSupervisors(h, journal, stderr)
	restoreErr := update.Restore(journal)
	if restoreErr != nil {
		fmt.Fprintf(stderr, "cfo update: %v; starting the previous build from a copy proved to be it\n", restoreErr)
	}
	// A supervisor that could not be stopped still serves; it is the
	// rollback's result only when the program it runs now is proved the
	// previous build, never a candidate answering from its moved-aside file.
	if running, ok := homeSupervisor(h.State); ok && restoreErr == nil && runsPreviousBuild(running, journal) && serves(h.State, running, false) == nil {
		return rolledBack(h, journal, running, "the previous build's supervisor still serves after: "+cause.Error(), stdout, stderr)
	}
	var lastErr error
	for try := 0; try < updateServeTries; try++ {
		program, held, err := previousProgram(h, journal)
		if err != nil {
			lastErr = err
			break
		}
		started, err := startPreviousSupervisor(h, program, address)
		held.Close()
		if err == nil {
			journal.Attempts = append(journal.Attempts, update.Attempt{PID: started.pid, Start: started.start, Program: program})
			_ = recordUpdate(h.State, journal, update.RollingBack, "")
			if err = awaitSupervisor(h.State, started, false); err == nil {
				if restoreErr != nil {
					_ = recordUpdate(h.State, journal, update.Degraded, fmt.Sprintf("the previous build serves from %s (pid %d); the aliases still need repair: %v", program, started.pid, restoreErr))
					fmt.Fprintf(stderr, "cfo update: the previous build serves from %s (pid %d), but cfo.exe and goblins.exe are not repaired yet; repair them in Windows PowerShell:\n  %s\n", program, started.pid, recoverCommand(h))
					return updateDegraded
				}
				return rolledBack(h, journal, started, "the previous build serves again after: "+cause.Error(), stdout, stderr)
			}
			// A supervisor that runs but does not serve keeps the lock
			// from the next try: end it, and only it.
			if endErr := endSupervisor(h, started); endErr != nil {
				err = errors.Join(err, endErr)
			}
		}
		lastErr = err
	}
	_ = recordUpdate(h.State, journal, update.RollingBack, "the previous build did not serve: "+lastErr.Error())
	fmt.Fprintf(stderr, "cfo update: THE BOARD IS DOWN: neither build serves (%v). Run this again in Windows PowerShell, or goblins --board:\n  %s\n", lastErr, recoverCommand(h))
	return updateBoardDown
}

// rolledBack finishes a rollback whose previous build serves: it records the
// update as rolled back and only then removes what it moved aside. A record
// that fails keeps every file, so update --recover can still finish it.
func rolledBack(h home.Home, journal *update.Journal, running serveProcess, outcome string, stdout, stderr io.Writer) int {
	if err := recordUpdate(h.State, journal, update.RolledBack, outcome); err != nil {
		fmt.Fprintf(stderr, "cfo update: the previous build serves the board (pid %d), but the update's journal could not record the rollback (%v), so the update is not finished; finish it in Windows PowerShell:\n  %s\n", running.pid, err, recoverCommand(h))
		return updateRolledBack
	}
	update.CleanUp(journal)
	fmt.Fprintf(stdout, "Rolled back: the previous build serves the board (pid %d).\n", running.pid)
	return updateRolledBack
}

// resolvedFrom is path as a process working in folder resolves it: an
// absolute path as it is, a rooted one on that folder's drive, and any other
// relative to the folder.
func resolvedFrom(folder, path string) string {
	switch {
	case filepath.IsAbs(path):
		return path
	case strings.HasPrefix(path, `\`) || strings.HasPrefix(path, "/"):
		return filepath.VolumeName(folder) + path
	default:
		return filepath.Join(folder, path)
	}
}

// pinHome makes the home's root and state absolute against this process's
// folder and names both in this process's environment, which every
// supervisor it starts inherits. A relative CFO_HOME or CFO_STATE_OVERRIDE
// resolves against each process's own folder, and those supervisors run in
// the home's root, not here: pinned, the update, its supervisors, its journal
// and its recovery line all mean the one home and state.
func pinHome(h home.Home) (home.Home, error) {
	var err error
	if h.Root, err = filepath.Abs(h.Root); err != nil {
		return h, err
	}
	if h.State, err = filepath.Abs(h.State); err != nil {
		return h, err
	}
	if err := os.Setenv("CFO_HOME", h.Root); err != nil {
		return h, err
	}
	return h, os.Setenv("CFO_STATE_OVERRIDE", h.State)
}

// recoverCommand is the update --recover line to paste into Windows
// PowerShell: it runs the candidate's kept copy, so it works with cfo.exe and
// goblins.exe both gone, and names this home and its exact state itself, so
// it works from any folder whatever state the shell's environment names. It
// is built from the home alone, never from a path a journal holds.
func recoverCommand(h home.Home) string {
	return "$env:CFO_HOME = " + powerShellQuote(h.Root) + "; $env:CFO_STATE_OVERRIDE = " + powerShellQuote(h.State) + "; & " + powerShellQuote(filepath.Join(update.Dir(h.State), "candidate.exe")) + " update --recover"
}

// powerShellQuote is text as a PowerShell literal string, which expands
// nothing and escapes only its quote.
func powerShellQuote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", "''") + "'"
}

// previousProgram is the program the previous build's supervisor starts
// from: goblins.exe once it is put back, or, while it cannot be, a verified
// copy, which runs the previous build as well as the alias would. Each is
// hashed through a handle that keeps it from changing until it is closed, and
// the caller closes it only once the supervisor has started, so a program
// whose content is not the previous build never runs; with none proved,
// nothing is started.
func previousProgram(h home.Home, journal *update.Journal) (string, *os.File, error) {
	programs := []string{filepath.Join(h.Bin(), "goblins.exe")}
	for _, alias := range journal.Aliases {
		programs = append(programs, alias.Backup)
	}
	for _, program := range programs {
		held, hash, err := update.Hold(program)
		if err != nil {
			continue
		}
		if isPreviousBuild(hash, journal) {
			return program, held, nil
		}
		held.Close()
	}
	return "", nil, errors.New("no copy of the previous build still hashes to the build it replaced, so none was started")
}

func isPreviousBuild(hash string, journal *update.Journal) bool {
	for _, alias := range journal.Aliases {
		if hash == alias.Previous {
			return true
		}
	}
	return false
}

// endHomeSupervisors ends every supervisor this update may have started or
// left: those the journal recorded, the one holding this home's watcher
// lock, and any started since the update began from this home's programs,
// such as one started just before the process running the update ended.
func endHomeSupervisors(h home.Home, journal *update.Journal, stderr io.Writer) {
	var supervisors []serveProcess
	for _, attempt := range journal.Attempts {
		// An attempt is trusted only as far as it names this home's
		// program, started during this update; ending it proves the rest.
		if homeProgram(h, attempt.Program) && !attempt.Start.Before(journal.Started.Add(-time.Second)) {
			supervisors = append(supervisors, serveProcess{pid: attempt.PID, start: attempt.Start})
		}
	}
	if running, ok := homeSupervisor(h.State); ok {
		supervisors = append(supervisors, running)
	}
	supervisors = append(supervisors, homeServesSince(h, journal.Started)...)
	ended := map[int]bool{}
	for _, running := range supervisors {
		if ended[running.pid] || !processIs(running) {
			continue
		}
		ended[running.pid] = true
		if err := endSupervisor(h, running); err != nil {
			fmt.Fprintf(stderr, "cfo update: %v\n", err)
		}
	}
}

// homeSupervisor is the supervisor holding this home's watcher lock, proved
// by the lock record's pid, start time and host.
func homeSupervisor(stateDir string) (serveProcess, bool) {
	holder, err := lock.ReadNamed(stateDir, ".watch.lock")
	if err != nil || holder.Session != "exclusive-spawn" || holder.PID <= 0 || !holder.VerifiedAlive() {
		return serveProcess{}, false
	}
	return serveProcess{pid: holder.PID, start: holder.Start}, true
}

// homeServesSince finds supervisors running from this home's programs, the
// aliases or the update's verified copies, started at since or later.
func homeServesSince(h home.Home, since time.Time) []serveProcess {
	processes, err := proc.Processes()
	if err != nil {
		return nil
	}
	var found []serveProcess
	for _, process := range processes {
		if !homeImages[strings.ToLower(process.ExeBase)] {
			continue
		}
		start, ok := proc.StartTime(process.PID)
		if !ok || start.Before(since.Add(-time.Second)) {
			continue
		}
		identity, err := proc.Identify(process.PID, start)
		if err != nil || homeServe(h)(identity) != nil {
			continue
		}
		found = append(found, serveProcess{pid: process.PID, start: start})
	}
	return found
}

// homeImages are the program names a supervisor of a home runs as.
var homeImages = map[string]bool{"cfo.exe": true, "goblins.exe": true, "previous-cfo.exe": true, "previous-goblins.exe": true}

// homeServe accepts a process that is this home's supervisor: a program of
// this home, by the image it has loaded, running serve for this home as
// home.Resolve finds one, from CFO_HOME or else the per-user home its
// LOCALAPPDATA names, with CFO_STATE_OVERRIDE or else the home's state. A
// supervisor can run from any folder, and the fleet runs scratch supervisors
// from the same binary for other homes and states, none of them this home's.
func homeServe(h home.Home) func(proc.Identity) error {
	return func(identity proc.Identity) error {
		if err := serveRole(identity.Arguments); err != nil {
			return err
		}
		if !homeProgram(h, identity.Image) {
			return fmt.Errorf("it runs %s, not a program of this home", identity.Image)
		}
		// Its home and state are where its own file operations land: a
		// relative name resolves against its own working folder.
		root := identity.Getenv("CFO_HOME")
		if root == "" {
			local := identity.Getenv("LOCALAPPDATA")
			if local == "" {
				return errors.New("it names no home: neither CFO_HOME nor LOCALAPPDATA is set in its environment")
			}
			root = filepath.Join(resolvedFrom(identity.Directory, local), "CodeGoblins")
		} else {
			root = resolvedFrom(identity.Directory, root)
		}
		state := identity.Getenv("CFO_STATE_OVERRIDE")
		if state == "" {
			state = filepath.Join(root, "state")
		} else {
			state = resolvedFrom(identity.Directory, state)
		}
		if !sameHomePath(root, h.Root) || !sameHomePath(state, h.State) {
			return fmt.Errorf("it serves the home %s with state %s, not this one", root, state)
		}
		return nil
	}
}

func sameHomePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// runsPreviousBuild reports whether the program the supervisor runs now, read
// from its process, hashes to the build the update replaced.
func runsPreviousBuild(running serveProcess, journal *update.Journal) bool {
	identity, err := proc.Identify(running.pid, running.start)
	if err != nil {
		return false
	}
	hash, err := update.HashFile(identity.Image)
	return err == nil && isPreviousBuild(hash, journal)
}

// homeProgram reports whether program is one of this home's: an alias in its
// bin, one at its root, where an older install put them and an install keeps
// them current, or a verified copy an update keeps.
func homeProgram(h home.Home, program string) bool {
	directory := filepath.Clean(filepath.Dir(program))
	for _, folder := range []string{h.Bin(), h.Root, update.Dir(h.State)} {
		if strings.EqualFold(directory, filepath.Clean(folder)) {
			return true
		}
	}
	return false
}

// endSupervisor asks the supervisor to stop and waits for it to exit,
// ending it, and it alone, when it does not.
func endSupervisor(h home.Home, running serveProcess) error {
	if !processIs(running) {
		return nil
	}
	if err := provedHomeSupervisor(h, running); err != nil {
		return fmt.Errorf("pid %d is not proved this home's supervisor (%v), so it was left running", running.pid, err)
	}
	if err := supervisor.RequestStop(h.State, running.pid); err != nil {
		return err
	}
	if awaitExit(running, updateStopWait) {
		return nil
	}
	if err := proc.TerminateVerifiedIn(running.pid, running.start, homeServe(h)); err != nil && processIs(running) {
		return fmt.Errorf("the supervisor (pid %d) did not stop and could not be ended: %w", running.pid, err)
	}
	if !awaitExit(running, 10*time.Second) {
		return fmt.Errorf("the supervisor (pid %d) still runs", running.pid)
	}
	return nil
}

// unprovedSupervisor says why this home's watcher lock may be held by a
// supervisor this update cannot prove is this home's, or nothing when the
// lock is free, is held by a watcher (which a supervisor takes over), names a
// supervisor proved to have ended or a pid now another process's, or is held
// by a supervisor proved this home's. It reads the lock as the lock package
// does: a holder that may still run but cannot be verified, as one on another
// host or one this user cannot inspect is, counts as running; content that
// stays empty or malformed through the grace lock.acquire gives a holder
// mid-write (3 reads over 150 ms) is a crash orphan the next supervisor's lock
// reclaims; and a record the system cannot read proves nothing either way.
func unprovedSupervisor(h home.Home) error {
	var holder *lock.Info
	var err error
	var unread *os.PathError
	for reads, first := 1, time.Now(); ; reads++ {
		holder, err = lock.ReadNamed(h.State, ".watch.lock")
		if err == nil || errors.As(err, &unread) || reads >= 3 && time.Since(first) >= 150*time.Millisecond {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if unread != nil {
		return fmt.Errorf("the watcher lock's record cannot be read (%v), so who holds it is not proved", err)
	}
	if err != nil {
		return nil
	}
	if holder.Session != "exclusive-spawn" || !holder.Alive() {
		return nil
	}
	if !holder.VerifiedAlive() {
		return fmt.Errorf("the watcher lock is held by pid %d on %q, which may still be running but cannot be verified as this home's supervisor", holder.PID, holder.Hostname)
	}
	if err := provedHomeSupervisor(h, serveProcess{pid: holder.PID, start: holder.Start}); err != nil {
		return fmt.Errorf("the watcher lock is held by pid %d, which is not proved this home's supervisor (%v)", holder.PID, err)
	}
	return nil
}

// provedHomeSupervisor proves the running process this home's supervisor by
// the identity it runs with.
func provedHomeSupervisor(h home.Home, running serveProcess) error {
	identity, err := proc.Identify(running.pid, running.start)
	if err != nil {
		return err
	}
	return homeServe(h)(identity)
}

// serveRole accepts the command line of a supervisor: cfo or goblins, or an
// update's verified copy of them, running serve.
func serveRole(arguments []string) error {
	if len(arguments) > 1 {
		name := strings.TrimSuffix(strings.ToLower(filepath.Base(arguments[0])), ".exe")
		if (name == "cfo" || name == "goblins" || name == "previous-cfo" || name == "previous-goblins") && arguments[1] == "serve" {
			return nil
		}
	}
	return fmt.Errorf("it runs %q, not cfo serve", strings.Join(arguments, " "))
}

// processIs reports whether the process still runs as itself: not ended, and
// created at its start time. Windows keeps an ended process's start time
// readable while any handle to it stays open, so the lock's identity proof,
// which reads the exit status first, decides.
func processIs(running serveProcess) bool {
	hostname, _ := os.Hostname()
	return (&lock.Info{PID: running.pid, Start: running.start, Hostname: hostname}).VerifiedAlive()
}

func awaitExit(running serveProcess, wait time.Duration) bool {
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if !processIs(running) {
			return true
		}
	}
	return !processIs(running)
}

// startSupervisor starts serve from program on address, detached, as goblins
// does.
func startSupervisor(h home.Home, program, address string) (serveProcess, error) {
	var command *exec.Cmd
	var err error
	// serve.log can still be held for a moment by the console of the
	// supervisor just stopped.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		command, err = startDetached(program, h.Root, serveLogPath(h.State), "serve", "--listen", address)
		if err == nil || !errors.Is(err, errorSharingViolation) || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		return serveProcess{}, fmt.Errorf("start the supervisor from %s: %w", program, err)
	}
	go func() { _ = command.Wait() }()
	start, ok := proc.StartTime(command.Process.Pid)
	if !ok {
		return serveProcess{}, fmt.Errorf("the supervisor started from %s exited at once", program)
	}
	return serveProcess{pid: command.Process.Pid, start: start}, nil
}

// startPreviousSupervisor starts the previous build's supervisor. A build
// older than the handover cannot take the watcher lock from a watcher, so the
// update takes the lock over from one first and releases it just before that
// supervisor starts. The CFO's Stop hook can take it again in that gap; the
// rollback's updateServeTries cover a start lost to it.
func startPreviousSupervisor(h home.Home, program, address string) (serveProcess, error) {
	if err := supervisor.AcquireWatchLock(h.State); err != nil {
		return serveProcess{}, fmt.Errorf("free the watcher lock for the previous build: %w", err)
	}
	if err := supervisor.ReleaseWatchLock(h.State); err != nil {
		return serveProcess{}, err
	}
	return startSupervisor(h, program, address)
}

// awaitSupervisor waits until the supervisor started serves as itself: it
// holds the watcher lock by its own pid and start time, the board record
// names it, and its board answers. A build with /api/alive must answer it
// with its pid; the previous build, which may be older than that route, must
// answer its board page. A supervisor that exits fails at once.
func awaitSupervisor(stateDir string, started serveProcess, alive bool) error {
	deadline := time.Now().Add(updateServeWait)
	for {
		if !processIs(started) {
			return fmt.Errorf("the supervisor (pid %d) exited before it served; see %s", started.pid, serveLogPath(stateDir))
		}
		if serves(stateDir, started, alive) == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the supervisor (pid %d) did not serve within %s", started.pid, updateServeWait)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// serves reports whether the supervisor serves as itself now.
func serves(stateDir string, running serveProcess, alive bool) error {
	holder, err := lock.ReadNamed(stateDir, ".watch.lock")
	if err != nil {
		return err
	}
	record, err := readBoardRecord(stateDir)
	if err != nil {
		return err
	}
	if holder.PID != running.pid || holder.Session != "exclusive-spawn" || record.PID != running.pid {
		return fmt.Errorf("the supervisor (pid %d) does not hold the watcher lock and the board record", running.pid)
	}
	if start := holder.Start.Sub(running.start); start <= -time.Second || start >= time.Second {
		return fmt.Errorf("the watcher lock names another process with pid %d", running.pid)
	}
	return boardAnswers(record, alive)
}

// boardAnswers checks the board record's address answers: with /api/alive
// naming its pid, or, for a build that may predate that route, with its
// board page.
func boardAnswers(record boardRecord, alive bool) error {
	if alive {
		return boardAlive(context.Background(), record)
	}
	ctx, cancel := context.WithTimeout(context.Background(), aliveTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, record.URL+"/", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("the board at %s answered HTTP %d", record.URL, response.StatusCode)
	}
	return nil
}
