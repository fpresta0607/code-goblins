package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/fpresta0607/code-goblins/internal/claudehook"
	"github.com/fpresta0607/code-goblins/internal/guard"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/supervise"
	"github.com/fpresta0607/code-goblins/internal/wake"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// runHook is the single dispatcher every `cfo hook <name>` case routes
// through. An unknown hook name is not a caller error: a future Claude Code
// version invoking a hook name this build does not know about must not
// break the tool call, so it exits 0 with a one-line stderr diagnostic
// instead of denying or failing the request.
func runHook(name string, stdin io.Reader, stdout, stderr io.Writer) int {
	switch name {
	case "pretool-subagent":
		return hookPretoolSubagent(stdin, stdout, stderr)
	case "pretool-arm":
		return hookPretoolArm(stdin, stdout, stderr)
	case "pretool-cd":
		return hookPretoolCd(stdin, stdout, stderr)
	case "turnend-guard":
		return hookTurnendGuard(stdin, stdout, stderr)
	case "stop-autoarm":
		payload, ok := claudehook.ReadPayload(stdin)
		if !ok {
			return 0
		}
		h, err := home.Resolve()
		if err != nil {
			return 0
		}
		if !home.IsPrimary(h) {
			return 0
		}
		return hookStopAutoarm(h, payload, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "cfo hook: unknown hook %q\n", name)
		return 0
	}
}

// hookPretoolSubagent stops the CFO primary from delegating through the
// harness's own tools (Agent, Task, SendMessage, and the rest) instead of
// the fleet dispatch path: work started that way has no durable fleet
// record and dies with the session. Every early exit fails open (exit 0,
// silent) so the guard stays inert outside a genuine primary fleet home.
func hookPretoolSubagent(stdin io.Reader, stdout, stderr io.Writer) int {
	payload, ok := claudehook.ReadPayload(stdin)
	if !ok {
		return 0
	}
	h, err := home.Resolve()
	if err != nil {
		return 0
	}
	if !home.IsPrimary(h) {
		return 0
	}
	if os.Getenv("CFO_ALLOW_SUBAGENT") == "1" {
		return 0
	}
	stem, deny := guard.ClassifySubagent(payload.ToolName)
	if !deny {
		return 0
	}
	message := fmt.Sprintf(
		"[subagent-dispatch] the CFO primary dispatches through the fleet, not the harness's own delegation tools: work started that way has no durable fleet record and dies with this session. Use the fleet dispatch path once Plan 3 lands it (blocked tool: %s, delegation-shaped on %q). Launch the session with CFO_ALLOW_SUBAGENT=1 for a deliberate exception.",
		payload.ToolName, stem,
	)
	return claudehook.DenyPreTool(stderr, message)
}

// hookPretoolArm stops the agent shell from invoking the watcher directly:
// the watcher is supposed to be armed by the Stop-owned auto-arm hook, and
// running it (or killing it, backgrounding it, piping it, and the rest)
// from a Bash call bypasses that supervision. Every early exit fails open
// (exit 0, silent).
func hookPretoolArm(stdin io.Reader, stdout, stderr io.Writer) int {
	payload, ok := claudehook.ReadPayload(stdin)
	if !ok {
		return 0
	}
	h, err := home.Resolve()
	if err != nil {
		return 0
	}
	if !home.IsPrimary(h) {
		return 0
	}
	code, reason, deny := guard.ClassifyArm(payload.Command)
	if !deny {
		return 0
	}
	return claudehook.DenyPreTool(stderr, fmt.Sprintf("[%s] %s", code, reason))
}

// hookPretoolCd stops the agent shell from relocating its working directory:
// Claude Code's Bash tool keeps its working directory across calls, so a
// relocation anywhere in the command outlives the tool call. Upstream's
// cd-guard predicate is looser than IsPrimary; it is deliberately tightened
// to IsPrimary here, same as pretool-arm, so both guards share the
// inert-in-dev guarantee. This is a sanctioned deviation from upstream.
// Every early exit fails open (exit 0, silent).
func hookPretoolCd(stdin io.Reader, stdout, stderr io.Writer) int {
	payload, ok := claudehook.ReadPayload(stdin)
	if !ok {
		return 0
	}
	h, err := home.Resolve()
	if err != nil {
		return 0
	}
	if !home.IsPrimary(h) {
		return 0
	}
	code, reason, deny := guard.ClassifyCd(payload.Command)
	if !deny {
		return 0
	}
	return claudehook.DenyPreTool(stderr, fmt.Sprintf("[%s] %s", code, reason))
}

// genuinelyDownMessage is step 5's attended fail-open: supervision is
// confirmed broken (either the block budget is exhausted after a reported
// auto-arm failure, or the budget bookkeeping itself cannot be written), so
// the guard tells the operator once and lets the turn end rather than
// blocking every Stop forever.
const genuinelyDownMessage = "CFO SUPERVISION IS GENUINELY DOWN: the Stop-owned auto-arm could not restore the watcher and the block budget is exhausted. This turn may end, but supervision stays off. Run cfo doctor, verify the stop-autoarm hook registration in .claude/settings.json, and re-launch the session."

// escalationCeilingMultiplier sets the hard ceiling (escalationCeilingMultiplier
// * blockBudget) that fires regardless of NotifiedOnce or AlarmFired: the
// guarantee that no configuration can wedge a session shut. NotifiedOnce has
// exactly one writer, Task 11's stop-autoarm hook; if that hook is missing
// from settings.json or dies before ever calling MarkNotified, nothing ever
// satisfies the normal ladder arm below. stop_hook_active is also
// deliberately ignored by hookTurnendGuard (see its doc comment below), so
// Claude Code's own loop breaker cannot rescue the turn either. This arm is
// the fallback that still does.
const escalationCeilingMultiplier = 3

// ladderNeverEscalatedMessage is the ceiling arm's message: %d fills count.
// Distinct from genuinelyDownMessage because the likely cause is different -
// nothing ever reported a failure at all, rather than a reported failure the
// budget could not recover from.
const ladderNeverEscalatedMessage = "CFO SUPERVISION IS DOWN AND THE ESCALATION LADDER NEVER ESCALATED: the turn-end guard blocked %d times without the Stop-owned auto-arm ever reporting a failure. The usual cause is that \"cfo hook stop-autoarm\" is not registered in .claude/settings.json, so nothing is trying to restore the watcher. This turn may end, but supervision stays off. Run cfo doctor and verify the Stop hook registration."

// blindTurnBanner is step 6's block message: %s, %s fill inFlight (the
// "<N> task(s) in flight" string from supervise.Needed) and the watcher
// beat's age ("never" if no beat file exists yet).
const blindTurnBanner = "" +
	"●━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
	"●  TURN WOULD END BLIND - SUPERVISION IS OFF\n" +
	"●  %s, but no live watcher holds this home (last beat: %s).\n" +
	"●  The Stop-owned auto-arm did not claim recovery within the sync window.\n" +
	"●  Repair: verify .claude/settings.json wires \"cfo hook stop-autoarm\" with asyncRewake, then end the turn again. Run cfo drain for pending wakes.\n" +
	"●━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

// hookTurnendGuard refuses to let a turn end blind: goblins in flight, no
// live watcher, and no proof (Task 11's stop-autoarm hook) that recovery is
// under way. Each step below carries a one-line comment naming its upstream
// analogue (upstream turnend_guard.sh); stop_hook_active is read by
// claudehook.ReadPayload but deliberately never consulted here, per the
// upstream 2026-07-21 incident that taught this repo not to trust it.
func hookTurnendGuard(stdin io.Reader, stdout, stderr io.Writer) int {
	// upstream: payload extraction + primary gate, shared by every hook.
	payload, ok := claudehook.ReadPayload(stdin)
	if !ok {
		return 0
	}
	h, err := home.Resolve()
	if err != nil {
		return 0
	}
	if !home.IsPrimary(h) {
		return 0
	}
	state := h.State

	guardGrace := claudehook.Seconds("CFO_GUARD_GRACE", 300)
	epochFresh := claudehook.Seconds("CFO_CLAUDE_AUTOARM_EPOCH_FRESH", 15)
	syncWait := time.Duration(claudehook.Int("CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS", 800, 0, 60000)) * time.Millisecond
	blockBudget := claudehook.Int("CFO_CLAUDE_TURNEND_BLOCK_BUDGET", 3, 0, 1000000)

	// upstream step 1: is any goblin work in flight at all? An unlistable
	// state dir cannot prove work is in flight, so it fails open exactly
	// like a transport failure: silent, touching nothing.
	needed, inFlight, err := supervise.Needed(state)
	if err != nil {
		return 0
	}
	if !needed {
		// upstream step 2: quiet turn. A pending failure episode (already
		// notified) survives a quiet turn instead of being reset away.
		if !supervise.NotifiedOnce(state) {
			if err := supervise.ResetBudget(state); err != nil {
				return attendedFailOpen(stdout)
			}
		}
		return 0
	}

	// upstream step 3: is the watcher itself healthy?
	if supervise.WatcherHealthy(state, guardGrace) {
		if err := supervise.ResetBudget(state); err != nil {
			return attendedFailOpen(stdout)
		}
		return 0
	}

	// upstream step 4: give the sibling Stop-owned auto-arm a sync window
	// to prove recovery is under way before charging anything.
	if pollAutoarmProof(state, guardGrace, epochFresh, syncWait) {
		return 0
	}

	// upstream step 5: charge the block-budget ladder.
	count, err := supervise.ChargeBudget(state, payload.SessionID)
	if err != nil {
		return attendedFailOpen(stdout)
	}
	if count > blockBudget && supervise.NotifiedOnce(state) && !supervise.AlarmFired(state) {
		// The alarm message is one-time; the block is not (AlarmFired keeps
		// every later Stop routed to step 6 instead of back through here).
		_ = supervise.MarkAlarm(state)
		return claudehook.InfoAllow(stdout, genuinelyDownMessage)
	}
	if count > escalationCeilingMultiplier*blockBudget {
		// Hard ceiling, independent of NotifiedOnce/AlarmFired: the
		// guarantee that no configuration can wedge a session shut even
		// when nothing ever calls MarkNotified. Deliberately unconditional
		// and deliberately NOT one-time: once count crosses the ceiling it
		// only ever grows, so every later Stop this session keeps taking
		// this arm and the turn can always end. Ordered after the normal
		// ladder arm above so a genuine, already-notified episode still
		// gets the normal GENUINELY DOWN message first.
		return claudehook.InfoAllow(stdout, fmt.Sprintf(ladderNeverEscalatedMessage, count))
	}

	// upstream step 6: block, with the blind-turn banner.
	return claudehook.BlockStop(stderr, fmt.Sprintf(blindTurnBanner, inFlight, beatAge(state)))
}

// pollAutoarmProof checks supervise.AutoarmOwnsRecovery immediately, then
// again every 100ms (or whatever is left of syncWait, whichever is shorter)
// until syncWait elapses. syncWait <= 0 means exactly one check and no
// waiting at all. Elapsed time is checked before each sleep, not after, so a
// syncWait shorter than 100ms cannot overshoot the window by sleeping a full
// 100ms anyway.
func pollAutoarmProof(state string, grace, epochFresh, syncWait time.Duration) bool {
	if supervise.AutoarmOwnsRecovery(state, grace, epochFresh) {
		return true
	}
	if syncWait <= 0 {
		return false
	}
	deadline := time.Now().Add(syncWait)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		wait := 100 * time.Millisecond
		if remaining < wait {
			wait = remaining
		}
		time.Sleep(wait)
		if supervise.AutoarmOwnsRecovery(state, grace, epochFresh) {
			return true
		}
	}
}

// attendedFailOpen is the failure posture for a ChargeBudget or ResetBudget
// error: the ladder cannot run at all, so the guard escalates once instead
// of blocking every Stop forever on a counter that can never advance. It
// deliberately does NOT call MarkAlarm: this branch fires for errors with
// nothing to do with the ladder's own escalation (a quiet-turn ResetBudget
// failure has no episode to speak of), and marking the alarm here would
// permanently disarm the ladder's own one-shot GENUINELY DOWN message for
// whatever genuine failure episode comes next, routing it straight to a
// permanent block instead. This branch can legitimately repeat on every
// Stop until the home is repaired; that is fine precisely because it never
// touches AlarmFired.
func attendedFailOpen(stdout io.Writer) int {
	return claudehook.InfoAllow(stdout, genuinelyDownMessage)
}

// beatAge renders state/.last-watcher-beat's age for the blind-turn banner,
// or "never" if the beat file does not exist yet.
func beatAge(state string) string {
	fi, err := os.Stat(filepath.Join(state, ".last-watcher-beat"))
	if err != nil {
		return "never"
	}
	return time.Since(fi.ModTime()).Round(time.Second).String()
}

// autoarmLockName and autoarmSession mirror internal/supervise's private
// constants of the same name: this hook is that lock's sole acquirer, and
// supervise exports no helper to take it, only functions that read it
// (WatcherHealthy, AutoarmOwnsRecovery).
const (
	autoarmLockName = ".claude-autoarm.lock"
	autoarmSession  = "autoarm"
)

// actionableReasonPattern matches the watch.Run reasons that mean a real
// supervision event needs a handling turn: a status/turn-ended signal, a
// stale-pane sweep (NOT PORTED IN V1, reserved for Plan 3), a check.sh sweep
// (NOT PORTED IN V1, reserved for Plan 4), or a heartbeat.
var actionableReasonPattern = regexp.MustCompile(`^(signal:|stale:|check:|heartbeat($|:))`)

// rewakeBannerFmt and failureBannerFmt are cfo hook stop-autoarm's two
// stderr banners, verbatim per the plan brief. rewakeBannerFmt's %s is the
// actionable reason line; failureBannerFmt's %d/%s are the attempt count and
// the final attempt's error text.
const rewakeBannerFmt = "cfo watcher wake - one supervision event needs a handling turn now.\n%s\nRun cfo drain, handle what it presents, and acknowledge with the WAKE_ACK_REQUIRED command it prints. Do not run cfo watch manually after an ordinary wake."

const failureBannerFmt = "cfo auto-arm FAILED after %d attempt(s): the watcher could not hold this home.\nLast error: %s\nSupervision is down and needs a repair turn: run cfo doctor, then verify the stop-autoarm hook registration in .claude/settings.json, and check state\\.watch.lock for a holder that is not yours."

// resolveAncestorPID is the stop-autoarm hook's identity gate: it returns
// the harness ancestor's pid, or false if none is found. A manual shell
// invocation with no harness ancestor above it must never arm.
//
// CFO_TEST_ANCESTOR_PID, when set, replaces the ambient proc.FindAncestor
// walk entirely: a go test binary launched from a Claude Code session has
// claude.exe about five hops up its own ancestry, well inside maxHops 16, so
// the ambient walk cannot be used from this repo's own test suite to assert
// "no harness ancestor found". The override is validated with
// proc.Ancestry(pid, 1): a pid the Toolhelp snapshot no longer resolves, or
// whose creation time cannot be resolved (both are Ancestry's own walk stop
// conditions), yields an empty walk, which disables the override and fails
// the identity gate outright rather than falling back to the ambient walk.
// Test seam, not a production contract: production hosts never set this
// variable.
func resolveAncestorPID() (int, bool) {
	if raw := os.Getenv("CFO_TEST_ANCESTOR_PID"); raw != "" {
		pid, err := strconv.Atoi(raw)
		if err != nil {
			return 0, false
		}
		entries, err := proc.Ancestry(pid, 1)
		if err != nil || len(entries) == 0 {
			return 0, false
		}
		return pid, true
	}
	entry, ok := proc.FindAncestor(os.Getpid(), 16, "claude", "node")
	if !ok {
		return 0, false
	}
	return entry.PID, true
}

// hookStopAutoarm hosts the watcher in-process for up to eight hours: Claude
// fires this hook on every Stop with asyncRewake:true and an 8h timeout,
// undeduplicated, and this process IS the watcher host - its eventual exit 2
// stderr is what rewakes the idle agent. Steps below are commented against
// the plan brief's numbering (upstream analogue:
// bin/fm-claude-stop-autoarm.sh). The stdin/home/IsPrimary prologue lives in
// runHook's dispatch switch, shared with every other hook in this file.
func hookStopAutoarm(h home.Home, payload claudehook.Payload, stdout, stderr io.Writer) int {
	state := h.State

	// Step 2: identity gate.
	ancestorPID, ok := resolveAncestorPID()
	if !ok {
		return 0
	}

	// Session custody: claim or confirm the primary session lock for the
	// harness ancestor. lock.ErrHeld (a different live owner holds the
	// home) and lock.ErrOwnerDead (the harness exited between the
	// ancestry walk and this acquire, per Task 4's cross-task contract)
	// are both inert, never a failure episode.
	if !lock.HeldBy(state, ancestorPID) {
		if _, err := lock.AcquireOwner(state, ancestorPID, payload.SessionID); err != nil {
			return 0
		}
	}

	// Step 3: need gate. An unlistable state directory never arms.
	needed, _, err := supervise.Needed(state)
	if err != nil || !needed {
		return 0
	}

	// Step 4: single-flight. ErrHeld means another firing already owns
	// this Stop; exit 0 immediately rather than racing it.
	if _, err := lock.AcquireNamedOwner(state, autoarmLockName, os.Getpid(), autoarmSession); err != nil {
		return 0
	}
	defer lock.ReleaseNamed(state, autoarmLockName)

	// Step 5: epoch ledger, best-effort. An unwritable ledger is not proof
	// of anything, so every outcome write below (through recordOutcome)
	// is best-effort too. Requirement 5: errors.Is distinguishes a
	// benign, expected ErrStaleEpoch supersede from a genuine ledger I/O
	// failure; neither is ever treated as a failure episode of its own -
	// this firing's real exit code and stderr are decided independently
	// of whether the ledger recorded them.
	epoch, epochErr := supervise.NextEpoch(state)
	hasEpoch := epochErr == nil
	recordOutcome := func(outcome string) {
		if !hasEpoch {
			return
		}
		if err := supervise.SetOutcome(state, epoch, outcome); err != nil && !errors.Is(err, supervise.ErrStaleEpoch) {
			_ = err // genuine ledger I/O failure; still best-effort here
		}
	}

	guardGrace := claudehook.Seconds("CFO_GUARD_GRACE", 300)
	attempts := claudehook.Int("CFO_CLAUDE_AUTOARM_ATTEMPTS", 2, 1, 3)

	var (
		reason      string
		lastErr     error
		actionable  bool
		healthy     bool
		attemptsRun int
	)

	// Step 6: attempt loop. Requirement 1: watch.Config is single-use on
	// every exit path (Run calls Cleanup on success via defer and on
	// acquire failure explicitly), so ConfigFromEnv is rebuilt fresh every
	// attempt; reusing one Config across retries would hand a later
	// attempt a closed directory-change waiter that scores a strike per
	// Wait until the breaker degrades it to timer mode.
	for i := 0; i < attempts; i++ {
		attemptsRun = i + 1
		reason, lastErr = watch.Run(watch.ConfigFromEnv(h))
		if lastErr == nil && actionableReasonPattern.MatchString(reason) {
			actionable = true
			break
		}
		if lastErr != nil && !errors.Is(lastErr, lock.ErrHeld) {
			// Requirement 6: watch.Run deliberately does not publish an
			// episode on its own error paths, because a transient stat
			// failure inside the loop must not churn the recovery
			// generation. This arm owns that publish instead, scoped to
			// genuine internal failures only: a lock.ErrHeld contention
			// error means a different live watcher already holds
			// .watch.lock, which is that watcher's own episode to
			// publish (or not), not this attempt's failure to report.
			_, _ = wake.PublishEpisode(state)
		}
		if supervise.WatcherHealthy(state, guardGrace) {
			healthy = true
			break
		}
		// Strike; continue to the next attempt.
	}

	// Step 7, need-vanished check first: it wins over whatever the loop
	// found. If the goblin work that justified arming is already gone,
	// whatever the loop durably queued (a wake record, a published
	// episode) survives for a later drain regardless, and this firing
	// does not need to force an immediate handling turn for it.
	if stillNeeded, _, err := supervise.Needed(state); err != nil || !stillNeeded {
		recordOutcome("clean")
		return 0
	}

	if healthy {
		_ = supervise.ResetBudget(state)
		recordOutcome("clean")
		return 0
	}

	if actionable {
		_ = supervise.ResetBudget(state)
		recordOutcome("rewake")
		return claudehook.BlockStop(stderr, fmt.Sprintf(rewakeBannerFmt, reason))
	}

	// Genuine failure: neither actionable nor healthy, and the need has
	// not vanished. Requirement 4: MarkNotified is called on every
	// failure path below, not only the first-in-episode branch - it is
	// the only writer of this marker, and the turnend-guard's normal
	// escalation arm is unreachable without it having fired at least
	// once. NotifiedOnce is read BEFORE marking so the branch below still
	// correctly distinguishes first from repeat.
	firstInEpisode := !supervise.NotifiedOnce(state)
	_ = supervise.MarkNotified(state)

	if firstInEpisode {
		recordOutcome("failed")
		lastErrText := "watcher closed without an actionable reason"
		if lastErr != nil {
			lastErrText = lastErr.Error()
		}
		return claudehook.BlockStop(stderr, fmt.Sprintf(failureBannerFmt, attemptsRun, lastErrText))
	}

	// Repeat failure: the outcome is failed-suppressed either way; only
	// the exit code differs, gated on whether the synchronous
	// turnend-guard has already spent this episode's one-time attended
	// fail-open (AlarmFired). An unconditional exit 2 here would defeat
	// that fail-open and produce an unbreakable loop of empty rewakes -
	// the ONE turn the fail-open was meant to release would never end
	// either. Later Stops still meet the guard's blind-turn banner; this
	// arm only makes sure this hook is not the thing blocking the turn
	// the guard just let through.
	recordOutcome("failed-suppressed")
	if supervise.AlarmFired(state) {
		return 0
	}
	return 2
}
