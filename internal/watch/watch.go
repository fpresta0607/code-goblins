// Package watch implements the triage loop: a one-shot cycle that holds a
// singleton lock, scans state/ for changed status files, coalesces signals
// within a grace window, emits heartbeats with exponential backoff, and
// closes on the first actionable event. One actionable reason closes one
// watcher cycle; continuity across cycles is the arm layer's job (Task 11's
// stop-autoarm hook, which hosts this loop in-process), never the
// watcher's, so Run never wraps itself in an outer loop.
package watch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/claudehook"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const (
	watchLockName       = ".watch.lock"
	watcherBeatFile     = ".last-watcher-beat"
	lastHeartbeatFile   = ".last-heartbeat"
	heartbeatStreakFile = ".heartbeat-streak"
	seenPrefix          = ".seen-"

	// maxHeartbeatStreak bounds the value ConfigFromEnv's caller-independent
	// backoff shift (Heartbeat << streak) ever sees, so a streak file grown
	// past this by hand or by many quiet cycles can never overflow the shift.
	maxHeartbeatStreak = 8
)

// Config carries watch.Run's tunables and its two injection seams. Every
// timing decision in the loop reads a beacon's mtime against the wall clock
// (time.Now): state/.last-watcher-beat and state/.last-heartbeat are read by
// supervise.WatcherHealthy (a different package, a different process) and by
// Run itself across restarts, so a beacon stamped from an injected clock
// would make a healthy watcher read as stale to that outside reader. Sleep
// is therefore the loop's only timing seam; tests move time on the beacons
// directly with os.Chtimes instead. WaitEvent and Cleanup default to nil,
// meaning pure timer mode with nothing to release, until Task 9 supplies
// both for filesystem notifications.
type Config struct {
	Home         home.Home
	Poll         time.Duration
	SignalGrace  time.Duration
	Heartbeat    time.Duration
	HeartbeatMax time.Duration
	Sleep        func(time.Duration)
	WaitEvent    func(timeout time.Duration) bool
	Cleanup      func()
}

// ConfigFromEnv fills Config from the timing env vars (internal/claudehook),
// clamping every interval to at least 1s. claudehook.Seconds ships with no
// clamp of its own: CFO_POLL=0 would spin the loop rewriting signatures and
// touching the beat, and CFO_POLL=-5 converts to roughly 49 days of wait so
// the watcher never beats and supervise.WatcherHealthy reads it as stale
// against a live process. The clamp belongs at this consumer; Task 2, which
// shipped claudehook.Seconds, is not reopened for it.
func ConfigFromEnv(h home.Home) Config {
	return Config{
		Home:         h,
		Poll:         clampMin1s(claudehook.Seconds("CFO_POLL", 15)),
		SignalGrace:  clampMin1s(claudehook.Seconds("CFO_SIGNAL_GRACE", 30)),
		Heartbeat:    clampMin1s(claudehook.Seconds("CFO_HEARTBEAT", 600)),
		HeartbeatMax: clampMin1s(claudehook.Seconds("CFO_HEARTBEAT_MAX", 7200)),
		Sleep:        time.Sleep,
	}
}

func clampMin1s(d time.Duration) time.Duration {
	if d < time.Second {
		return time.Second
	}
	return d
}

// Change is one *.status or *.turn-ended file ScanSignals found moved
// against its persisted signature.
type Change struct {
	Name string
	Sig  string
}

// Sanitize maps every character outside [A-Za-z0-9_-] to '_', covering ':'
// '/' '\' '.' which are illegal or ambiguous in NTFS filenames. The mapping
// is deliberately lossy and collision-prone in principle, which is safe here
// because a collision only merges two signatures and the worst consequence
// is a duplicate wake that wake.Deduped folds away. Never widen it to
// preserve dots: a signature file for "a.status" written as ".seen-a.status"
// would itself end in ".status" and be indistinguishable from a hidden
// status file the next scan picks up.
func Sanitize(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// ScanSignals is a pure read: it compares each *.status and *.turn-ended
// file directly inside stateDir against the persisted size:mtime signature
// in state/.seen-<Sanitize(name)> and returns the entries whose signature
// moved, in filename order. It writes nothing anywhere; a file with no
// signature file yet counts as changed, so a first sighting is reported the
// same as any later change (the cross-restart contract: signals that land
// while no watcher runs are caught on the next start).
func ScanSignals(stateDir string) ([]Change, error) {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var changes []Change
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".status") && !strings.HasSuffix(name, ".turn-ended") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		sig := fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())

		seen, err := os.ReadFile(filepath.Join(stateDir, seenPrefix+Sanitize(name)))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if string(seen) == sig {
			continue
		}
		changes = append(changes, Change{Name: name, Sig: sig})
	}
	return changes, nil
}

// CommitSignatures writes the persisted signature for each change. Detection
// (ScanSignals) and commitment are separate so a watcher killed inside the
// SignalGrace window, or any error return before the wake.Append that must
// precede a commit, re-reports the same signal on the next start instead of
// swallowing it permanently: a crash between Append and CommitSignatures
// costs a duplicate wake, which wake.Deduped folds away, rather than a lost
// one.
func CommitSignatures(stateDir string, changes []Change) error {
	for _, c := range changes {
		path := filepath.Join(stateDir, seenPrefix+Sanitize(c.Name))
		if err := fsx.AtomicWriteFile(path, []byte(c.Sig)); err != nil {
			return err
		}
	}
	return nil
}

// touchBeacon stamps path with the current wall-clock time, creating an
// empty file if it does not exist yet.
func touchBeacon(path string) error {
	now := time.Now()
	err := os.Chtimes(path, now, now)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, nil, 0o644)
}

// ensureBeacon creates an empty path if it does not exist yet, and otherwise
// leaves it untouched: a missing state/.last-heartbeat is created at Run
// start so the heartbeat cadence has something to measure against, but an
// existing one must survive Run start unmodified or the persisted cadence
// (which is the whole point of stamping this file across watcher exits)
// would reset every time the watcher restarts.
func ensureBeacon(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, nil, 0o644)
}

// readHeartbeatStreak reads state/.heartbeat-streak, clamped to 0..8 so the
// backoff shift (Heartbeat << streak) can never overflow. A missing or
// unreadable-as-an-integer file reads as 0, matching a fresh or hand-edited
// home rather than erroring inside a loop that must not get stuck.
func readHeartbeatStreak(stateDir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, heartbeatStreakFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if convErr != nil || n < 0 {
		return 0, nil
	}
	if n > maxHeartbeatStreak {
		return maxHeartbeatStreak, nil
	}
	return n, nil
}

func writeHeartbeatStreak(stateDir string, n int) error {
	return fsx.AtomicWriteFile(filepath.Join(stateDir, heartbeatStreakFile), []byte(strconv.Itoa(n)+"\n"))
}

func removeHeartbeatStreak(stateDir string) error {
	err := os.Remove(filepath.Join(stateDir, heartbeatStreakFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// heartbeatDue reports whether the heartbeat at heartbeatPath has aged past
// its current backoff interval, and the streak that interval was computed
// from (so a caller that closes on it can increment the same value it read).
func heartbeatDue(stateDir, heartbeatPath string, heartbeat, max time.Duration) (due bool, streak int, err error) {
	streak, err = readHeartbeatStreak(stateDir)
	if err != nil {
		return false, 0, err
	}
	interval := heartbeat << streak
	if interval > max {
		interval = max
	}
	fi, err := os.Stat(heartbeatPath)
	if err != nil {
		return false, streak, err
	}
	return time.Since(fi.ModTime()) >= interval, streak, nil
}

// Run acquires the state/.watch.lock singleton, then loops: touch the
// watcher beat, scan for changed status files, and check the heartbeat
// cadence, waiting between checks. It returns the first actionable reason
// (a "signal:..." detail or "heartbeat") and closes; one actionable reason
// closes one watcher cycle, and continuity is the arm layer's job.
func Run(cfg Config) (reason string, err error) {
	if _, err := lock.AcquireNamedOwner(cfg.Home.State, watchLockName, os.Getpid(), "watch"); err != nil {
		return "", fmt.Errorf("watch: acquire singleton: %w", err)
	}
	// LIFO: this defer is registered first and so runs SECOND (after
	// Cleanup below), releasing the singleton only once Cleanup (Task 9's
	// filesystem watch teardown) has already run.
	defer lock.ReleaseNamed(cfg.Home.State, watchLockName)
	if cfg.Cleanup != nil {
		defer cfg.Cleanup()
	}

	beatPath := filepath.Join(cfg.Home.State, watcherBeatFile)
	heartbeatPath := filepath.Join(cfg.Home.State, lastHeartbeatFile)

	// A fresh home's heartbeat becomes due one interval after this touch,
	// never at t=0: ensureBeacon only creates the beacon when it is absent,
	// leaving an existing one (and the cadence it carries across restarts)
	// untouched.
	if err := ensureBeacon(heartbeatPath); err != nil {
		return "", err
	}

	for {
		if err := touchBeacon(beatPath); err != nil {
			return "", err
		}

		changes, err := ScanSignals(cfg.Home.State)
		if err != nil {
			return "", err
		}
		if len(changes) > 0 {
			// Linger one grace period so a crewmate's final status write and
			// the same turn's turn-end marker land as one wake, not two.
			cfg.Sleep(cfg.SignalGrace)

			// Nothing was committed between the two scans, so this rescan
			// already returns the union of both: any file the first scan
			// saw is still unequal to its (still uncommitted) persisted
			// signature here, and any file that changed only during the
			// grace window is picked up fresh. No separate coalescing step
			// is needed.
			changes, err = ScanSignals(cfg.Home.State)
			if err != nil {
				return "", err
			}

			names := make([]string, len(changes))
			for i, c := range changes {
				names[i] = c.Name
			}
			detail := "signal:" + strings.Join(names, " ")

			// One record per changed file: a single record keyed on an
			// arbitrary member of the changed set would let a later cycle's
			// append overwrite an earlier cycle's mention of a file whose
			// signature has already advanced, so that file's completion
			// would never be presented and would then be acked away.
			for _, c := range changes {
				if _, err := wake.Append(cfg.Home.State, "signal", c.Name, detail); err != nil {
					return "", err
				}
			}
			if err := CommitSignatures(cfg.Home.State, changes); err != nil {
				return "", err
			}
			if err := touchBeacon(heartbeatPath); err != nil {
				return "", err
			}
			if err := removeHeartbeatStreak(cfg.Home.State); err != nil {
				return "", err
			}
			if _, err := wake.PublishEpisode(cfg.Home.State); err != nil {
				return "", err
			}
			return detail, nil
		}

		// NOT PORTED IN V1: upstream's stale-pane sweep (window/pane
		// staleness against Herdr's tracking) is deferred to Plan 3.
		// NOT PORTED IN V1: upstream's *.check.sh sweep (goblin health
		// checks / process-event delivery) is deferred to Plan 4.

		due, streak, err := heartbeatDue(cfg.Home.State, heartbeatPath, cfg.Heartbeat, cfg.HeartbeatMax)
		if err != nil {
			return "", err
		}
		if due {
			if _, err := wake.Append(cfg.Home.State, "heartbeat", "heartbeat", "heartbeat"); err != nil {
				return "", err
			}
			if err := touchBeacon(heartbeatPath); err != nil {
				return "", err
			}
			if err := writeHeartbeatStreak(cfg.Home.State, streak+1); err != nil {
				return "", err
			}
			if _, err := wake.PublishEpisode(cfg.Home.State); err != nil {
				return "", err
			}
			return "heartbeat", nil
		}

		if cfg.WaitEvent != nil {
			cfg.WaitEvent(cfg.Poll)
		} else {
			cfg.Sleep(cfg.Poll)
		}

		// A successor may have stolen the singleton while we waited. Without
		// this re-check a displaced watcher would keep looping, eventually
		// append a wake and publish a second downtime episode that churns
		// the recovery-generation AckEpisode keys on. Return quietly, with
		// nothing appended and no episode published: the successor owns the
		// cycle now.
		if !lock.HeldByNamed(cfg.Home.State, watchLockName, os.Getpid()) {
			return "", nil
		}
	}
}
