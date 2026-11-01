// Package digest composes the session-start digest in its two forms: the
// brief one a CFO session sees at the top of context when Claude Code fires
// the SessionStart hook (ComposeBrief), and the long one that hook writes to
// a file and an operator prints with the manual `cfo session-start` alias
// (Compose). Composition samples current state once under a shared deadline.
// Rendering never probes terminals; memory and state have separate bounds.
package digest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/custody"
	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/layout"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/memory"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

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
// recorded for any owner".
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
// ownerPID identifies the process taking custody of the session lock, the
// harness of the CFO's own session, which the caller has proven it is (see
// custody.Take); session is the Claude session id to record against
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
	current := sampleFleet(context.Background(), h.State, fleet.NewTerminalEndpoint(h.State, nil))
	composeLong(h, current, ew)

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
func composeLong(h home.Home, current fleetSample, ew *werr) {
	writeAFKMode(h.State, ew)
	_, catalogErr := os.Lstat(filepath.Join(h.Data, filepath.FromSlash(memory.CatalogPath)))
	hasCatalog := !errors.Is(catalogErr, os.ErrNotExist)
	writeWakeQueue(h.State, 2500, briefErrorWidth, ew)
	writeSupervisionInstructions(h.Data, false, ew)

	var fleetState, context bytes.Buffer
	fmt.Fprintln(&fleetState, "== FLEET STATE ==")
	summary, _ := briefFleet(current, 3000)
	fleetState.Write(summary)
	backlog, err := fleet.ReadBacklog(h)
	if err != nil {
		fmt.Fprintf(&fleetState, "backlog.md: UNREADABLE (%s)\n", cutError(err, briefErrorWidth))
	} else {
		var queued []fleet.BacklogRow
		for _, row := range backlog.Queued {
			if row.Structured {
				queued = append(queued, row)
			}
		}
		parked := 0
		for _, row := range backlog.Parked {
			if row.Structured {
				parked++
			}
		}
		fmt.Fprintf(&fleetState, "Queued: %d; parked: %d. First queued tasks (display only):\n", len(queued), parked)
		if !backlog.Present {
			fmt.Fprintln(&fleetState, "backlog.md: ABSENT")
		}
		for _, row := range queued[:min(5, len(queued))] {
			line := row.Raw
			if len(line) > 160 {
				line = strings.ToValidUTF8(line[:160], "") + "... (display only)"
			}
			fmt.Fprintln(&fleetState, line)
		}
		if len(queued) > 5 {
			fmt.Fprintf(&fleetState, "(+%d more queued)\n", len(queued)-5)
		}
	}
	fmt.Fprintln(&fleetState, "Canonical current state: cfo fleet-view; cfo drain reads the durable work queue. Status tails above are event history, not proof of present state. No omitted record was acknowledged.")
	files := writeContext(h.Data, &werr{w: &context})

	if hasCatalog {
		ew.println("== READ-ONCE CONTRACT ==")
		writePrintedInFull(nil, ew)
		ew.println("Only the shown source ranges and bounded state summary were delivered. Cold facts, full metadata, backlog and omitted wakes require targeted retrieval before dependent action.")
	} else {
		writeReadOnceContract(files, ew)
	}
	ew.write(fleetState.Bytes())
	writeOrphans(h.State, ew)
	writeStorage(h, time.Now(), ew)
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
func ComposeBrief(h home.Home, ownerPID int, session string, isAfterCompact, isResumed bool, w io.Writer) error {
	return composeBriefWithEndpoint(context.Background(), h, ownerPID, session, isAfterCompact, isResumed, fleet.NewTerminalEndpoint(h.State, nil), w)
}

func composeBriefWithEndpoint(ctx context.Context, h home.Home, ownerPID int, session string, isAfterCompact, isResumed bool, endpoint crewstate.Endpoint, w io.Writer) error {
	var lockSection bytes.Buffer
	heldLock := writeSessionLock(h.State, ownerPID, session, &werr{w: &lockSection})
	current := sampleFleet(ctx, h.State, endpoint)
	_, catalogErr := os.Lstat(filepath.Join(h.Data, filepath.FromSlash(memory.CatalogPath)))
	hasCatalog := !errors.Is(catalogErr, os.ErrNotExist)

	// A tool cuts long output short just as a hook does, so the fallback
	// names the two files a session must not go without beside the command.
	fallback := fmt.Sprintf("read %s and %s themselves; \"cfo session-start\" prints the long digest, which is too long for a tool to hand back whole.", filepath.Join(h.Data, "overlord.md"), filepath.Join(h.Data, filepath.FromSlash(layout.MemoryIndex)))
	if hasCatalog {
		fallback = "cfo session-start prints the verified warm map read-only; use cfo memory retrieve --project <name> --task <id> --topic <topic> for relevant cold authority and facts, resolving any memory diagnostic before dependent action."
	}
	next := "this session does not hold the home, so no long digest was written: " + fallback
	hasUnchangedContext := false
	if heldLock {
		var long bytes.Buffer
		composeLong(h, current, &werr{w: &long})
		path := filepath.Join(h.State, FullDigestFile)
		isSameContext := false
		if isResumed {
			previous, err := fsx.ReadFile(path)
			previousContext := bytes.Index(previous, []byte("== CONTEXT ==\n"))
			currentContext := bytes.Index(long.Bytes(), []byte("== CONTEXT ==\n"))
			isSameContext = err == nil && previousContext >= 0 && currentContext >= 0 && bytes.Equal(previous[previousContext:], long.Bytes()[currentContext:])
		}
		if err := fsx.AtomicWriteFile(path, long.Bytes()); err != nil {
			next = fmt.Sprintf("the long digest could not be written (%s): %s", err, fallback)
		} else {
			hasUnchangedContext = isResumed && isSameContext
			next = path + " holds what this digest leaves out: the bounded canonical state projection and data\\projects.md, data\\overlord.md and data\\memory\\MEMORY.md in full. Detailed metadata, backlog, status history and omitted wakes require targeted retrieval. "
			if hasCatalog {
				next = path + " holds the verified warm operating map, memory diagnostics and bounded canonical state summary. Before dependent work use cfo memory retrieve --project <name> --task <id> --topic <topic> for relevant cold authority and facts. It does not embed the full history or cold fact files. "
			}
			if hasUnchangedContext {
				next += "The source context is unchanged. If this conversation already read those exact bytes and still holds them, do not read it again; otherwise read all source context before dependent action. Current state must be reconciled from cfo fleet-view and cfo drain."
			} else {
				next += "Read all of it, in parts if your tool caps a read, before acting on anything not shown here."
			}
		}
	}

	var afkMode, instructions, storage bytes.Buffer
	writeAFKMode(h.State, &werr{w: &afkMode})
	writeStorage(h, time.Now(), &werr{w: &storage})
	writeSupervisionInstructions(h.Data, true, &werr{w: &instructions})
	checkpoint := ""
	if isAfterCompact {
		if path, _, isFresh := FreshCheckpoint(h.State, time.Now()); isFresh {
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
		if hasCatalog {
			ew.println("== READ-ONCE CONTRACT ==")
			writePrintedInFull(nil, ew)
			ew.println("This preview does not deliver source memory. Follow READ THIS NEXT for the freshly validated warm map; retrieve cold ranges by relevance before dependent action.")
		} else {
			writeBriefContract(isWholeQueue, isWholeFleet, hasUnchangedContext, ew)
		}
		writeNextStep(ew)
		return text.Bytes()
	}

	// The wake queue and the fleet share what the fixed sections leave: the
	// fleet is held to two fifths of it only while the queue needs the rest.
	room := Budget - lockSection.Len() - afkMode.Len() - storage.Len() - instructions.Len() - len(tail(false, false))
	fleetTable, isWholeFleet := briefFleet(current, room)
	var wakeQueue bytes.Buffer
	isWholeQueue := writeWakeQueue(h.State, max(room*3/5, room-len(fleetTable)), briefErrorWidth, &werr{w: &wakeQueue})
	if wakeQueue.Len() > room-len(fleetTable) {
		fleetTable, isWholeFleet = briefFleet(current, max(0, room-wakeQueue.Len()))
	}

	ew := &werr{w: w}
	ew.write(lockSection.Bytes())
	ew.write(afkMode.Bytes())
	ew.write(wakeQueue.Bytes())
	ew.write(instructions.Bytes())
	ew.write(fleetTable)
	ew.write(storage.Bytes())
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
func briefFleet(current fleetSample, room int) ([]byte, bool) {
	var section bytes.Buffer
	ew := &werr{w: &section}
	ew.println("== FLEET ==")
	if current.err != nil {
		ew.printf("state\\: UNREADABLE (%s)\n", cutError(current.err, briefErrorWidth))
	}
	if current.orphans > 0 {
		ew.printf("%d orphan status logs: cfo reap --json reads the full finding set.\n", current.orphans)
	}
	if len(current.lines) == 0 {
		ew.println("(no goblins in flight)")
		return section.Bytes(), current.err == nil
	}
	more := func(left int) string {
		return fmt.Sprintf("(+%d more goblins: cfo fleet-view reads current state)\n", left)
	}
	for listed, line := range current.lines {
		if section.Len()+len(line)+len(more(len(current.lines))) > room {
			ew.printf("%s", more(len(current.lines)-listed))
			return section.Bytes(), false
		}
		ew.printf("%s", line)
	}
	return section.Bytes(), current.err == nil
}

const startupStateBudget = 2 * time.Second

type fleetSample struct {
	lines   []string
	orphans int
	err     error
}

func sampleFleet(ctx context.Context, stateDir string, endpoint crewstate.Endpoint) fleetSample {
	ctx, cancel := context.WithTimeout(ctx, startupStateBudget)
	defer cancel()
	scan, err := state.ScanIDs(stateDir)
	current := fleetSample{orphans: len(scan.OrphanStatusIDs), err: err}
	for _, id := range scan.MetaIDs {
		current.lines = append(current.lines, fleetLine(ctx, stateDir, id, boundedEndpoint{source: endpoint}))
	}
	return current
}

// Native terminal reads have their own longer timeout and cannot be cancelled.
// Stop waiting at the shared startup deadline; the single in-flight read then
// finishes under its existing timeout without starting another terminal probe.
type boundedEndpoint struct {
	source crewstate.Endpoint
}

func (endpoint boundedEndpoint) Read(ctx context.Context, meta state.TaskMeta) (bool, herdr.BusyState, error) {
	if err := ctx.Err(); err != nil {
		return false, herdr.BusyUnknown, err
	}
	type result struct {
		exists bool
		busy   herdr.BusyState
		err    error
	}
	answer := make(chan result, 1)
	go func() {
		exists, busy, err := endpoint.source.Read(ctx, meta)
		answer <- result{exists: exists, busy: busy, err: err}
	}()
	select {
	case value := <-answer:
		return value.exists, value.busy, value.err
	case <-ctx.Done():
		return false, herdr.BusyUnknown, ctx.Err()
	}
}

func (endpoint boundedEndpoint) Validate(ctx context.Context, meta state.TaskMeta) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	validator, ok := endpoint.source.(crewstate.StructuralValidator)
	if !ok {
		return false, nil
	}
	return validator.Validate(ctx, meta)
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

func fleetLine(ctx context.Context, stateDir, id string, endpoint crewstate.Endpoint) string {
	meta, err := state.ReadMeta(filepath.Join(stateDir, id+".meta"))
	if err != nil {
		return fmt.Sprintf("%s  UNREADABLE (%s)\n", id, cutError(err, briefErrorWidth))
	}
	current, err := crewstate.Resolve(ctx, stateDir, id, endpoint)
	if err != nil {
		return fmt.Sprintf("%s  current state UNREADABLE (%s)\n", id, cutError(err, briefErrorWidth))
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
	detail := current.Detail
	if current.State == crewstate.Unknown && ctx.Err() != nil {
		detail = "startup sample deadline reached; use cfo fleet-view"
	}
	if record, err := state.ReadLifecycle(stateDir, id); err == nil && record.Generation == meta["spawn_gen"] && current.State == crewstate.Paused && record.Pause != nil {
		detail = record.Pause.Description()
	}
	if runes := []rune(detail); len(runes) > fleetStatusWidth {
		detail = string(runes[:fleetStatusWidth]) + "..."
	}
	return fmt.Sprintf("%s  %s/%s  %s  current=%s (%s) %s; event: %s\n", id, meta["harness"], meta["model"], meta["kind"], current.State, current.Source, detail, status)
}

// writeBriefContract is the brief's read-once contract. The brief prints no
// file in full, so it names none, and it says which of its own sections are
// whole: what it tells the CFO it has read is only what it handed over.
func writeBriefContract(isWholeQueue, isWholeFleet, hasUnchangedContext bool, ew *werr) {
	ew.println("== READ-ONCE CONTRACT ==")
	writePrintedInFull(nil, ew)
	queue, fleet := "the whole wake queue", "one line for every goblin"
	if !isWholeQueue {
		queue = "only the first lines of the wake queue"
	}
	if !isWholeFleet {
		fleet = "one line for only the first goblins"
	}
	if hasUnchangedContext {
		ew.printf("This digest printed %s and %s. It printed no file in full. The file named under READ THIS NEXT contains unchanged source context; only bytes actually read and still held by this conversation count as read. Current state requires reconciliation.\n", queue, fleet)
		return
	}
	ew.printf("This digest printed %s and %s. It printed no file in full: you have not read data\\projects.md, data\\overlord.md or data\\memory\\MEMORY.md until you read the file named under READ THIS NEXT. Detailed metadata, backlog, status history and omitted wakes remain undelivered and require targeted retrieval.\n", queue, fleet)
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

// writeSessionLock takes the home's session lock for ownerPID/session, the
// harness of the CFO's own session, which the caller has proven it is, and
// reports the outcome, returning whether THIS call actually holds the lock
// afterward. A live holder that is another process loses the lock
// (custody.Take), and the section says so. On any error - lock.ErrOwnerDead
// (the owner is already gone), a takeover that could not be recorded, or an
// I/O failure - it prints the read-only banner and continues.
func writeSessionLock(stateDir string, ownerPID int, session string, ew *werr) bool {
	ew.println("== SESSION LOCK ==")

	info, replaced, err := custody.Take(stateDir, ownerPID, session, "the session-start digest")
	if err != nil {
		writeReadOnlyBanner(stateDir, err.Error(), ew)
		return false
	}
	ew.printf("SESSION LOCK: held by pid %d on %s\n", info.PID, info.Hostname)
	if replaced != nil {
		ew.println(custody.Notice(*replaced))
	}
	return true
}

// writeReadOnlyBanner prints the read-only banner with why this session does
// not hold the home. The current holder's pid and host are read separately
// (best-effort), since why may name no holder at all.
func writeReadOnlyBanner(stateDir, why string, ew *werr) {
	holderPID, holderHost := "unknown", "unknown"
	if holder, herr := lock.Read(stateDir); herr == nil {
		holderPID = strconv.Itoa(holder.PID)
		holderHost = holder.Hostname
	}
	ew.println(readOnlyBannerTop)
	ew.println(readOnlyBannerTitle)
	ew.printf("●  Custody: pid %s on %s (%s).\n", holderPID, holderHost, why)
	ew.println(readOnlyBannerFooter)
	ew.println(readOnlyBannerTop)
}

// ComposeReadOnly writes the long digest for a session that is not the CFO's
// own, such as one that prints it by hand: it takes no lock, writes nothing,
// and its SESSION LOCK section says who holds the home and why this session
// does not.
func ComposeReadOnly(h home.Home, why string, w io.Writer) error {
	ew := &werr{w: w}
	ew.println("== SESSION LOCK ==")
	writeReadOnlyBanner(h.State, why, ew)
	current := sampleFleet(context.Background(), h.State, fleet.NewTerminalEndpoint(h.State, nil))
	composeLong(h, current, ew)
	return ew.err
}

// writeStorage prints one line on the home's disk: the drive's free space
// against the floor and the mark, what the home holds by bucket, and what the
// janitor last did. It reads one system call and the janitor's record, and
// runs nothing.
func writeStorage(h home.Home, now time.Time, ew *werr) {
	line := "STORAGE: "
	settings, settingsErr := fleetconfig.Read(h.Root)
	reading, err := disk.ReadLeast(h.Root, h.DevDrive)
	switch {
	case err != nil:
		line += "free disk UNREADABLE (" + err.Error() + ")"
	case settingsErr != nil:
		line += fmt.Sprintf("%s %.1f GB free of %.0f GB; config/fleet.json UNREADABLE (%s), so the floor is unknown", reading.Drive, disk.GB(reading.Free), disk.GB(reading.Total), settingsErr)
	default:
		line += fmt.Sprintf("%s %.1f GB free of %.0f GB (floor %.0f GB, wake %.0f GB)", reading.Drive, disk.GB(reading.Free), disk.GB(reading.Total), settings.DiskFloorGB, settings.DiskWakeGB)
		if reading.Free < fleetconfig.Bytes(settings.DiskFloorGB) {
			line += ", UNDER THE FLOOR: nothing starts"
		}
	}
	record, err := janitor.ReadRecord(h.State)
	if err != nil {
		ew.println(line + "; the janitor has not swept yet")
		return
	}
	b := record.Buckets
	gb := func(bytes int64) float64 { return disk.GB(uint64(max(bytes, 0))) }
	line += fmt.Sprintf("; the home holds %.1f GB (worktrees %.1f, scratch %.1f, caches %.1f, state %.1f, data %.1f, bin %.1f); the janitor swept %s ago, removed %d item(s), freed %.1f GB",
		gb(b.Total()), gb(janitor.Sum(b.Worktrees)), gb(janitor.Sum(b.Scratch)), gb(janitor.Sum(b.Caches)), gb(b.State), gb(b.Data), gb(b.Bin), now.Sub(record.Time).Round(time.Minute), len(record.Removed), gb(record.Freed()))
	if len(record.Kept) > 0 {
		line += fmt.Sprintf(", kept %d it must not touch", len(record.Kept))
	}
	if len(record.Strays) > 0 {
		line += fmt.Sprintf(", reports %d stray item(s) (cfo runtime names them)", len(record.Strays))
	}
	if ended, left := len(record.Processes.Ended), len(record.Processes.Left); ended+left > 0 {
		line += fmt.Sprintf(", ended %d leftover process(es) and left %d it could not prove the fleet's own (cfo runtime names them)", ended, left)
	}
	ew.println(line)
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
	if _, err := os.Lstat(filepath.Join(dataDir, "memory", "startup.json")); !errors.Is(err, os.ErrNotExist) {
		ew.printf("The CFO's memory is %s, the same for Claude Code, Codex and Pi. The verified startup.json selects exact warm source ranges; cold facts and original rulings remain at their owners. Use cfo memory retrieve for the task's project, task and topic before dependent action. Read the Stow skill for capture, corrections, provenance and per-item pinned-offload authority; no digest grants that authority.\n", memory)
	} else {
		ew.printf("The CFO's memory is %s, the same for Claude Code, Codex and Pi: MEMORY.md there is its index, %s, one line per fact. Read a fact's own file when its line bears on the work. When you learn something durable, write it there as one file (frontmatter name, description and type, then the fact) and add its one line to MEMORY.md, never only to a harness's own memory folder, which the next CFO may not see.\n", memory, indexPrinted)
	}
	ew.println("For an explicit user decision, the registered primary CFO uses cfo question --id <stable-id> --text <question> --option <choice> --recommend <exact-choice>. Repeat --option for real choices; omit --recommend unless you recommend one. Other always permits a written answer.")
	ew.println("This contract applies to Claude Code, Codex and Pi: publish from this primary session's shell, then continue independent work or end the turn awaiting the answer. The durable answer returns to the same CFO as a normal message; it does NOT answer a pending native prompt tool. Do not open a native prompt for the same decision or promote worker wake diagnostics into user questions.")
	ew.println("For a nonblocking presentation, use a Scrawl page (lavish-axi <file> --no-open) and cfo present --id <stable-id> --kind review --url <returned-safe-url> from this registered primary; workers add --task and --generation. Report only a successful tool result, refresh only while live and finish with --state ended. Do not wait for a viewing choice or claim URL opening mirrors browser control; see docs/native-board.md.")
	ew.println("The Stop-owned auto-arm hook owns watcher continuity: it hosts the watcher in-process across every turn for up to 8 hours.")
	ew.println("Never run \"cfo watch\" from the agent shell. The watcher is armed and supervised by that hook alone; running it manually bypasses supervision.")
	ew.println("Wakes arrive as rewake turns: a Stop hook exit 2 reopens the turn with an operational reason (a signal, a stale sweep, or a heartbeat), not a fresh session.")
	ew.println("Every drain presentation ends with a WAKE_ACK_REQUIRED command. Run it after handling what cfo drain printed, or the same records resurface on the next drain.")
	ew.println("That command is REFUSED when unanswered questions sit at or below its sequence: drain lists every waiting goblin and retires nothing. The refusal is the protection working, not an error - a record acked unread is a record nobody will ever read.")
	ew.println("Answer each listed goblin with \"cfo send <id> \"...\"\", then re-run the same command with --ack-blocking. That flag is range-scoped, not per-record: it retires EVERY question at or below the sequence, and the refusal listing is the whole set it will retire. The ack floor only moves forward, so a later question cannot be retired while an earlier one is kept - to hold one open, handle it first or ack a range that stops below its sequence.")
	ew.println("Supervision is needed whenever tasks are in flight or queued work could start: any state\\*.meta file with no terminal status, or a queued task a Start could start, keeps the Stop hooks watching for a live watcher, and a turn that ends with no goblin at work while work waits and memory is free is reopened with the next work named. Your job is the whole list, not only the goblins in flight.")
	ew.println(orphans + " is the fleet nothing else reports: a harness process with no pane is unsupervised and may still be spending tokens. Run \"cfo reap\" to re-sweep and \"cfo reap --apply\" to retire everything it found except a process: ending one is authorised only by naming its pid, as \"cfo reap --force <pid> --apply\". A HELD line says why that one was left alone.")
}

// writeReadOnceContract names every source this digest already printed, so
// the agent does not spend a turn re-reading what it was just handed. The
// files it names as printed in full are the ones the sections below actually
// printed (printed), never a fixed list: a file that was absent or unreadable
// is not one the agent has read. State projections never claim full files.
func writeReadOnceContract(printed []string, ew *werr) {
	ew.println("== READ-ONCE CONTRACT ==")
	writePrintedInFull(printed, ew)
	ew.println("State is bounded: the first 5 queued task records, capped task rows and each shown task's last status event, not full metadata, backlog or logs.")
	ew.println("The last orphan sweep is capped; cfo reap --json reads the full finding set.")
	ew.println("Do not re-read a file named on the PRINTED IN FULL line this turn. A backlog or status section that hit its cap only needs a fresh read for what is past the cap, not for what is already shown.")
	ew.println("That line holds only for a reader that reached NEXT STEP at the end of this digest: one handed a part of it, by a tool that cut it short or a read that stopped early, has read less.")
}

// writeNextStep prints the fixed two-line closing reminder.
func writeNextStep(ew *werr) {
	ew.println("== NEXT STEP ==")
	ew.println("Follow the SUPERVISION OPERATING INSTRUCTIONS above for how to proceed from here.")
	ew.println("This digest never arms anything itself: the Stop-owned auto-arm hook is the only thing that starts the watcher, on the next Stop.")
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
	var section bytes.Buffer
	if err := reap.RenderRecord(&section, stateDir, time.Now()); err != nil {
		ew.printf("ORPHANS: UNREADABLE (%s)\n", cutError(err, briefErrorWidth))
		return
	}
	text := section.String()
	if len(text) > 1000 {
		text = strings.ToValidUTF8(text[:1000], "") + "\n(+more findings: cfo reap --json)\n"
	}
	ew.printf("%s", text)
}

// writeContext prints data\projects.md, data\overlord.md, and the memory
// index data\memory\MEMORY.md, each in full, or as "<name>: ABSENT" /
// "<name>: (present, empty)" / "<name>: UNREADABLE (<err>)". A fact's own
// file is never printed: the index is what every session pays for. It
// returns the files it printed in full, as the read-once contract names them.
func writeContext(dataDir string, ew *werr) []string {
	ew.println("== CONTEXT ==")
	if _, err := os.Lstat(filepath.Join(dataDir, filepath.FromSlash(memory.CatalogPath))); !errors.Is(err, os.ErrNotExist) {
		catalog, err := memory.Load(dataDir, time.Now())
		if err != nil {
			ew.printf("MEMORY UNREADABLE: %s. Do not act without resolving source authority; no legacy fallback was selected.\n", err)
			return nil
		}
		text, err := catalog.Retrieve(memory.Query{}, time.Now())
		if err != nil {
			ew.printf("MEMORY UNREADABLE: %s\n", err)
			return nil
		}
		ew.println("Verified warm operating map; quoted source ranges follow. No source file is claimed read in full.")
		ew.printf("%s", text)
		ew.println("Before work, retrieve relevant cold authority and project facts with cfo memory retrieve --project <name> --task <id> --topic <topic>; retrieve a named item with --id <id>.")
		routes := catalog.Routes(2000)
		ew.printf("%s", routes)
		budget, err := memory.ReadBudget(filepath.Join(filepath.Dir(dataDir), "config", "startup-memory-budget"), memory.DefaultBudget)
		if err != nil {
			ew.printf("MEMORY BUDGET UNREADABLE: %s; Stow cannot declare reset-safe.\n", err)
		} else {
			memoryBytes := len(text) + len(routes)
			ew.printf("Warm memory and routes: %d bytes, %d estimated tokens (bytes/4 rounded up), budget %d estimated tokens.\n", memoryBytes, memory.Tokens(memoryBytes), budget)
			if memory.Tokens(memoryBytes) > budget {
				ew.println("MEMORY OVER BUDGET: retained all authority. Open a concrete budget or named-offload decision; an exception is not relief or reset-safe.")
			}
		}
		return nil
	}
	var printed []string
	for _, name := range []string{"projects.md", "overlord.md", filepath.FromSlash(layout.MemoryIndex)} {
		if writeContextFile(dataDir, name, ew) {
			printed = append(printed, `data\`+name)
		}
	}
	if report, err := memory.MemoryReport(dataDir, time.Now()); err != nil {
		ew.printf("MEMORY BUDGET UNREADABLE: %s; Stow cannot declare reset-safe.\n", err)
	} else {
		ew.printf("LEGACY MEMORY UNCURATED: %d source bytes, %d estimated tokens, budget %d. No authority was offloaded; cfo memory migration-plan inventories named proposals.\n", report.Bytes, report.EstimatedTokens, report.Budget)
		if report.IsOverBudget {
			ew.println("MEMORY OVER BUDGET: preserve authority and open a concrete budget or per-item offload decision; not reset-safe.")
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
