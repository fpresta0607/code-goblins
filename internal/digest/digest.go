// Package digest composes the session-start digest in its two forms: the
// brief one a CFO session sees at the top of context when Claude Code fires
// the SessionStart hook (ComposeBrief), and the long one that hook writes to
// a file and an operator prints with the manual `cfo session-start` alias
// (Compose). Composition never shells out and never touches the network, so
// the whole package stays inside the 1s render budget by construction; NOT
// PORTED IN V1: upstream's 120s subprocess watchdog has nothing to guard
// here, since there is no subprocess stage.
package digest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/claudehook"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/layout"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/reap"
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

// Limit is the most hook output Claude Code hands a session whole. Anything
// longer is saved to a file and the session is given a preview of about 2 KB
// with the file's path. Measured on Claude Code 2.1.286 from the hook records
// in its own transcripts, which keep both what a hook printed and what the
// session was given: the longest output passed whole was 9,293 characters and
// the shortest replaced was 10,026.
const Limit = 10000

// Budget is the size ComposeBrief keeps its digest within, in bytes, which
// are never fewer than characters: a margin under Limit for whatever wraps
// the output on its way in.
const Budget = 9000

// FullDigestFile is the long digest's basename under the state folder: what
// ComposeBrief leaves out, written beside the lock it was composed under.
const FullDigestFile = "session-digest.md"

// ReadCompleteMarker returns the owner pid recorded in
// state\.session-start-complete, and whether the marker exists and parses.
// Any read or parse failure, including a missing file, reads as absent
// rather than an error: a missing or malformed marker means "no digest was
// recorded for any owner", which is exactly the fall-through-to-Compose
// case hook routing wants for anything it cannot positively confirm.
func ReadCompleteMarker(stateDir string) (int, bool) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, CompleteMarkerFile))
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

func (e *werr) write(text []byte) {
	if e.err != nil {
		return
	}
	_, e.err = e.w.Write(text)
}

// Compose writes the long session-start digest to w, in this exact section
// order: SESSION LOCK, AFK MODE while it is on, WAKE QUEUE, SUPERVISION
// OPERATING INSTRUCTIONS, READ-ONCE CONTRACT, FLEET STATE, ORPHANS, CONTEXT,
// NEXT STEP. It is what `cfo session-start` prints by hand; a hook prints
// ComposeBrief, because a session is handed a hook's output whole only up to
// Limit.
//
// A read failure anywhere below SESSION LOCK - a per-file read inside FLEET
// STATE or CONTEXT (a path that exists but cannot be read as text, e.g. a
// directory in a file's place), a wake-queue read, or a failure to list
// state\ itself - renders inline (as "<name>: UNREADABLE (<err>)" or the
// section's own equivalent) and composition continues with every remaining
// section; a missing path renders as "<name>: ABSENT". Composition is never
// aborted by a read failure. The ONLY things that can make Compose return a
// non-nil error are a write error on w itself (every section writes through
// the werr latch below, so one broken write silently no-ops every later
// print and is surfaced here) and a failure to write the completion marker.
// The caller is responsible for rendering a returned error as digest text
// (never a nonzero exit; see cmd/cfo/hook.go's session-start case).
//
// ownerPID identifies the process taking custody of the session lock (see
// lock.AcquireOwner); session is the Claude session id to record against
// that custody, or "" for a manual, session-less invocation. On success
// under a lock this call actually holds, AND only when composition itself
// produced no write error, Compose atomically writes
// state\.session-start-complete naming ownerPID: a marker written despite a
// truncated or write-broken digest would tell a later resume/reload/fork
// that this custody already saw a full digest when it did not, which is
// exactly the start-blind failure the marker's fall-through exists to
// prevent (see cmd/cfo/hook.go's session-start routing).
func Compose(h home.Home, ownerPID int, session string, w io.Writer) error {
	ew := &werr{w: w}

	heldLock := writeSessionLock(h.State, ownerPID, session, ew)
	composeLong(h, ew)

	if heldLock && ew.err == nil {
		if err := writeCompleteMarker(h.State, ownerPID); err != nil {
			return err
		}
	}

	return ew.err
}

func writeCompleteMarker(stateDir string, ownerPID int) error {
	return fsx.AtomicWriteFile(filepath.Join(stateDir, CompleteMarkerFile), []byte(strconv.Itoa(ownerPID)+"\n"))
}

// composeLong writes every section of the long digest below SESSION LOCK.
// The read-once contract comes before the sections it speaks for, so those
// are composed first: the contract names the files they actually printed.
func composeLong(h home.Home, ew *werr) {
	writeAFKMode(h.State, ew)
	writeWakeQueue(h.State, math.MaxInt, math.MaxInt, ew)
	writeSupervisionInstructions(h.Data, false, ew)

	statusTail := claudehook.Int("CFO_SESSION_START_STATUS_TAIL", 5, 0, 1000000)
	queuedLimit := claudehook.Int("CFO_SESSION_START_QUEUED_LIMIT", 20, 0, 1000000)
	var fleetState, context bytes.Buffer
	metas := writeFleetState(h, statusTail, queuedLimit, &werr{w: &fleetState})
	files := writeContext(h.Data, &werr{w: &context})

	writeReadOnceContract(append(files, metas...), statusTail, queuedLimit, ew)
	ew.write(fleetState.Bytes())
	writeOrphans(h.State, ew)
	ew.write(context.Bytes())
	writeNextStep(ew)
}

// ComposeBrief writes the digest a session is handed whole: within Budget
// however large the home is, in the order a CFO needs it. SESSION LOCK, AFK
// MODE while it is on, WAKE QUEUE with its ack line, SUPERVISION OPERATING
// INSTRUCTIONS, FLEET with one line a goblin, READ THIS NEXT naming the file
// that holds the long digest, READ-ONCE CONTRACT, NEXT STEP.
//
// AFK mode's notice is printed whole, as the long digest prints it: its terms
// are what a session starting from this digest decides under.
//
// A wake queue or fleet too long for its share keeps its first lines and
// says how many it left out; a wake queue cut short prints no ack line,
// because that line retires every record at or below its sequence and the
// session has not seen them all.
//
// The long digest is written to state\FullDigestFile only under a lock this
// call holds, as every other mutation is: a session that does not hold the
// home is told to print the long form with `cfo session-start` instead. The
// completion marker is written as Compose writes it.
func ComposeBrief(h home.Home, ownerPID int, session string, isAfterCompact bool, w io.Writer) error {
	var lockSection bytes.Buffer
	heldLock := writeSessionLock(h.State, ownerPID, session, &werr{w: &lockSection})

	// A tool cuts long output short just as a hook does, so the fallback
	// names the two files a session must not go without beside the command.
	fallback := fmt.Sprintf("read %s and %s themselves; \"cfo session-start\" prints the long digest, which is too long for a tool to hand back whole.", filepath.Join(h.Data, "overlord.md"), filepath.Join(h.Data, filepath.FromSlash(layout.MemoryIndex)))
	next := "this session does not hold the home, so no long digest was written: " + fallback
	if heldLock {
		var long bytes.Buffer
		composeLong(h, &werr{w: &long})
		path := filepath.Join(h.State, FullDigestFile)
		if err := fsx.AtomicWriteFile(path, long.Bytes()); err != nil {
			next = fmt.Sprintf("the long digest could not be written (%s): %s", err, fallback)
		} else {
			next = path + " holds what this digest leaves out: the backlog's queued rows, every state\\*.meta in full with its status tail, the orphan sweep, and data\\projects.md, data\\overlord.md and data\\memory\\MEMORY.md in full. Read all of it, in parts if your tool caps a read, before acting on anything not shown here."
		}
	}

	var afkMode, instructions bytes.Buffer
	writeAFKMode(h.State, &werr{w: &afkMode})
	writeSupervisionInstructions(h.Data, true, &werr{w: &instructions})
	checkpoint := ""
	if isAfterCompact {
		path := filepath.Join(h.State, CheckpointFile)
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && time.Since(info.ModTime()) <= 15*time.Minute {
			checkpoint = path + " holds the checkpoint written before compaction. Read it first.\n"
		} else {
			checkpoint = "no checkpoint was written before this compaction, or the one on disk is stale or unreadable; run cfo install to register the pre-compact hook.\n"
		}
	}
	tail := func(isWholeQueue, isWholeFleet bool) []byte {
		var text bytes.Buffer
		ew := &werr{w: &text}
		ew.println("== READ THIS NEXT ==")
		ew.printf("%s", checkpoint)
		ew.println("READ THIS NEXT: " + next)
		writeBriefContract(isWholeQueue, isWholeFleet, ew)
		writeNextStep(ew)
		return text.Bytes()
	}

	// The wake queue and the fleet share what the fixed sections leave: the
	// fleet is held to two fifths of it only while the queue needs the rest.
	room := Budget - lockSection.Len() - afkMode.Len() - instructions.Len() - len(tail(false, false))
	fleetTable, isWholeFleet := briefFleet(h.State, room)
	var wakeQueue bytes.Buffer
	isWholeQueue := writeWakeQueue(h.State, max(room*3/5, room-len(fleetTable)), briefErrorWidth, &werr{w: &wakeQueue})
	if wakeQueue.Len() > room-len(fleetTable) {
		fleetTable, isWholeFleet = briefFleet(h.State, max(0, room-wakeQueue.Len()))
	}

	ew := &werr{w: w}
	ew.write(lockSection.Bytes())
	ew.write(afkMode.Bytes())
	ew.write(wakeQueue.Bytes())
	ew.write(instructions.Bytes())
	ew.write(fleetTable)
	ew.write(tail(isWholeQueue, isWholeFleet))

	if heldLock && ew.err == nil {
		if err := writeCompleteMarker(h.State, ownerPID); err != nil {
			return err
		}
	}
	return ew.err
}

// briefFleet is the FLEET section within room bytes: one line a goblin, its
// harness and model, its kind, and its last status line cut to a table's
// width, and whether every goblin is listed.
func briefFleet(stateDir string, room int) ([]byte, bool) {
	var section bytes.Buffer
	ew := &werr{w: &section}
	ew.println("== FLEET ==")
	scan, err := state.ScanIDs(stateDir)
	if err != nil {
		ew.printf("state\\: UNREADABLE (%s)\n", cutError(err, briefErrorWidth))
	}
	if len(scan.MetaIDs) == 0 {
		ew.println("(no goblins in flight)")
		return section.Bytes(), err == nil
	}
	more := func(left int) string {
		return fmt.Sprintf("(+%d more goblins: see FLEET STATE in the file named under READ THIS NEXT)\n", left)
	}
	for listed, id := range scan.MetaIDs {
		line := fleetLine(stateDir, id)
		if section.Len()+len(line)+len(more(len(scan.MetaIDs))) > room {
			ew.printf("%s", more(len(scan.MetaIDs)-listed))
			return section.Bytes(), false
		}
		ew.printf("%s", line)
	}
	return section.Bytes(), err == nil
}

// fleetStatusWidth is how much of a goblin's last status line the FLEET
// table shows.
const fleetStatusWidth = 80

// briefErrorWidth is how many bytes of a read failure's text the brief
// prints inline: an error can quote the whole of what it could not read.
const briefErrorWidth = 200

// cutError is err's text, cut to width bytes.
func cutError(err error, width int) string {
	text := err.Error()
	if len(text) <= width {
		return text
	}
	return strings.ToValidUTF8(text[:width], "") + "..."
}

func fleetLine(stateDir, id string) string {
	meta, err := state.ReadMeta(filepath.Join(stateDir, id+".meta"))
	if err != nil {
		return fmt.Sprintf("%s  UNREADABLE (%s)\n", id, cutError(err, briefErrorWidth))
	}
	status := "no status yet"
	if tail, err := state.TailStatus(stateDir, id, 1); err != nil {
		status = fmt.Sprintf("status UNREADABLE (%s)", err)
	} else if len(tail) == 1 {
		status = tail[0]
	}
	if runes := []rune(status); len(runes) > fleetStatusWidth {
		status = string(runes[:fleetStatusWidth]) + "..."
	}
	if record, err := state.ReadLifecycle(stateDir, id); err == nil && record.Generation == meta["spawn_gen"] && record.Phase == "paused" && record.Pause != nil {
		status = record.Pause.Description()
	}
	return fmt.Sprintf("%s  %s/%s  %s  %s\n", id, meta["harness"], meta["model"], meta["kind"], status)
}

// writeBriefContract is the brief's read-once contract. The brief prints no
// file in full, so it names none, and it says which of its own sections are
// whole: what it tells the CFO it has read is only what it handed over.
func writeBriefContract(isWholeQueue, isWholeFleet bool, ew *werr) {
	ew.println("== READ-ONCE CONTRACT ==")
	writePrintedInFull(nil, ew)
	queue, fleet := "the whole wake queue", "one line for every goblin"
	if !isWholeQueue {
		queue = "only the first lines of the wake queue"
	}
	if !isWholeFleet {
		fleet = "one line for only the first goblins"
	}
	ew.printf("This digest printed %s and %s. It printed no file in full: you have not read data\\projects.md, data\\overlord.md, data\\memory\\MEMORY.md, any state\\*.meta, any status log, the backlog or the orphan sweep until you read the file named under READ THIS NEXT.\n", queue, fleet)
}

// printedInFullPrefix starts the one line of a read-once contract that names
// every file the digest printed whole, as home-relative paths.
const printedInFullPrefix = "PRINTED IN FULL: "

func writePrintedInFull(files []string, ew *werr) {
	if len(files) == 0 {
		ew.println(printedInFullPrefix + "none")
		return
	}
	ew.println(printedInFullPrefix + strings.Join(files, ", "))
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

// writeAFKMode prints AFK mode's notice while it is on, ahead of the wake
// queue, so the CFO reads what it decides itself and what stays the
// Overlord's alone before anything that waits on it: who turned it on, when
// and from where, and its terms. It prints nothing while AFK mode is off, and
// says so inline when its switch cannot be read.
func writeAFKMode(stateDir string, ew *werr) {
	lines := afk.NoticeFor(stateDir)
	if len(lines) == 0 {
		return
	}
	ew.println("== AFK MODE ==")
	for _, line := range lines {
		ew.println(line)
	}
}

// writeWakeQueue reads the raw pending wake records and current episode,
// then hands them to wake.Render, the same renderer `cfo drain` uses, so the
// two presentations can never drift in format. This section never acks:
// Pending and ReadEpisode are read-only calls that create nothing.
//
// A Pending or ReadEpisode read failure (e.g. a corrupt .wake-queue line)
// degrades this section inline as "WAKE QUEUE: UNREADABLE (<err>)" and
// returns, never aborting the rest of Compose - the same per-file-scoped
// treatment CONTEXT and FLEET STATE already give a bad file. Only a write
// failure from the renderer itself (a failure of w, not of the wake state) is
// latched onto ew as the Compose-level failure.
//
// The section stays within room bytes, and the result is whether it listed
// the whole queue: one too long for room keeps its first records and
// withholds the ack line (see wake.RenderWithin). A read failure's text is
// cut to errorWidth bytes, because it can quote a whole corrupt queue line.
func writeWakeQueue(stateDir string, room, errorWidth int, ew *werr) bool {
	const header = "== WAKE QUEUE =="
	ew.println(header)
	records, err := wake.Pending(stateDir)
	if err != nil {
		ew.printf("WAKE QUEUE: UNREADABLE (%s)\n", cutError(err, errorWidth))
		return false
	}
	episode, err := wake.ReadEpisode(stateDir)
	if err != nil {
		ew.printf("WAKE QUEUE: UNREADABLE (%s)\n", cutError(err, errorWidth))
		return false
	}
	if ew.err != nil {
		return false
	}
	isWhole, err := wake.RenderWithin(ew.w, records, episode, time.Now().UTC(), room-len(header)-1)
	if err != nil {
		ew.err = err
	}
	return isWhole
}

// writeSupervisionInstructions prints the fixed operating-instructions
// block: v1 cuts (gate-agent refusal, network stage, *.check.sh
// sweeps, pane/window staleness, procevent sources, X-mode) mean this text
// stays short relative to upstream's equivalent. It names the memory folder
// by its full path, because a CFO often runs in a project rather than in the
// home.
//
// The brief digest prints neither CONTEXT nor ORPHANS, so for it the two
// sentences that point at those sections point at the long digest instead.
func writeSupervisionInstructions(dataDir string, isBrief bool, ew *werr) {
	ew.println("== SUPERVISION OPERATING INSTRUCTIONS ==")
	memory := filepath.Join(dataDir, filepath.FromSlash(path.Dir(layout.MemoryIndex)))
	indexPrinted, orphans := "printed in full under CONTEXT below", "The ORPHANS section below"
	if isBrief {
		indexPrinted, orphans = "printed in full in the file named under READ THIS NEXT", "The ORPHANS section of the file named under READ THIS NEXT"
	}
	ew.printf("The CFO's memory is %s, the same for Claude Code, Codex and Pi: MEMORY.md there is its index, %s, one line per fact. Read a fact's own file when its line bears on the work. When you learn something durable, write it there as one file (frontmatter name, description and type, then the fact) and add its one line to MEMORY.md, never only to a harness's own memory folder, which the next CFO may not see.\n", memory, indexPrinted)
	ew.println("For an explicit user decision, the registered primary CFO uses cfo question --id <stable-id> --text <question> --option <choice> --recommend <exact-choice>. Repeat --option for real choices; omit --recommend unless you recommend one. Other always permits a written answer.")
	ew.println("This contract applies to Claude Code, Codex and Pi: publish from this primary session's shell, then continue independent work or end the turn awaiting the answer. The durable answer returns to the same CFO as a normal message; it does NOT answer a pending native prompt tool. Do not open a native prompt for the same decision or promote worker wake diagnostics into user questions.")
	ew.println("For a nonblocking presentation, use a Scrawl page (lavish-axi <file> --no-open) and cfo present --id <stable-id> --kind review --url <returned-safe-url> from this registered primary; workers add --task and --generation. Report only a successful tool result, refresh only while live and finish with --state ended. Do not wait for a viewing choice or claim URL opening mirrors browser control; see docs/native-board.md.")
	ew.println("The Stop-owned auto-arm hook owns watcher continuity: it hosts the watcher in-process across every turn for up to 8 hours.")
	ew.println("Never run \"cfo watch\" from the agent shell. The watcher is armed and supervised by that hook alone; running it manually bypasses supervision.")
	ew.println("Wakes arrive as rewake turns: a Stop hook exit 2 reopens the turn with an operational reason (a signal, a stale sweep, or a heartbeat), not a fresh session.")
	ew.println("Every drain presentation ends with a WAKE_ACK_REQUIRED command. Run it after handling what cfo drain printed, or the same records resurface on the next drain.")
	ew.println("That command is REFUSED when unanswered questions sit at or below its sequence: drain lists every waiting goblin and retires nothing. The refusal is the protection working, not an error - a record acked unread is a record nobody will ever read.")
	ew.println("Answer each listed goblin with \"cfo send <id> \"...\"\", then re-run the same command with --ack-blocking. That flag is range-scoped, not per-record: it retires EVERY question at or below the sequence, and the refusal listing is the whole set it will retire. The ack floor only moves forward, so a later question cannot be retired while an earlier one is kept - to hold one open, handle it first or ack a range that stops below its sequence.")
	ew.println("Supervision is needed whenever tasks are in flight: any state\\*.meta file with no terminal status keeps the turn-end guard watching for a live watcher.")
	ew.println(orphans + " is the fleet nothing else reports: a harness process with no pane is unsupervised and may still be spending tokens. Run \"cfo reap\" to re-sweep and \"cfo reap --apply\" to retire everything it found except a process: ending one is authorised only by naming its pid, as \"cfo reap --force <pid> --apply\". A HELD line says why that one was left alone.")
}

// writeReadOnceContract names every source this digest already printed, so
// the agent does not spend a turn re-reading what it was just handed. The
// files it names as printed in full are the ones the sections below actually
// printed (printed), never a fixed list: a file that was absent or unreadable
// is not one the agent has read. The backlog and each status log are capped
// (queuedLimit queued rows, statusTail status lines), so the contract says so
// explicitly rather than claiming a fresh read of a capped source would show
// nothing new - an overflowed backlog or status log still has a real, unshown
// remainder.
func writeReadOnceContract(printed []string, statusTail, queuedLimit int, ew *werr) {
	ew.println("== READ-ONCE CONTRACT ==")
	writePrintedInFull(printed, ew)
	ew.printf("This digest also printed data\\backlog.md's first %d queued rows (not the full backlog) and each goblin's last %d status lines (not the full log).\n", queuedLimit, statusTail)
	ew.println("It also printed the last recorded orphan sweep, with its status-log listing capped; the full finding set is in state\\.reap-audit.json and in \"cfo reap --json\".")
	ew.println("Do not re-read a file named on the PRINTED IN FULL line this turn. A backlog or status section that hit its cap only needs a fresh read for what is past the cap, not for what is already shown.")
	ew.println("That line holds only for a reader that reached NEXT STEP at the end of this digest: one handed a part of it, by a tool that cut it short or a read that stopped early, has read less.")
}

// writeNextStep prints the fixed two-line closing reminder.
func writeNextStep(ew *werr) {
	ew.println("== NEXT STEP ==")
	ew.println("Follow the SUPERVISION OPERATING INSTRUCTIONS above for how to proceed from here.")
	ew.println("This digest never arms anything itself: the Stop-owned auto-arm hook is the only thing that starts the watcher, on the next Stop.")
}

// writeFleetState prints the backlog compact listing, then every
// state\*.meta with its status tail, then orphan .status files (a status log
// with no matching meta) by name only, then "(no goblins in flight)" when
// there were no metas at all. A failure to list h.State itself (beyond a
// simply-missing directory, which reads as no goblins at all) renders
// inline as "state\: UNREADABLE (<err>)", same as every other per-file read
// failure in this section: it never propagates as a Compose-level failure.
// It returns the metas it printed in full, as the read-once contract names
// them.
func writeFleetState(h home.Home, statusTail, queuedLimit int, ew *werr) []string {
	ew.println("== FLEET STATE ==")
	writeBacklog(h, queuedLimit, ew)

	scan, err := state.ScanIDs(h.State)
	if err != nil {
		ew.printf("state\\: UNREADABLE (%s)\n", err)
	}

	var printed []string
	for _, id := range scan.MetaIDs {
		if writeMetaEntry(h.State, id, statusTail, ew) {
			printed = append(printed, `state\`+id+".meta")
		}
	}
	for _, id := range scan.OrphanStatusIDs {
		ew.printf("%s.status: orphan status log, no matching meta\n", id)
	}
	if len(scan.MetaIDs) == 0 {
		ew.println("(no goblins in flight)")
	}
	return printed
}

// writeOrphans prints the last recorded orphan sweep: harness processes with
// no pane, dev servers left running in finished worktrees, and the worktree,
// metadata and status records nothing retired. It reads the persisted record
// rather than sweeping, because this package never shells out and a sweep
// needs Herdr and the process table; the watcher takes the sweep on its timer
// and leaves the record here. A home that has never been swept says so, and a
// sweep that failed says that too, so an unreadable fleet never reads as a
// clean one.
func writeOrphans(stateDir string, ew *werr) {
	ew.println("== ORPHANS ==")
	if ew.err != nil {
		return
	}
	if err := reap.RenderRecord(ew.w, stateDir, time.Now()); err != nil {
		ew.err = err
	}
}

// writeBacklog prints the first limit rows of data\backlog.md's Queued
// section, in file order, plus a "(+N more queued)" overflow line when more
// remain. It reads the backlog as fleet-view and the board do, so a parked or
// done row, or a row's detail lines, is never listed as queued work.
func writeBacklog(h home.Home, limit int, ew *werr) {
	backlog, err := fleet.ReadBacklog(h)
	if err != nil {
		ew.printf("backlog.md: UNREADABLE (%s)\n", err)
		return
	}
	if !backlog.Present {
		ew.println("backlog.md: ABSENT")
		return
	}

	var queued []string
	for _, row := range backlog.Queued {
		if row.Structured {
			queued = append(queued, row.Raw)
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
// nothing if the status log does not exist or is empty). It reports whether
// the meta was read and printed.
func writeMetaEntry(stateDir, id string, statusTail int, ew *werr) bool {
	ew.printf("%s.meta:\n", id)
	lines, err := fsx.ReadLines(filepath.Join(stateDir, id+".meta"))
	if err != nil {
		ew.printf("  UNREADABLE (%s)\n", err)
	} else {
		for _, line := range lines {
			ew.printf("  %s\n", line)
		}
	}
	isPrinted := err == nil
	if meta, err := state.ReadTaskMeta(stateDir, id); err == nil {
		if record, err := state.ReadLifecycle(stateDir, id); err == nil && record.Generation == meta.SpawnGen && record.Phase == "paused" && record.Pause != nil {
			ew.printf("  paused: %s\n", record.Pause.Description())
		}
	}

	tail, err := state.TailStatus(stateDir, id, statusTail)
	if err != nil {
		ew.printf("  status: UNREADABLE (%s)\n", err)
		return isPrinted
	}
	if len(tail) == 0 {
		return isPrinted
	}
	ew.printf("  -- status tail (last %d) --\n", len(tail))
	for _, line := range tail {
		ew.printf("  %s\n", line)
	}
	return isPrinted
}

// writeContext prints data\projects.md, data\overlord.md, and the memory
// index data\memory\MEMORY.md, each in full, or as "<name>: ABSENT" /
// "<name>: (present, empty)" / "<name>: UNREADABLE (<err>)". A fact's own
// file is never printed: the index is what every session pays for. It
// returns the files it printed in full, as the read-once contract names them.
func writeContext(dataDir string, ew *werr) []string {
	ew.println("== CONTEXT ==")
	var printed []string
	for _, name := range []string{"projects.md", "overlord.md", filepath.FromSlash(layout.MemoryIndex)} {
		if writeContextFile(dataDir, name, ew) {
			printed = append(printed, `data\`+name)
		}
	}
	return printed
}

// writeContextFile prints one context file and reports whether it had any
// lines to print.
func writeContextFile(dataDir, name string, ew *werr) bool {
	lines, err := fsx.ReadLines(filepath.Join(dataDir, name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			ew.printf("%s: ABSENT\n", name)
			return false
		}
		ew.printf("%s: UNREADABLE (%s)\n", name, err)
		return false
	}
	if len(lines) == 0 {
		ew.printf("%s: (present, empty)\n", name)
		return false
	}
	ew.printf("%s:\n", name)
	for _, line := range lines {
		ew.println(line)
	}
	return true
}
