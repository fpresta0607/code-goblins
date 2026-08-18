// Package watch implements the triage loop: a one-shot cycle that holds a
// singleton lock, scans state/ for changed status files, coalesces signals
// within a grace window, emits heartbeats with exponential backoff, and
// closes on the first actionable event. One actionable reason closes one
// watcher cycle; continuity across cycles is the arm layer's job (Task 11's
// stop-autoarm hook, which hosts this loop in-process), never the
// watcher's, so Run never wraps itself in an outer loop.
package watch

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/claudehook"
	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/routing"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const (
	watchLockName = ".watch.lock"
	seenPrefix    = ".seen-"
)

// Config carries watch.Run's tunables and its injection seams. Monitor is the
// structural Herdr monitor ConfigFromEnv installs for production; tests may
// leave it nil, in which case the signals-only path still advances monitor's
// typed heartbeat through the one watcher-health record. WaitEvent and
// Cleanup default to nil, meaning pure timer mode with nothing to release,
// until Task 9 supplies both for filesystem notifications.
type Config struct {
	Home         home.Home
	Poll         time.Duration
	SignalGrace  time.Duration
	Heartbeat    time.Duration
	HeartbeatMax time.Duration
	Monitor      *monitor.Service
	// Routing is the standing answer to a harness that starts erroring. An
	// empty policy simply means every fault wakes the CFO undecided.
	Routing routing.Policy
	Sleep   func(time.Duration)

	// WaitEvent is Task 9's filesystem-notification seam, replacing the
	// plain Sleep(Poll) wait between checks. Its bool return has two
	// halves, both load-bearing: true means an event was observed within
	// timeout and Run proceeds to rescan immediately - that fast path is
	// the entire point of supplying WaitEvent. False means the wait ended
	// with no event observed, and Run itself then enforces the floor: it
	// measures the elapsed time around the call and calls Sleep for
	// whatever is left of timeout, so a WaitEvent that returns early for
	// any reason (a broken directory handle, a bug) cannot spin the loop
	// faster than Poll for the whole in-process eight-hour host Task 11
	// runs.
	WaitEvent func(timeout time.Duration) bool

	Cleanup func()
}

// ConfigFromEnv fills Config from the timing env vars (internal/claudehook),
// clamping every interval to at least 1s. claudehook.Seconds ships with no
// clamp of its own: CFO_POLL=0 would spin the loop rewriting signatures and
// touching the beat, and CFO_POLL=-5 converts to roughly 49 days of wait so
// the watcher never beats and supervise.WatcherHealthy reads it as stale
// against a live process. The clamp belongs at this consumer; Task 2, which
// shipped claudehook.Seconds, is not reopened for it. HeartbeatMax is
// additionally floored at Heartbeat itself (after both are floored at 1s),
// so a CFO_HEARTBEAT_MAX set below CFO_HEARTBEAT cannot pin the backoff
// cadence to something shorter than its own base interval.
func ConfigFromEnv(h home.Home) Config {
	heartbeat := clampMin1s(claudehook.Seconds("CFO_HEARTBEAT", 600))
	heartbeatMax := clampMin1s(claudehook.Seconds("CFO_HEARTBEAT_MAX", 7200))
	if heartbeatMax < heartbeat {
		heartbeatMax = heartbeat
	}
	cfg := Config{
		Home:         h,
		Poll:         clampMin1s(claudehook.Seconds("CFO_POLL", 15)),
		SignalGrace:  clampMin1s(claudehook.Seconds("CFO_SIGNAL_GRACE", 30)),
		Heartbeat:    heartbeat,
		HeartbeatMax: heartbeatMax,
		Sleep:        time.Sleep,
	}
	// A missing or unreadable policy is not fatal: the fleet simply wakes the
	// CFO undecided, which is what it did before there was a policy at all.
	if policy, err := routing.Load(h.Data); err == nil {
		cfg.Routing = policy
	}
	// The production monitor prober is the read-only structural Herdr prober,
	// resolved against the same session source spawn uses (HERDR_SESSION,
	// default "default") so monitoring cannot drift to a different implicit
	// session. Both watch entry paths (manual cfo watch and the Task 11
	// stop-autoarm hook) build their Config here, so this one installation
	// covers both.
	session := os.Getenv("HERDR_SESSION")
	if session == "" {
		session = "default"
	}
	cfg.Monitor = &monitor.Service{
		StateDir:     h.State,
		Probe:        monitor.NewHerdrProber(&herdr.Client{Commands: execx.OSRunner{}, Session: session}),
		Heartbeat:    heartbeat,
		HeartbeatMax: heartbeatMax,
	}
	// A directory-change waiter is strictly an optimization: on failure
	// (most commonly a dev checkout with no state/ dir yet) cfg is left in
	// pure timer mode, exactly as it was before Task 9. WaitEvent and
	// Cleanup are wired together or not at all - see the Config.WaitEvent
	// doc for why a WaitEvent without a matching Cleanup would leak the
	// waiter's handles into a Go heap buffer the kernel still holds a
	// pointer to.
	if waiter, err := NewDirWaiter(h.State); err == nil {
		cfg.WaitEvent = waiter.Wait
		cfg.Cleanup = waiter.Close
	}
	return cfg
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
// is deliberately lossy: two distinct names can sanitize to the same
// string (for example "a.b.status" and "a_b.status" both become
// "a_b_status"). Never widen it to preserve dots: a signature file for
// "a.status" written with a literal dot would itself end in ".status" and
// be indistinguishable from a hidden status file the next scan picks up.
// Sanitize's output is therefore never used alone as a signature filename;
// see SeenName, which disambiguates it.
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

// SeenName returns the signature filename ScanSignals and CommitSignatures
// use for a raw *.status/*.turn-ended basename: Sanitize(name) for a
// human-readable prefix, plus an 8-hex-digit FNV-1a-32 hash of the RAW name
// so two names that sanitize identically can never share one signature
// file. Without the hash, a Sanitize collision is not a one-time duplicate
// wake: CommitSignatures for whichever colliding file commits last leaves
// the OTHER permanently stale against its own persisted signature, so every
// following ScanSignals reports that file changed again, and every watcher
// cycle closes on signal immediately - an unbounded rewake and
// recovery-generation storm under Task 11's eight-hour arm loop, not
// something wake.Deduped's last-write-wins fold has any power over (Deduped
// dedupes wake records by (kind,key); it does nothing to a scan that
// re-detects the same file as changed every cycle). The hash makes that
// collision unreachable rather than merely unlikely, while the sanitized
// prefix keeps the filename readable for a human inspecting state/.
func SeenName(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	return fmt.Sprintf("%s%s-%08x", seenPrefix, Sanitize(name), h.Sum32())
}

// ScanSignals is a pure read: it compares each *.status and *.turn-ended
// file directly inside stateDir against the persisted size:mtime signature
// in state/<SeenName(name)> and returns the entries whose signature moved,
// in filename order. It writes nothing anywhere; a file with no signature
// file yet counts as changed, so a first sighting is reported the same as
// any later change (the cross-restart contract: signals that land while no
// watcher runs are caught on the next start).
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

		seen, err := os.ReadFile(filepath.Join(stateDir, SeenName(name)))
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
		path := filepath.Join(stateDir, SeenName(c.Name))
		if err := fsx.AtomicWriteFile(path, []byte(c.Sig)); err != nil {
			return err
		}
	}
	return nil
}

// Run acquires the state/.watch.lock singleton, scans raw status signals,
// then runs the monitor before waiting. A raw signal remains this cycle's
// close reason, while a monitor event discovered in the same cycle stays
// persisted for the next cycle so two wake episodes never conflict.
func Run(cfg Config) (string, error) {
	if _, err := lock.AcquireNamedOwner(cfg.Home.State, watchLockName, os.Getpid(), "watch"); err != nil {
		// The lock was never acquired, so there is no LIFO defer pair to
		// register here: call Cleanup directly rather than deferring it,
		// or a waiter Task 9 already constructed (its handles open, before
		// Run ever got the singleton) would leak on this path, which Task
		// 11's AutoarmAttempts retries against a held lock exercise on
		// every acquire failure.
		if cfg.Cleanup != nil {
			cfg.Cleanup()
		}
		return "", fmt.Errorf("watch: acquire singleton: %w", err)
	}
	// LIFO: this defer is registered first and so runs SECOND (after
	// Cleanup below), releasing the singleton only once Cleanup (Task 9's
	// filesystem watch teardown) has already run.
	defer lock.ReleaseNamed(cfg.Home.State, watchLockName)
	if cfg.Cleanup != nil {
		defer cfg.Cleanup()
	}

	for {
		var signalDetail string
		changes, err := ScanSignals(cfg.Home.State)
		if err != nil {
			return "", err
		}
		if len(changes) > 0 {
			// Linger one grace period so a crewmate's final status write and
			// the same turn's turn-end marker land as one wake, not two.
			cfg.Sleep(cfg.SignalGrace)

			// A successor may have stolen the singleton during the grace
			// sleep. Re-check before this cycle commits to anything: a
			// double-live watcher that both proceeded here would each
			// append and publish, producing two episodes whose generations
			// collide on the next ack. Return quietly, exactly as the
			// mid-loop steal check below does.
			if !lock.HeldByNamed(cfg.Home.State, watchLockName, os.Getpid()) {
				return "", nil
			}

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
			if len(changes) == 0 {
				// Everything that triggered this cycle vanished during the
				// grace window (a goblin despawn inside SignalGrace is
				// plausible). Closing here anyway would append nothing,
				// commit nothing, and still publish an episode for a
				// meaningless "signal:" reason with an empty wake queue.
				// One extra fast iteration instead: no spin, since the next
				// pass falls through to the normal heartbeat check and wait.
				continue
			}

			// Only decision signals wake the CFO: a goblin writing its
			// working/review/test status is doing its job, not asking for a
			// decision. Non-decision changes are committed silently and the
			// heartbeat carries the fleet summary instead.
			var decisions []string
			for _, c := range changes {
				if signalIsDecision(cfg.Home.State, c.Name) {
					decisions = append(decisions, c.Name)
				}
			}
			if len(decisions) > 0 {
				detail := "signal:" + strings.Join(decisions, " ")
				for _, name := range decisions {
					if _, err := wake.Append(cfg.Home.State, "signal", name, detail); err != nil {
						return "", err
					}
				}
				signalDetail = detail
			}
			if err := CommitSignatures(cfg.Home.State, changes); err != nil {
				return "", err
			}
			if len(decisions) > 0 {
				if _, err := wake.PublishEpisode(cfg.Home.State); err != nil {
					return "", err
				}
			}
		}

		if cfg.Monitor != nil {
			result, err := cfg.Monitor.Scan(context.Background())
			if err != nil {
				return "", err
			}
			if result.Event != nil && signalDetail == "" {
				event := *result.Event
				event.Detail = routeHarnessError(cfg, event)
				if _, err := cfg.Monitor.Publish(event); err != nil {
					return "", err
				}
				return event.Kind + ":" + event.Detail, nil
			}
		} else if err := monitor.TouchHeartbeat(cfg.Home.State, time.Now()); err != nil {
			return "", err
		}
		if signalDetail != "" {
			return signalDetail, nil
		}

		if cfg.WaitEvent != nil {
			// A true return means WaitEvent observed something within
			// Poll and Run should proceed to rescan right away. A false
			// return means it did not, and Run enforces the Poll floor
			// itself so a WaitEvent that returns early for any reason
			// cannot spin the loop.
			start := time.Now()
			if !cfg.WaitEvent(cfg.Poll) {
				if remaining := cfg.Poll - time.Since(start); remaining > 0 {
					cfg.Sleep(remaining)
				}
			}
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

// routeHarnessError answers a provider failure with the fleet's standing
// policy, so the CFO reads the fix in the same breath as the fault instead of
// diagnosing a rate limit from scratch every time.
//
// The watcher decides but does not switch. A switch stops a harness, waits
// for it to exit, and relaunches it - seconds of interactive work - and this
// loop holds the watch singleton while it runs. Stalling fleet triage behind
// one goblin's relaunch would be worse than the churn it saves, so an `auto`
// rule is delivered as an instruction the CFO carries out on wake, and a
// non-auto rule as a recommendation it can weigh.
func routeHarnessError(cfg Config, event monitor.Event) string {
	if event.TaskID == "" || !strings.HasPrefix(event.Detail, string(monitor.HarnessError)) {
		return event.Detail
	}
	meta, err := state.ReadTaskMeta(cfg.Home.State, event.TaskID)
	if err != nil {
		return event.Detail
	}
	fault := event.Fault
	if fault == "" {
		return event.Detail
	}
	rule, matched := cfg.Routing.Match(meta.Harness, fault)
	if !matched {
		switch fault {
		case routing.Provider:
			// A third-party outage is the platform's own problem, not the
			// harness's: switching harnesses will not help. Wait and retry.
			return event.Detail + " | third-party outage: wait and retry; `cfo switch` will not help"
		case routing.Auth:
			return event.Detail + " | credential failure: fix the credential, not the harness"
		default:
			return event.Detail + " | no standing rule; decide and use `cfo switch " + event.TaskID + " --harness <h>` if a switch is right"
		}
	}
	prefix := " | RECOMMENDED: "
	if rule.Auto {
		prefix = " | STANDING POLICY, run it now: "
	}
	detail := event.Detail + prefix + rule.Command(event.TaskID)
	if rule.Note != "" {
		detail += " (" + rule.Note + ")"
	}
	if !rule.ForceDirty {
		detail += " (add --force-dirty if the worktree has uncommitted changes)"
	}
	return detail
}

// decisionVerb reports whether a status verb needs the CFO's immediate
// attention: a gate question, a hard failure, or a green gate awaiting merge.
// Everything else is a goblin doing its job and is folded into the heartbeat.
func decisionVerb(verb string) bool {
	switch verb {
	case "needs-decision", "blocked", "failed", "checks-passed", "checks_passed":
		return true
	default:
		return false
	}
}

// signalIsDecision reports whether a changed *.status file's latest verb is a
// decision the CFO must see immediately. *.turn-ended markers and unparsable
// or non-decision verbs are treated as noise, never as a wake.
func signalIsDecision(stateDir, name string) bool {
	if !strings.HasSuffix(name, ".status") {
		return false
	}
	id := strings.TrimSuffix(name, ".status")
	lines, err := state.TailStatus(stateDir, id, 1)
	if err != nil || len(lines) == 0 {
		return false
	}
	verb, _, ok := crewstate.ParseStatusLine(lines[0])
	if !ok {
		return false
	}
	return decisionVerb(verb)
}
