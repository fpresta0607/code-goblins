package supervisor

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// watchLock is the singleton the supervisor and every watcher contend for.
const watchLock = ".watch.lock"

// legacyExitWait bounds the wait for the lock once a watcher that never
// answered the handover has been ended.
const legacyExitWait = 5 * time.Second

// takeOverWatcher takes the lock from a watcher the Stop hook hosts, or a cfo
// watch, when held says one holds it. The supervisor supersedes a watcher, so
// it asks the watcher to yield and waits for the lock; another supervisor
// holding it is refused as before, so two never run. On 2026-10-01 an install
// stopped serve, the CFO's Stop hook took the lock in the gap, and every new
// serve refused to start until the hook's window ended.
func takeOverWatcher(stateDir string, held error) error {
	holder, err := lock.ReadNamed(stateDir, watchLock)
	if !errors.Is(held, lock.ErrHeld) || err != nil || holder.Session != watch.WatcherSession {
		return held
	}
	withdraw, err := watch.RequestHandover(stateDir)
	if err != nil {
		return errors.Join(held, err)
	}
	defer withdraw()
	if err := acquireWithin(stateDir, watch.HandoverWait, true); err == nil || !errors.Is(err, lock.ErrHeld) {
		return err
	}
	if err := endLegacyWatcher(stateDir, *holder); err != nil {
		return err
	}
	return acquireWithin(stateDir, legacyExitWait, false)
}

// askAgainEvery is how often a serve waiting for the lock writes its request
// again. A watcher reads the request when a change to it ends its wait, so a
// request written before its wait began is seen only at the next write; and
// a serve started at the same moment can replace this one's request and then
// exit, leaving one from a process that has ended, which no watcher honours.
const askAgainEvery = time.Second

// acquireWithin takes the lock for this process, looking again while a
// watcher holds it, until wait runs out, and while asking, writes its request
// again every askAgainEvery. Once another supervisor holds it, a serve started
// at the same moment that won the handover, it stops at once: that one runs,
// and this one refuses as before.
func acquireWithin(stateDir string, wait time.Duration, asking bool) error {
	deadline := time.Now().Add(wait)
	askedAgain := time.Now()
	for {
		_, err := lock.AcquireExclusiveNamed(stateDir, watchLock)
		if err == nil || !errors.Is(err, lock.ErrHeld) || !time.Now().Before(deadline) {
			return err
		}
		if holder, readErr := lock.ReadNamed(stateDir, watchLock); readErr == nil && holder.Session != watch.WatcherSession {
			return err
		}
		if asking && time.Since(askedAgain) >= askAgainEvery {
			if _, err := watch.RequestHandover(stateDir); err != nil {
				return err
			}
			askedAgain = time.Now()
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// endLegacyWatcher ends the one watcher that held the lock through a whole
// handover wait without answering: a Stop hook started from a binary older
// than the handover never reads the request. It ends that process alone,
// never its parent or any terminal host, and only once it has proved the
// process is that watcher: the lock still names it as a watcher by pid, start
// time and host, and, through the one handle it is ended by, the process
// was created at that start time and runs as a watcher. A dead holder's lock
// is then free to take; pending wakes are in the durable queue, not the
// watcher.
func endLegacyWatcher(stateDir string, holder lock.Info) error {
	current, err := lock.ReadNamed(stateDir, watchLock)
	if err != nil {
		return err
	}
	if current.PID != holder.PID || !current.Start.Equal(holder.Start) || current.Session != watch.WatcherSession || !current.VerifiedAlive() {
		return fmt.Errorf("%w: pid %d holds the watcher lock as %q and is not the watcher that was asked to hand it over", lock.ErrHeld, current.PID, current.Session)
	}
	if err := proc.TerminateVerified(current.PID, current.Start, watcherRole); err != nil {
		return fmt.Errorf("%w: pid %d holds the watcher lock and was left running: %v", lock.ErrHeld, current.PID, err)
	}
	return nil
}

// watcherRole accepts the command line of a watcher: cfo, or its goblins
// alias, running the Stop hook's auto-arm or cfo watch. A cfo binary running
// as anything else, a terminal's host, serve or a gate's test, is not one.
func watcherRole(arguments []string) error {
	if len(arguments) > 0 {
		name := strings.TrimSuffix(strings.ToLower(filepath.Base(arguments[0])), ".exe")
		role := strings.Join(arguments[1:], " ")
		if (name == "cfo" || name == "goblins") && (role == "hook stop-autoarm" || role == "watch") {
			return nil
		}
	}
	return fmt.Errorf("it runs %q, not a watcher", strings.Join(arguments, " "))
}
