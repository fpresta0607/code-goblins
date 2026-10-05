package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/claudehook"
	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/guard"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/supervise"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// runHook is the single dispatcher every `cfo hook <name>` case routes
// through. An unknown hook name is not a caller error: a future Claude Code
// version invoking a hook name this build does not know about must not
// break the tool call, so it exits 0 with a one-line stderr diagnostic
// instead of denying or failing the request.
func runHook(name string, stdin io.Reader, stdout, stderr io.Writer) int {
	// A goblin is never supervised by the CFO's hooks: it is the work being
	// supervised. Until the hooks moved to user scope this held by accident
	// - a goblin's worktree has no cfo.exe, so the repo-scoped `[ -x ... ]`
	// guard fired - and an accident that has to be remembered is exactly the
	// bug this replaces. The check sits above the dispatch rather than
	// inside each arm so it covers every hook that exists and every hook
	// anyone adds later. A gate agent is not the CFO either. The hooks are
	// user-level, so both run them on every tool call they select, and both
	// leave on their environment alone, before reading the payload or the
	// home.
	if os.Getenv(harness.RoleVariable) == harness.RoleGoblin || os.Getenv(gateAgentVariable) != "" {
		return 0
	}
	switch name {
	case "pretool-subagent":
		return hookPretoolSubagent(stdin, stdout, stderr)
	case "pretool-bash":
		return hookPretoolBash(stdin, stderr, guard.ClassifyArm, guard.ClassifyCd)
	case "pretool-arm":
		return hookPretoolBash(stdin, stderr, guard.ClassifyArm)
	case "pretool-cd":
		return hookPretoolBash(stdin, stderr, guard.ClassifyCd)
	case "turnend-guard":
		return hookTurnendGuard(stdin, stdout, stderr)
	case "pre-compact":
		payload, ok := claudehook.ReadPayload(stdin)
		if !ok || payload.SessionID == "" {
			return 0
		}
		h, err := home.Resolve()
		if err != nil || !home.IsPrimary(h) {
			return 0
		}
		holder, err := lock.Read(h.State)
		if err != nil || holder.Session != payload.SessionID || holder.PID != resolveSessionOwnerPID(h.State) || !holder.VerifiedAlive() {
			return 0
		}
		_ = digest.WriteCheckpoint(h, time.Now())
		return 0
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
	case "session-start":
		payload, ok := claudehook.ReadPayload(stdin)
		if !ok {
			return 0
		}
		// Deliberate deviation, recorded for the ledger: a home that cannot
		// be resolved (os.Getwd fails; CFO_HOME unset) is silent exit 0 here,
		// matching the fail-open prologue every other hook in this switch
		// shares, rather than rendered as "SESSION START DEGRADED" digest
		// text. Reaching this arm needs the working directory itself to be
		// gone, which the shared prologue's uniformity was judged to
		// outweigh; digest.ComposeBrief is never even reached in this case.
		h, err := home.Resolve()
		if err != nil {
			return 0
		}
		if !home.IsPrimary(h) {
			return 0
		}
		return hookSessionStart(h, payload, stdout)
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
	// A native question holds the registered CFO's whole turn and shows only
	// in its own terminal, so the Overlord never sees it on the board and the
	// fleet goes unsupervised while it waits. Only that session is refused:
	// any other has no board to publish through, and its native prompt is
	// the only way it has to ask.
	if guard.ClassifyNativePrompt(payload.ToolName) {
		if !supervisor.RunsUnderRegisteredCFO(h.State) {
			return 0
		}
		return claudehook.DenyPreTool(stderr, fmt.Sprintf(nativePromptRefusal, payload.ToolName))
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

// nativePromptRefusal is what the registered CFO reads when it reaches for a
// native question, naming the two commands that ask without blocking.
const nativePromptRefusal = "[native-prompt] the registered primary CFO never asks through a native selector (blocked tool: %s): it holds this whole turn, shows only in this terminal and never reaches the Command Center, so supervision stops while it waits. Publish the question with cfo question --id <stable-id> --text \"<question>\" --option \"<choice>\" --recommend \"<choice>\", or a command only the Overlord can run with cfo run-request --id <stable-id> --title \"<why>\" --shell powershell --command-file <path>. His answer arrives here as a message, so keep supervising while it is out."

// gateAgentVariable is set by no-mistakes on every agent it starts for a
// gate step.
const gateAgentVariable = "NO_MISTAKES_GATE"

// hookPretoolBash applies the Bash guards to one Bash call, the first that
// refuses it deciding. pretool-bash, the one hook a Bash call runs, applies
// both, so the call starts one process however many guards there are;
// pretool-arm and pretool-cd apply one each.
//
// The arm guard stops the agent shell from invoking the watcher directly:
// the watcher is supposed to be armed by the Stop-owned auto-arm hook, and
// running it (or killing it, backgrounding it, piping it, and the rest)
// from a Bash call bypasses that supervision. The cd guard stops the agent
// shell from relocating its working directory: Claude Code's Bash tool keeps
// its working directory across calls, so a relocation anywhere in the
// command outlives the tool call. Upstream's cd-guard predicate is looser
// than IsPrimary; it is deliberately tightened to IsPrimary here, as the arm
// guard's is, so both share the inert-in-dev guarantee. This is a sanctioned
// deviation from upstream. Every early exit fails open (exit 0, silent).
func hookPretoolBash(stdin io.Reader, stderr io.Writer, guards ...func(command string) (code, reason string, deny bool)) int {
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
	for _, classify := range guards {
		if code, reason, deny := classify(payload.Command); deny {
			return claudehook.DenyPreTool(stderr, fmt.Sprintf("[%s] %s", code, reason))
		}
	}
	return 0
}

// genuinelyDownMessage is step 5's attended fail-open: supervision is
// confirmed broken (either the block budget is exhausted after a reported
// auto-arm failure, or the budget bookkeeping itself cannot be written), so
// the guard tells the operator once and lets the turn end rather than
// blocking every Stop forever.
const genuinelyDownMessage = "CFO SUPERVISION IS GENUINELY DOWN: the Stop-owned auto-arm could not restore the watcher and the block budget is exhausted. This turn may end, but supervision stays off. Run cfo doctor, repair the stop-autoarm hook registration with cfo install (it writes the user settings: ~/.claude/settings.json, or CLAUDE_CONFIG_DIR when set), and re-launch the session."

// escalationCeilingMultiplier sets the hard ceiling (escalationCeilingMultiplier
// * blockBudget) that fires regardless of NotifiedOnce or AlarmFired: the
// guarantee that no configuration can wedge a session shut. NotifiedOnce has
// exactly one writer, Task 11's stop-autoarm hook; if that hook is missing
// from the user settings or dies before ever calling MarkNotified, nothing ever
// satisfies the normal ladder arm below. stop_hook_active is also
// deliberately ignored by hookTurnendGuard (see its doc comment below), so
// Claude Code's own loop breaker cannot rescue the turn either. This arm is
// the fallback that still does.
const escalationCeilingMultiplier = 3

// ladderNeverEscalatedMessage is the ceiling arm's message: %d fills count.
// Distinct from genuinelyDownMessage because the likely cause is different -
// nothing ever reported a failure at all, rather than a reported failure the
// budget could not recover from.
const ladderNeverEscalatedMessage = "CFO SUPERVISION IS DOWN AND THE ESCALATION LADDER NEVER ESCALATED: the turn-end guard blocked %d times without the Stop-owned auto-arm ever reporting a failure. The usual cause is that \"cfo hook stop-autoarm\" is not registered in the user settings (~/.claude/settings.json, or CLAUDE_CONFIG_DIR when set), so nothing is trying to restore the watcher. This turn may end, but supervision stays off. Run cfo doctor to check the Stop hook registration and cfo install to repair it."

// blindTurnBanner is step 6's block message: %s, %s fill inFlight (the
// "<N> task(s) in flight" string from supervise.Needed) and the watcher
// beat's age ("never" if no beat file exists yet).
const blindTurnBanner = "" +
	"●━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
	"●  TURN WOULD END BLIND - SUPERVISION IS OFF\n" +
	"●  %s, but no live watcher holds this home (last beat: %s).\n" +
	"●  The Stop-owned auto-arm did not claim recovery within the sync window.\n" +
	"●  Repair: run cfo doctor, then cfo install to wire \"cfo hook stop-autoarm\" with asyncRewake in the user settings, then end the turn again. Run cfo drain for pending wakes.\n" +
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
		// A persistently unwritable budget file (path shadowed by a
		// directory, an ACL, an AV lock that never releases) means count can
		// never advance, so the normal ladder arm below is unreachable and
		// the escalation ceiling - which sits after this charge - is too. If
		// NotifiedOnce is already true, stop-autoarm has genuinely reported a
		// failure episode, so marking the alarm here represents a real
		// reported failure rather than a bookkeeping hiccup: the
		// operator-facing message is identical either way, and this restores
		// the sibling hookStopAutoarm's waitForAlarm release so its
		// repeat-failure arm can exit 0 instead of looping exit 2 forever.
		// Best-effort: a MarkAlarm failure here is no worse than the charge
		// failure that led here.
		if supervise.NotifiedOnce(state) {
			_ = supervise.MarkAlarm(state)
		}
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
// of blocking every Stop forever on a counter that can never advance. The
// function itself never touches MarkAlarm: this branch fires for errors with
// nothing to do with the ladder's own escalation (a quiet-turn ResetBudget
// failure has no episode to speak of), and marking the alarm here would
// permanently disarm the ladder's own one-shot GENUINELY DOWN message for
// whatever genuine failure episode comes next, routing it straight to a
// permanent block instead. This branch can legitimately repeat on every
// Stop until the home is repaired; that is fine precisely because it never
// touches AlarmFired. The one exception lives at the ChargeBudget call site,
// not here: see its comment for why a persistent ChargeBudget error after
// NotifiedOnce is already true marks the alarm itself before calling in.
func attendedFailOpen(stdout io.Writer) int {
	return claudehook.InfoAllow(stdout, genuinelyDownMessage)
}

// beatAge renders the typed monitor heartbeat's age for the blind-turn
// banner, or "never" when no watcher cycle has been recorded.
func beatAge(state string) string {
	heartbeat, err := monitor.ReadHeartbeat(state)
	if err != nil || heartbeat.LastCycle.IsZero() {
		return "never"
	}
	return time.Since(heartbeat.LastCycle).Round(time.Second).String()
}

// autoarmLockName and autoarmSession mirror internal/supervise's private
// constants of the same name: this hook is that lock's sole acquirer, and
// supervise exports no helper to take it, only functions that read it
// (WatcherHealthy, AutoarmOwnsRecovery).
const (
	autoarmLockName = ".claude-autoarm.lock"
	autoarmSession  = "autoarm"
)

// autoarmWaitSeconds bounds a wait on a watcher this hook does not host. It
// sits five minutes under the 28800s timeout install.Hooks registers, so the
// wait ends with a recorded clean outcome instead of being killed.
const autoarmWaitSeconds = 28500

// rewokenFile holds the highest wake sequence a rewake has already covered.
const rewokenFile = ".claude-autoarm-rewoken"

// awaitQueuedWake waits until deadline for a queued wake that no rewake has
// covered yet. Every queued record needs the CFO, because wake.Append admits
// no other kind, so none is filtered out. It returns false early once the
// watcher it relies on stops being healthy.
func awaitQueuedWake(state string, grace, settle time.Duration, deadline time.Time) (string, bool, error) {
	poll := max(claudehook.Seconds("CFO_POLL", 15), time.Second)
	for {
		records, err := wake.Pending(state)
		if err != nil {
			return "", false, err
		}
		rewoken, err := readRewoken(state)
		if err != nil {
			return "", false, err
		}
		var fresh []wake.Record
		for _, record := range records {
			if record.Seq > rewoken {
				fresh = append(fresh, record)
			}
		}
		if len(fresh) > 0 {
			reason := fresh[0].Kind + ":" + fresh[0].Key
			if len(fresh) > 1 {
				reason += fmt.Sprintf(" and %d more queued wake(s)", len(fresh)-1)
			}
			return reason, true, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || settledWatcherProblem(state, grace, settle) != "" {
			return "", false, nil
		}
		time.Sleep(min(poll, remaining))
	}
}

// handoverSettlePoll is how often a watcher that is changing hands is looked
// at again.
const handoverSettlePoll = 100 * time.Millisecond

// settledWatcherProblem is supervise.WatcherProblem, read again for up to
// settle while the watcher is changing hands, so a reading taken in the
// middle of a handover is not taken for supervision being down. The lock is
// free between a watcher letting it go and serve taking it, serve's record is
// empty between its creation and its first write, and a serve that has just
// taken the lock has not stamped its heartbeat yet. On 2026-10-02 the hook
// reported supervision down in CI while serve held the lock: each of its two
// attempts took one reading, and a handover always spends the first.
func settledWatcherProblem(state string, grace, settle time.Duration) string {
	deadline := time.Now().Add(settle)
	for {
		problem := supervise.WatcherProblem(state, grace)
		if problem == "" || !changingHands(state) || !time.Now().Before(deadline) {
			return problem
		}
		time.Sleep(handoverSettlePoll)
	}
}

// changingHands reports whether the watcher lock is on its way to a holder
// that may yet prove healthy: a live serve has asked for it and not yet
// withdrawn its request, which it does once it holds the lock; or its record
// is there and cannot be read, as one being written; or a live process holds
// it. A lock nobody holds or asks for, or one a dead process left, is not.
func changingHands(state string) bool {
	if watch.HandoverPending(state) {
		return true
	}
	holder, err := lock.ReadNamed(state, ".watch.lock")
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	return err != nil || holder.Alive()
}

func readRewoken(state string) (int, error) {
	data, err := fsx.ReadFile(filepath.Join(state, rewokenFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// markRewoken records every record queued now as covered: the rewake sends
// the CFO to cfo drain, which shows all of them.
func markRewoken(state string) error {
	records, err := wake.Pending(state)
	if err != nil || len(records) == 0 {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(state, rewokenFile), []byte(strconv.Itoa(records[len(records)-1].Seq)+"\n"))
}

// actionableReasonPattern matches the watch.Run reasons that mean a real
// supervision event needs a handling turn: a status/turn-ended signal, a
// monitor stale or heartbeat event, an orphan sweep finding, or a check.sh
// sweep (NOT PORTED IN V1, reserved for Plan 4). A typed heartbeat record
// alone proves liveness; a monitor heartbeat event is separately actionable.
//
// orphan: belongs here for the same reason the sweep exists at all. It is
// only ever returned once per newly-appeared finding set, and by the time it
// is returned the sweep has already appended a wake record and published an
// episode. Leaving it unmatched would read a successful sweep as an arming
// attempt that accomplished nothing: the hook would strike it, and on the
// last attempt publish a watcher-down failure episode for a watcher that was
// working exactly as designed.
var actionableReasonPattern = regexp.MustCompile(`^(signal:|stale:|check:|orphan:|heartbeat($|:))`)

// rewakeBannerFmt and failureBannerFmt are cfo hook stop-autoarm's two
// stderr banners, verbatim per the plan brief. rewakeBannerFmt's %s is the
// actionable reason line; failureBannerFmt's %d/%s/%s are the attempt count,
// the final attempt's error text and what the last look at the watcher found.
const rewakeBannerFmt = "cfo watcher wake - one supervision event needs a handling turn now.\n%s\nRun cfo drain, handle what it presents, and acknowledge with the WAKE_ACK_REQUIRED command it prints. That command is refused while unanswered blocked/failed notifies sit at or below its sequence: drain lists every waiting goblin and retires nothing. Answer each with `cfo send <id> \"...\"`, then re-run with --ack-blocking, which retires EVERY question at or below that sequence. Do not run cfo watch manually after an ordinary wake."

const failureBannerFmt = "cfo auto-arm FAILED after %d attempt(s): the watcher could not hold this home.\nLast error: %s\nWatcher: %s\nSupervision is down and needs a repair turn: run cfo doctor, repair the stop-autoarm hook registration with cfo install (it writes the user settings: ~/.claude/settings.json, or CLAUDE_CONFIG_DIR when set), and check state\\.watch.lock for a holder that is not yours."

// windowEndedBanner rewakes the CFO when the hook's wait on the wake queue
// ends with its window and nothing queued, so the turn it ends re-arms it.
const windowEndedBanner = "cfo watch window ended: the Stop hook watched the wake queue for its whole window and nothing arrived. End this turn to re-arm it; there is nothing to drain."

// stalledWatcher is the failure banner for a watcher a live process holds
// but has not finished a cycle within the stall window, or "" when no live
// process holds it or the watcher is healthy. cfo install cannot free a lock
// a live process holds, so the banner names the holder and says to restart it.
func stalledWatcher(state string, grace time.Duration, attempts int) string {
	holder, err := lock.ReadNamed(state, ".watch.lock")
	if err != nil || !holder.Alive() || supervise.WatcherHealthy(state, grace) {
		return ""
	}
	since := "has never finished a cycle"
	if heartbeat, err := monitor.ReadHeartbeat(state); err == nil && !heartbeat.LastCycle.IsZero() {
		since = "has not finished a cycle for " + time.Since(heartbeat.LastCycle).Round(time.Minute).String()
	}
	return fmt.Sprintf("cfo auto-arm FAILED after %d attempt(s): pid %d holds the watcher (state\\.watch.lock, taken %s) but %s, so its monitor wakes have stopped; goblin notifies still reach the queue. If it is cfo serve, restart cfo serve (goblins stop, then goblins); nothing else can take the watcher while that process lives, cfo doctor included.",
		attempts, holder.PID, holder.Acquired.UTC().Format("2006-01-02 15:04 UTC"), since)
}

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
// stderr is what rewakes the idle agent. When cfo serve already holds a
// healthy watcher, this process waits on that watcher's wake queue instead
// and exits the same way. Steps below are commented against
// the plan brief's numbering (upstream analogue:
// bin/fm-claude-stop-autoarm.sh). The stdin/home/IsPrimary prologue lives in
// runHook's dispatch switch, shared with every other hook in this file.
func hookStopAutoarm(h home.Home, payload claudehook.Payload, stdout, stderr io.Writer) int {
	return hookStopAutoarmWithConfig(h, payload, stdout, stderr, watch.ConfigFromEnv)
}

func hookStopAutoarmWithConfig(h home.Home, payload claudehook.Payload, stdout, stderr io.Writer, newConfig func(home.Home) watch.Config) int {
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
	// is best-effort too. Requirement 5: a stale-epoch refusal (a benign,
	// expected supersede) and a genuine ledger I/O failure are both
	// best-effort here and neither is ever treated as a failure episode
	// of its own - this firing's real exit code and stderr are decided
	// independently of whether the ledger write itself succeeded, so the
	// two error kinds need no separate handling beyond ignoring both.
	epoch, epochErr := supervise.NextEpoch(state)
	hasEpoch := epochErr == nil
	recordOutcome := func(outcome string) {
		if !hasEpoch {
			return
		}
		_ = supervise.SetOutcome(state, epoch, outcome)
	}

	// A watcher this hook does not host, cfo serve, runs its reconcile
	// cycle in the loop that also stamps its heartbeat, and a cycle slowed
	// by a starved machine ran past the guard's five-minute grace on
	// 2026-09-30, so the hook read serve as dead while it held the lock and
	// could not be replaced. A live holder counts as the watcher until its
	// heartbeat is older than the stall window; only then is supervision down.
	watcherGrace := max(claudehook.Seconds("CFO_GUARD_GRACE", 300), claudehook.Seconds("CFO_WATCHER_STALL", 900))
	attempts := claudehook.Int("CFO_CLAUDE_AUTOARM_ATTEMPTS", 2, 1, 3)
	syncWait := time.Duration(claudehook.Int("CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS", 800, 0, 60000)) * time.Millisecond
	// A watcher changing hands is looked at again for this long before a
	// look counts as a strike.
	settle := time.Duration(claudehook.Int("CFO_CLAUDE_AUTOARM_SETTLE_MS", 3000, 0, 60000)) * time.Millisecond

	var (
		reason         string
		lastErr        error
		actionable     bool
		healthy        bool
		attemptsRun    int
		watcherProblem string
	)

	// Step 6: attempt loop. Requirement 1: watch.Config is single-use on
	// every exit path (Run calls Cleanup on success via defer and on
	// acquire failure explicitly), so ConfigFromEnv is rebuilt fresh every
	// attempt; reusing one Config across retries would hand a later
	// attempt a closed directory-change waiter that scores a strike per
	// Wait until the breaker degrades it to timer mode. Requirement 6's
	// watcher-down episode is NOT published per-attempt here: see the
	// genuine-failure outcome arm below for why the publish has to happen
	// after the outcome is settled, not inside this loop.
	//
	// A healthy watcher this hook does not host, cfo serve, queues wakes
	// but has no way to rewake this session, so the hook waits on the queue
	// itself and hosts the watcher again if that one stops being healthy.
	deadline := time.Now().Add(claudehook.Seconds("CFO_CLAUDE_AUTOARM_WAIT", autoarmWaitSeconds))
	for {
		healthy = false
		for i := 0; i < attempts; i++ {
			attemptsRun = i + 1
			reason, lastErr = watch.Run(newConfig(h))
			if lastErr == nil && actionableReasonPattern.MatchString(reason) {
				actionable = true
				break
			}
			if watcherProblem = settledWatcherProblem(state, watcherGrace, settle); watcherProblem == "" {
				healthy = true
				break
			}
			// Strike; continue to the next attempt.
		}
		if !healthy {
			break
		}
		var err error
		reason, actionable, err = awaitQueuedWake(state, watcherGrace, settle, deadline)
		if err != nil {
			healthy, lastErr = false, err
			break
		}
		if actionable || !time.Now().Before(deadline) {
			break
		}
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

	// A rewake records the queue it covers, so a record still waiting on an
	// answer never rewakes the session a second time. Without that record the
	// next Stop would rewake for the same wake forever, so failing to write it
	// is a supervision failure, not a rewake.
	if actionable {
		if err := markRewoken(state); err != nil {
			actionable, healthy, lastErr = false, false, fmt.Errorf("record the rewake: %w", err)
		}
	}

	if actionable {
		_ = supervise.ResetBudget(state)
		recordOutcome("rewake")
		return claudehook.BlockStop(stderr, withAFKBanner(state, fmt.Sprintf(rewakeBannerFmt, reason)))
	}

	// The wait ended with its window and nothing queued. Exiting quietly
	// would leave an idle CFO with no hook watching, deaf to every wake
	// after the window, so the CFO is woken to end a turn, which re-arms it.
	if healthy {
		_ = supervise.ResetBudget(state)
		recordOutcome("rewake")
		return claudehook.BlockStop(stderr, windowEndedBanner)
	}

	// Genuine failure: neither actionable nor healthy, and the need has
	// not vanished - supervision could not be re-established this firing.
	//
	// Requirement 6: watch.Run deliberately does not publish an episode
	// on its own error paths, because a transient stat failure must not
	// churn the recovery generation on every retry inside package watch.
	// This arm owns that publish instead, exactly ONCE per firing, here
	// where the outcome is finally settled, regardless of which error
	// flavor closed the attempt loop: a live-but-wedged watcher returns
	// lock.ErrHeld from a stale-beat home and IS the dominant
	// watcher-down case, not an exemption from this publish. Publishing
	// earlier (inside the loop, once per attempt, before HEALTHY is even
	// checked) would both spuriously mark a HEALTHY home as down and
	// churn the generation across every attempt and every repeat Stop of
	// a sustained episode.
	_, _ = wake.PublishEpisode(state)

	// Requirement 4: MarkNotified is called on every failure path below,
	// not only the first-in-episode branch - it is the only writer of
	// this marker, and the turnend-guard's normal escalation arm is
	// unreachable without it having fired at least once. NotifiedOnce is
	// read BEFORE marking so the branch below still correctly
	// distinguishes first from repeat.
	firstInEpisode := !supervise.NotifiedOnce(state)
	_ = supervise.MarkNotified(state)

	if firstInEpisode {
		recordOutcome("failed")
		lastErrText := "watcher closed without an actionable reason"
		if lastErr != nil {
			lastErrText = lastErr.Error()
		}
		if stalled := stalledWatcher(state, watcherGrace, attemptsRun); stalled != "" {
			return claudehook.BlockStop(stderr, stalled)
		}
		if watcherProblem == "" {
			watcherProblem = "healthy when last looked at"
		}
		return claudehook.BlockStop(stderr, fmt.Sprintf(failureBannerFmt, attemptsRun, lastErrText, watcherProblem))
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
	//
	// waitForAlarm polls rather than reading AlarmFired once: this firing
	// can settle its own failure in well under 150ms (a wedged foreign
	// .watch.lock holder fails watch.Run's acquire near-instantly), but
	// the sibling turnend-guard's own decision on this SAME Stop first
	// burns up to CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS polling for autoarm
	// proof (pollAutoarmProof) before it ever calls MarkAlarm. A single
	// immediate read here would deterministically race ahead of that and
	// see AlarmFired still false, returning exit 2 and re-waking the
	// agent with empty stderr on the exact turn the guard's attended
	// fail-open was meant to release.
	recordOutcome("failed-suppressed")
	if waitForAlarm(state, syncWait) {
		return 0
	}
	return 2
}

// sessionStartNudgeLine is the SessionStart routing shortcut's exact
// stdout for a resume/reload/fork whose completion marker already names the
// CURRENT custody's owner: printing a full digest again would just repeat
// what an earlier SessionStart under the same lock already showed.
const sessionStartNudgeLine = "CFO: operational input may be waiting; run cfo drain if supervision was active. For an explicit user decision, publish cfo question --id <stable-id> --text <question> [--option <choice>] [--recommend <exact-choice>] from the registered primary shell. Answers return as normal messages to the same CFO, not native prompt-tool responses."

// withAFKBanner is text followed, while AFK mode is on, by its banner on a
// line of its own: a rewake and a resumed session each tell the CFO it is on
// and where its terms are.
func withAFKBanner(stateDir, text string) string {
	if banner := afk.BannerFor(stateDir); banner != "" {
		return text + "\n" + banner
	}
	return text
}

// resolveSessionOwnerPID identifies the process taking custody of the
// session lock for a SessionStart digest. A registered native terminal can
// hold custody through its cmd shim rather than the nearest node ancestor.
// Reuse that verified custodian only when this command runs beneath it.
// Otherwise retain the hook's ancestor selection and test seam.
func resolveSessionOwnerPID(stateDir string) int {
	if os.Getenv("CFO_TEST_ANCESTOR_PID") == "" {
		if holder, err := lock.Read(stateDir); err == nil && holder.VerifiedAlive() {
			if ancestry, err := proc.Ancestry(os.Getpid(), 32); err == nil {
				for _, ancestor := range ancestry {
					if ancestor.PID == holder.PID {
						return holder.PID
					}
				}
			}
		}
	}
	if pid, ok := resolveAncestorPID(); ok {
		return pid
	}
	return os.Getpid()
}

// hookSessionStart is the SessionStart hook entry point. Unlike every other
// hook in this file, its stdout is PLAIN TEXT with exit 0 always, never the
// {"systemMessage":"..."} envelope: Claude Code injects SessionStart stdout
// into the session context verbatim, so a JSON wrapper would deliver the
// whole digest as one escaped blob. It always exits 0, even when
// ComposeBrief fails: a SessionStart exit 2 would block session init
// entirely, so a ComposeBrief failure is rendered as digest text (SESSION
// START DEGRADED) instead of a nonzero exit.
//
// Routing: startup, new, clear, compact, empty, or any source this build
// does not recognize all take the same single code path below, the brief
// digest, with no marker consulted. resume, reload, and fork take the
// short-circuit nudge instead, but only when state\.session-start-complete
// names THIS resolved owner and lock.HeldBy confirms that owner still holds
// the home; otherwise they fall through to the same brief digest, so a
// session resuming into a home that never received a digest under this
// custody does not start blind.
//
// The digest is the brief one for every source because Claude Code hands a
// session a hook's output whole only up to digest.Limit: the long digest
// arrived as a preview of its first 2 KB, a session lock and a few wake
// lines, under a contract saying the session had read every file.
func hookSessionStart(h home.Home, payload claudehook.Payload, stdout io.Writer) int {
	ownerPID := resolveSessionOwnerPID(h.State)

	switch payload.Source {
	case "resume", "reload", "fork":
		if markerPID, ok := digest.ReadCompleteMarker(h.State); ok && markerPID == ownerPID && lock.HeldBy(h.State, ownerPID) {
			fmt.Fprintln(stdout, withAFKBanner(h.State, sessionStartNudgeLine))
			registerPrimary(h, ownerPID, "claude", payload.SessionID, stdout)
			return 0
		}
	}

	if err := digest.ComposeBrief(h, ownerPID, payload.SessionID, payload.Source == "compact", stdout); err != nil {
		fmt.Fprintf(stdout, "SESSION START DEGRADED: %s\n", err)
	}
	registerPrimary(h, ownerPID, "claude", payload.SessionID, stdout)
	return 0
}

// waitForAlarm polls supervise.AlarmFired for up to syncWait, checking
// immediately and then every 100ms (or whatever remains, whichever is
// shorter) until syncWait elapses. syncWait <= 0 means exactly one check
// and no waiting at all. Mirrors pollAutoarmProof's shape for the same
// reason: a fast decision on one side of a two-process race must not read
// state before the slower side has had its full window to write it.
func waitForAlarm(state string, syncWait time.Duration) bool {
	if supervise.AlarmFired(state) {
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
		if supervise.AlarmFired(state) {
			return true
		}
	}
}
