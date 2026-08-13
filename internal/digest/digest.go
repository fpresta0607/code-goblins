// Package digest composes the session-start digest: the seven fixed
// sections a CFO session sees at the top of context when Claude Code fires
// the SessionStart hook (or an operator runs the manual `cfo session-start`
// alias). Composition never shells out and never touches the network, so
// the whole package stays inside the 1s render budget by construction; NOT
// PORTED IN V1: upstream's 120s subprocess watchdog has nothing to guard
// here, since there is no subprocess stage.
package digest

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/claudehook"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// CompleteMarkerFile is the SessionStart completion marker's basename.
// Compose writes it, containing the acquiring owner pid, whenever it
// composes a full digest under a lock it actually holds. Hook routing reads
// it (see cmd/cfo/hook.go) to decide whether a resume/reload/fork
// SessionStart already saw a digest under the CURRENT custody and can be
// answered with a short nudge instead of a full re-render. It is the
// marker's only consumer and the only reason the marker is written at all.
const CompleteMarkerFile = ".session-start-complete"

// ReadCompleteMarker returns the owner pid recorded in
// state\.session-start-complete, and whether the marker exists and parses.
// Any read or parse failure, including a missing file, reads as absent
// rather than an error: a missing or malformed marker means "no digest was
// recorded for any owner", which is exactly the fall-through-to-Compose
// case hook routing wants for anything it cannot positively confirm.
func ReadCompleteMarker(stateDir string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(stateDir, CompleteMarkerFile))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

// werr wraps an io.Writer and latches the first write error, letting every
// section writer below write a straight sequence of lines without an err
// check after each call. Once err is set, every further print is a no-op:
// that has the same user-visible effect as literally stopping composition
// (nothing more reaches w), and Compose surfaces the latched error to its
// caller at the end so a broken output stream is still reported as the
// Compose-level failure the contract requires.
type werr struct {
	w   io.Writer
	err error
}

func (e *werr) println(line string) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintln(e.w, line)
}

func (e *werr) printf(format string, a ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, a...)
}

// Compose writes the full session-start digest to w, in this exact section
// order: SESSION LOCK, WAKE QUEUE, SUPERVISION OPERATING INSTRUCTIONS,
// READ-ONCE CONTRACT, FLEET STATE, CONTEXT, NEXT STEP.
//
// A per-file read failure inside FLEET STATE or CONTEXT (a path that exists
// but cannot be read as text, e.g. a directory in a file's place) renders
// inline as "<name>: UNREADABLE (<err>)" and composition continues with
// every remaining section; a missing path renders as "<name>: ABSENT". Only
// a genuine Compose-level failure - a write error on w, or a wake-queue read
// error that is not scoped to one named file - aborts composition early and
// is returned to the caller, which is responsible for rendering it as digest
// text (never a nonzero exit; see cmd/cfo/hook.go's session-start case).
//
// ownerPID identifies the process taking custody of the session lock (see
// lock.AcquireOwner); session is the Claude session id to record against
// that custody, or "" for a manual, session-less invocation. On success
// under a lock this call actually holds, Compose atomically writes
// state\.session-start-complete naming ownerPID.
func Compose(h home.Home, ownerPID int, session string, w io.Writer) error {
	ew := &werr{w: w}

	heldLock := writeSessionLock(h.State, ownerPID, session, ew)

	if err := writeWakeQueue(h.State, ew); err != nil {
		return err
	}

	writeSupervisionInstructions(ew)
	writeReadOnceContract(ew)

	statusTail := claudehook.Int("CFO_SESSION_START_STATUS_TAIL", 5, 0, 1000000)
	queuedLimit := claudehook.Int("CFO_SESSION_START_QUEUED_LIMIT", 20, 0, 1000000)
	if err := writeFleetState(h, statusTail, queuedLimit, ew); err != nil {
		return err
	}

	writeContext(h.Data, ew)
	writeNextStep(ew)

	if heldLock {
		marker := filepath.Join(h.State, CompleteMarkerFile)
		if err := fsx.AtomicWriteFile(marker, []byte(strconv.Itoa(ownerPID)+"\n")); err != nil {
			return err
		}
	}

	return ew.err
}

// readOnlyBanner is printed verbatim (after the custody line is filled in)
// whenever this session does not hold the home: every mutating step below
// it - taking the lock, writing the completion marker, acking a wake - is
// skipped, and the rest of the digest composes read-only.
const readOnlyBannerTop = "●━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
const readOnlyBannerTitle = "●  READ-ONLY DIGEST - THIS SESSION DOES NOT HOLD THE HOME"
const readOnlyBannerFooter = "●  Every mutating step below is skipped: no lock is taken, no marker is written, no wake is acknowledged."

// writeSessionLock attempts to acquire the home's session lock for
// ownerPID/session and reports the outcome, returning whether THIS call
// actually holds the lock afterward. On any acquire error - lock.ErrHeld (a
// different live owner), lock.ErrOwnerDead (the resolved owner is already
// gone), or an I/O failure - it prints the read-only banner and continues;
// the current holder's pid/host are read separately (best-effort) since
// AcquireOwner's own failure carries no holder identity for the
// ErrOwnerDead case, where no file may have been touched at all.
func writeSessionLock(stateDir string, ownerPID int, session string, ew *werr) bool {
	ew.println("== SESSION LOCK ==")

	info, err := lock.AcquireOwner(stateDir, ownerPID, session)
	if err == nil {
		ew.printf("SESSION LOCK: held by pid %d on %s\n", info.PID, info.Hostname)
		return true
	}

	holderPID, holderHost := "unknown", "unknown"
	if holder, herr := lock.Read(stateDir); herr == nil {
		holderPID = strconv.Itoa(holder.PID)
		holderHost = holder.Hostname
	}
	ew.println(readOnlyBannerTop)
	ew.println(readOnlyBannerTitle)
	ew.printf("●  Custody: pid %s on %s (%s).\n", holderPID, holderHost, err)
	ew.println(readOnlyBannerFooter)
	ew.println(readOnlyBannerTop)
	return false
}

// writeWakeQueue reads the raw pending wake records and current episode,
// then hands them to wake.Render, the same renderer `cfo drain` uses, so the
// two presentations can never drift in format. This section never acks:
// Pending and ReadEpisode are read-only calls that create nothing.
func writeWakeQueue(stateDir string, ew *werr) error {
	ew.println("== WAKE QUEUE ==")
	records, err := wake.Pending(stateDir)
	if err != nil {
		return err
	}
	episode, err := wake.ReadEpisode(stateDir)
	if err != nil {
		return err
	}
	return wake.Render(ew.w, records, episode)
}

// writeSupervisionInstructions prints the fixed operating-instructions
// block: v1 cuts (AFK gate, gate-agent refusal, network stage, *.check.sh
// sweeps, pane/window staleness, procevent sources, X-mode) mean this text
// stays short relative to upstream's equivalent.
func writeSupervisionInstructions(ew *werr) {
	ew.println("== SUPERVISION OPERATING INSTRUCTIONS ==")
	ew.println("The Stop-owned auto-arm hook owns watcher continuity: it hosts the watcher in-process across every turn for up to 8 hours.")
	ew.println("Never run \"cfo watch\" from the agent shell. The watcher is armed and supervised by that hook alone; running it manually bypasses supervision.")
	ew.println("Wakes arrive as rewake turns: a Stop hook exit 2 reopens the turn with an operational reason (a signal, a stale sweep, or a heartbeat), not a fresh session.")
	ew.println("Every drain presentation ends with a WAKE_ACK_REQUIRED command. Run it after handling what cfo drain printed, or the same records resurface on the next drain.")
	ew.println("Supervision is needed whenever tasks are in flight: any state\\*.meta file with no terminal status keeps the turn-end guard watching for a live watcher.")
}

// writeReadOnceContract names every source this digest already printed in
// full, so the agent does not spend a turn re-reading what it was just
// handed.
func writeReadOnceContract(ew *werr) {
	ew.println("== READ-ONCE CONTRACT ==")
	ew.println("This digest already printed, in full, everything a fresh read of these sources would show right now: data\\backlog.md, every state\\*.meta, each goblin's status tail, data\\projects.md, data\\overlord.md, and data\\learnings.md.")
	ew.println("Do not re-read any of them this turn. Treat what is printed above as current; the next digest reflects whatever changes.")
}

// writeNextStep prints the fixed two-line closing reminder.
func writeNextStep(ew *werr) {
	ew.println("== NEXT STEP ==")
	ew.println("Follow the SUPERVISION OPERATING INSTRUCTIONS above for how to proceed from here.")
	ew.println("This digest never arms anything itself: the Stop-owned auto-arm hook is the only thing that starts the watcher, on the next Stop.")
}

// writeFleetState prints the backlog compact listing, then every
// state\*.meta with its status tail, then orphan .status files (a status log
// with no matching meta) by name only. Only a failure to list h.State itself
// (beyond a simply-missing directory, which reads as no goblins at all)
// propagates as a Compose-level failure; every per-file read failure inside
// this section renders inline instead.
func writeFleetState(h home.Home, statusTail, queuedLimit int, ew *werr) error {
	ew.println("== FLEET STATE ==")
	writeBacklog(h.Data, queuedLimit, ew)

	entries, err := os.ReadDir(h.State)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	var metaIDs []string
	metaSet := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if id, ok := strings.CutSuffix(e.Name(), ".meta"); ok {
			metaIDs = append(metaIDs, id)
			metaSet[id] = true
		}
	}

	var orphanIDs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if id, ok := strings.CutSuffix(e.Name(), ".status"); ok && !metaSet[id] {
			orphanIDs = append(orphanIDs, id)
		}
	}

	if len(metaIDs) == 0 {
		ew.println("(no goblins in flight)")
	}
	for _, id := range metaIDs {
		writeMetaEntry(h.State, id, statusTail, ew)
	}
	for _, id := range orphanIDs {
		ew.printf("%s.status: orphan status log, no matching meta\n", id)
	}

	return nil
}

// writeBacklog prints the first limit unchecked "- [ ]" rows of
// data\backlog.md, in file order, plus a "(+N more queued)" overflow line
// when more remain; checked "- [x]" (done) rows are never listed.
func writeBacklog(dataDir string, limit int, ew *werr) {
	lines, err := fsx.ReadLines(filepath.Join(dataDir, "backlog.md"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			ew.println("backlog.md: ABSENT")
			return
		}
		ew.printf("backlog.md: UNREADABLE (%s)\n", err)
		return
	}

	var queued []string
	for _, line := range lines {
		if strings.HasPrefix(line, "- [ ]") {
			queued = append(queued, line)
		}
	}
	total := len(queued)
	if total > limit {
		queued = queued[:limit]
	}
	for _, line := range queued {
		ew.println(line)
	}
	if total > limit {
		ew.printf("(+%d more queued)\n", total-limit)
	}
}

// writeMetaEntry prints one goblin's full state\<id>.meta contents followed
// by its status tail (the last statusTail lines of state\<id>.status, or
// nothing if the status log does not exist or is empty).
func writeMetaEntry(stateDir, id string, statusTail int, ew *werr) {
	ew.printf("%s.meta:\n", id)
	lines, err := fsx.ReadLines(filepath.Join(stateDir, id+".meta"))
	if err != nil {
		ew.printf("  UNREADABLE (%s)\n", err)
	} else {
		for _, line := range lines {
			ew.printf("  %s\n", line)
		}
	}

	tail, err := state.TailStatus(stateDir, id, statusTail)
	if err != nil {
		ew.printf("  status: UNREADABLE (%s)\n", err)
		return
	}
	if len(tail) == 0 {
		return
	}
	ew.printf("  -- status tail (last %d) --\n", len(tail))
	for _, line := range tail {
		ew.printf("  %s\n", line)
	}
}

// writeContext prints data\projects.md, data\overlord.md, and
// data\learnings.md, each in full, or as "<name>: ABSENT" /
// "<name>: (present, empty)" / "<name>: UNREADABLE (<err>)".
func writeContext(dataDir string, ew *werr) {
	ew.println("== CONTEXT ==")
	for _, name := range []string{"projects.md", "overlord.md", "learnings.md"} {
		writeContextFile(dataDir, name, ew)
	}
}

func writeContextFile(dataDir, name string, ew *werr) {
	lines, err := fsx.ReadLines(filepath.Join(dataDir, name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			ew.printf("%s: ABSENT\n", name)
			return
		}
		ew.printf("%s: UNREADABLE (%s)\n", name, err)
		return
	}
	if len(lines) == 0 {
		ew.printf("%s: (present, empty)\n", name)
		return
	}
	ew.printf("%s:\n", name)
	for _, line := range lines {
		ew.println(line)
	}
}
