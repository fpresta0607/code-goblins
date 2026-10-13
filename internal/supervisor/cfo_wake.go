package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A Claude Code CFO is woken by its own Stop hook, which waits on the wake
// queue. A Codex or pi CFO has no such hook, so the supervisor wakes it the one
// way every harness takes input: it types one short wake line into the CFO's
// native terminal, only while its harness sits idle at an empty composer, so
// never mid-turn and never over text somebody left unsent, and proves it taken
// the way cfo send does.
const (
	// cfoWakeEvery is how often the supervisor looks for queued wakes the
	// CFO has not been told about.
	cfoWakeEvery = 5 * time.Second
	// cfoWakeGap is the least time between two wake lines typed into the CFO.
	cfoWakeGap = 30 * time.Second
	// cfoIdleRecheck is how far apart the two readings that find the CFO
	// idle at an empty composer are taken, so a harness caught between two
	// steps of one turn is not taken for idle.
	cfoIdleRecheck = time.Second
)

// cfoWakeStuckAfter is how long a queued wake may wait untyped before the
// Overlord is told that the CFO cannot be.
const cfoWakeStuckAfter = 5 * time.Minute

// cfoWakeSkipsFile records each queued wake the supervisor did not type, why
// and what the CFO's screen showed, so a CFO that was never told can be
// explained afterwards: from 01:17Z to 01:33Z on 2026-10-13 six wakes waited
// for an idle Codex CFO, and nothing said why. maxCFOWakeSkips bounds it.
const (
	cfoWakeSkipsFile = "cfo-wake-skips.jsonl"
	maxCFOWakeSkips  = 200
	// cfoWakeSkipRows is how many of the screen's last rows a skip keeps.
	cfoWakeSkipRows = 8
)

// cfoWakeSkip is one record of cfoWakeSkipsFile: when, the wakes left
// untyped, the newest of them, why in plain words, and the screen's last rows.
type cfoWakeSkip struct {
	At     time.Time `json:"at"`
	Wakes  int       `json:"wakes"`
	Newest int       `json:"newest"`
	Why    string    `json:"why"`
	Screen string    `json:"screen,omitempty"`
}

// backgroundWork is a running row that is no turn: a command the CFO left
// running, or the count of its background shells, at a prompt it waits at.
var backgroundWork = regexp.MustCompile(`^(?:[^(]*Running…|.*(^|·)\s*\d+ shells?\s*(·|$))`)

// cfoWakeSteady is how long one reason a CFO not in a turn was not told must
// hold, on every look, before the Overlord is: a CFO between two steps of a
// turn can show a frame that reads otherwise.
const cfoWakeSteady = time.Minute

// whyInTurn is why a wake is not typed into a CFO in the middle of a turn,
// which takes its wakes once the turn ends: it is never the Overlord's to
// restart for that, since a restart would end a CFO at work.
const whyInTurn = "its screen shows it in the middle of a turn"

// whyEnded and whyGone are why a wake is not typed into a CFO that no longer
// runs, or whose terminal now runs another program.
const (
	whyEnded = "the CFO that registered no longer runs"
	whyGone  = "its terminal no longer runs the CFO that registered"
)

// cfoWokenFile holds the highest wake sequence a typed wake line covered:
// the line sends the CFO to cfo drain, which shows all of them, so a record a
// line covered is never typed about again, answered or not.
const cfoWokenFile = ".cfo-wake-typed"

// CFOWake is how a queued wake reaches a CFO.
type CFOWake string

const (
	// CFOWakeNone: nothing delivers a wake to a CFO running this harness.
	CFOWakeNone CFOWake = ""
	// CFOWakeStopHook: the harness's own Stop hook waits on the wake queue
	// and reopens the turn, as Claude Code's does.
	CFOWakeStopHook CFOWake = "stop-hook"
	// CFOWakeTyped: the supervisor types one wake line into the CFO's
	// native terminal while it sits idle at an empty composer, as it does
	// for Codex and pi. It needs the CFO in a native terminal, which the
	// supervisor can read and type into; one in any other terminal is not
	// woken.
	CFOWakeTyped CFOWake = "typed-line"
)

// CFOWakeFor says how a CFO running agent, a harness by its id, is woken.
// It is the one place that knows, for the typed delivery here and for
// whatever tells a user which harnesses a CFO can run in.
func CFOWakeFor(agent string) CFOWake {
	switch harness.Kind(agent) {
	case harness.Claude:
		return CFOWakeStopHook
	case harness.Codex, harness.Pi:
		return CFOWakeTyped
	}
	return CFOWakeNone
}

// keepCFOAwake looks for wakes to type into the CFO every cfoWakeEvery until
// ctx ends, handing what went wrong to the next recovery cycle once however
// many looks met it.
func (s *Service) keepCFOAwake(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := s.wakeCFO(ctx, time.Now().UTC()); err != nil {
			s.mu.Lock()
			if s.cfoWakeErr == nil || !strings.Contains(s.cfoWakeErr.Error(), err.Error()) {
				s.cfoWakeErr = errors.Join(s.cfoWakeErr, err)
			}
			s.mu.Unlock()
		}
	}
}

// wakeCFO types one wake line into a registered Codex or pi CFO's native
// terminal when the queue holds records no line has covered, the CFO's
// harness sits idle at an empty composer, and no line was typed within
// cfoWakeGap. The records a line covers are marked before it is typed, so
// they stay covered when nothing proves it taken or the supervisor dies while
// it waits for the proof, because typing it twice is worse than asking the
// CFO to look: cfo send never types a line twice either.
func (s *Service) wakeCFO(ctx context.Context, now time.Time) error {
	connection := s.Options.CFO
	if connection == nil {
		return nil
	}
	stateDir := connection.State
	records, err := wake.Pending(stateDir)
	if err != nil {
		return err
	}
	covered, typedAt, err := readCFOWoken(stateDir)
	if err != nil {
		return err
	}
	var fresh []wake.Record
	for _, record := range records {
		if record.Seq > covered {
			fresh = append(fresh, record)
		}
	}
	if len(fresh) == 0 {
		return s.untoldWakes(nil, cfoWakeSkip{}, now)
	}
	if now.Sub(typedAt) < cfoWakeGap {
		return nil
	}
	primary, _, err := readPrimary(stateDir)
	if err != nil {
		return nil
	}
	// A CFO its Stop hook wakes, or one in Herdr, takes its wakes itself.
	if primary.Host == "" || CFOWakeFor(primary.Agent) != CFOWakeTyped {
		connection.steadySince, connection.steadyWhy = time.Time{}, ""
		return s.Store.withdrawCFOUnwoken("The CFO now takes its wakes itself.")
	}
	if !primary.Process.VerifiedAlive() {
		return s.untoldWakes(fresh, cfoWakeSkip{Why: whyEnded}, now)
	}
	line := wakeLine(fresh)
	// While AFK mode is on the line says so, since a CFO with no Stop hook
	// is told nothing else at a wake.
	if banner := afk.BannerFor(stateDir); banner != "" {
		line += " " + banner
	}
	skip, err := connection.typeWake(ctx, primary, line, fresh[len(fresh)-1].Seq, now)
	switch {
	case skip.Why != "":
		return errors.Join(err, s.untoldWakes(fresh, skip, now))
	case err != nil:
		return err
	}
	return s.untoldWakes(nil, skip, now)
}

// untoldWakes records fresh, the wakes left untyped, as skip says, once for
// each new wake and each new reason, and keeps the Overlord's item about
// them: made once the oldest has waited cfoWakeStuckAfter, and taken back
// once none waits untyped. With no wake left untyped, typed or drained, it
// only takes it back.
func (s *Service) untoldWakes(fresh []wake.Record, skip cfoWakeSkip, now time.Time) error {
	if len(fresh) == 0 {
		// The next wake left untyped starts its own minute.
		s.Options.CFO.steadySince, s.Options.CFO.steadyWhy = time.Time{}, ""
		return s.Store.withdrawCFOUnwoken("The CFO was told of the fleet's updates.")
	}
	skip.At, skip.Wakes, skip.Newest = now.UTC(), len(fresh), fresh[len(fresh)-1].Seq
	connection := s.Options.CFO
	var recordErr error
	if key := strconv.Itoa(skip.Newest) + "\x00" + skip.Why; key != connection.skipped {
		if recordErr = recordCFOWakeSkip(connection.State, skip); recordErr == nil {
			connection.skipped = key
		}
	}
	// A CFO at work takes its wakes as the turn ends, and one that no longer
	// runs has no Restart to offer: neither is the Overlord's to restart, and
	// an item made earlier is taken back.
	switch skip.Why {
	case whyInTurn, whyGone, whyEnded:
		connection.steadySince, connection.steadyWhy = time.Time{}, ""
		return errors.Join(recordErr, s.Store.withdrawCFOUnwoken("The CFO is not one to restart now."))
	}
	if skip.Why != connection.steadyWhy {
		connection.steadySince, connection.steadyWhy = now, skip.Why
	}
	waited := now.Sub(fresh[0].Time)
	if waited < cfoWakeStuckAfter || now.Sub(connection.steadySince) < cfoWakeSteady {
		return recordErr
	}
	return errors.Join(recordErr, s.offerCFORestart(fresh[0].Seq, len(fresh), waited, skip.Why, now))
}

// recordCFOWakeSkip adds skip to cfoWakeSkipsFile, keeping the newest
// maxCFOWakeSkips.
func recordCFOWakeSkip(stateDir string, skip cfoWakeSkip) error {
	data, err := json.Marshal(skip)
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, cfoWakeSkipsFile)
	lines, err := fsx.ReadLines(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines = append(lines, string(data))
	lines = lines[max(0, len(lines)-maxCFOWakeSkips):]
	return fsx.AtomicWriteFile(path, []byte(strings.Join(lines, "\n")+"\n"))
}

// offerCFORestart puts before the Overlord, once for the oldest untyped wake
// oldest, a Command Center item in plain words whose one action is the
// board's own Restart the CFO, which the supervisor does itself: the CFO
// cannot be told why its wakes wait, since that would be one more wake. The
// item is never run as a command, since a CFO a run terminal started is not
// proven to outlive that terminal.
func (s *Service) offerCFORestart(oldest, wakes int, waited time.Duration, why string, now time.Time) error {
	if s.Store.offeredCFORestart(oldest, time.Now()) {
		return nil
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	id := "cfo-unwoken-" + hex.EncodeToString(nonce[:])
	identity := sha256.Sum256([]byte("cfo-unwoken\n" + id + "\n" + strconv.FormatInt(now.UnixNano(), 10)))
	updates := "fleet updates"
	if wakes == 1 {
		updates = "fleet update"
	}
	title := fmt.Sprintf("The CFO has not been told of %d %s for %d minutes, because %s. Restart the CFO so it reads them?", wakes, updates, int(waited/time.Minute), why)
	h := s.Store.Home
	command := "# This item restarts the CFO with the board's Restart the CFO, never as a command.\n"
	// The item is made when it is, whichever moment the look was for.
	made := time.Now().UTC()
	_, err := s.recordBoardRun(Run{ID: id, Identity: hex.EncodeToString(identity[:]), Title: title, Shell: "powershell", Command: command, Cwd: h.Root, State: "ready", CreatedAt: made, ExpiresAt: made.Add(runLifetime), CFOUnwoken: oldest})
	return err
}

// wakeLine is the one line typed into the CFO: how many wakes wait, whose,
// and what to do.
func wakeLine(records []wake.Record) string {
	var named []string
	for _, record := range records[:min(len(records), 3)] {
		label := record.Kind
		if record.Key != record.Kind {
			label += " " + record.Key
		}
		named = append(named, label)
	}
	if len(records) > 3 {
		named = append(named, fmt.Sprintf("%d more", len(records)-3))
	}
	noun := "wake"
	if len(records) > 1 {
		noun = "wakes"
	}
	return bounded(fmt.Sprintf("cfo watcher wake: %d queued %s (%s); run cfo drain, handle what it shows and ack it with the WAKE_ACK_REQUIRED command it prints", len(records), noun, strings.Join(named, ", ")), 300)
}

// typeWake types text into the registered CFO's native terminal when its
// harness sits idle at an empty composer on two readings cfoIdleRecheck
// apart, marking the records up to covered as typed about at now first, and
// delivers it with the proof cfo send uses: its native hooks report the
// prompt taken, or its screen shows it working when it was not.
func (c *CFOConnection) typeWake(ctx context.Context, primary primaryRegistration, text string, covered int, now time.Time) (cfoWakeSkip, error) {
	screens, readable := harness.NativeScreens(harness.Kind(primary.Agent))
	if !readable {
		return cfoWakeSkip{Why: "its screen is one this supervisor cannot read"}, nil
	}
	gone := cfoWakeSkip{Why: whyGone}
	if err := c.verify(primary); err != nil {
		return gone, err
	}
	record, err := host.ReadRecord(c.State, primary.Host)
	if err != nil {
		return gone, err
	}
	read := c.ReadScreen
	if read == nil {
		read = host.ReadScreen
	}
	c.typing.Lock()
	defer c.typing.Unlock()
	for reading := range 2 {
		if reading > 0 {
			select {
			case <-time.After(cfoIdleRecheck):
			case <-ctx.Done():
				return cfoWakeSkip{}, ctx.Err()
			}
		}
		// A screen that cannot be read, or shows the CFO in a turn, at a
		// dialog or with text in its composer, is looked at again on the next
		// tick.
		screen, err := read(record)
		if err != nil {
			return cfoWakeSkip{Why: "its screen could not be read"}, nil
		}
		if why := notIdle(screens, screen); why != "" {
			return cfoWakeSkip{Why: why, Screen: host.ScreenTail(screen, cfoWakeSkipRows)}, nil
		}
	}
	if err := writeCFOWoken(c.State, covered, now); err != nil {
		return cfoWakeSkip{}, err
	}
	deliver := c.Deliver
	if deliver == nil {
		deliver = c.deliverNative
	}
	if err := deliver(ctx, state.TaskMeta{ID: primary.Host, Harness: primary.Agent}, text); err != nil {
		return cfoWakeSkip{}, fmt.Errorf("a wake line was typed into the CFO's native terminal %s, and nothing proves it taken: %w", primary.Host, err)
	}
	return cfoWakeSkip{}, nil
}

// notIdle says in plain words why screen does not show a harness waiting at
// an empty composer, or "" when it does.
func notIdle(screens harness.Screens, screen []string) string {
	// A turn is read first: a CFO at work may show a dialog's words in what
	// it read, and a status row whose heading the harness's own pattern
	// does not know still ends with the interrupt hint.
	isRunning, isInTurn := false, screens.IsWorking(screen)
	for _, row := range screen {
		if found, isWork := harness.RunningWork([]string{row}); isWork {
			isRunning = true
			isInTurn = isInTurn || strings.Contains(found, "esc to interrupt") || !backgroundWork.MatchString(found)
		}
	}
	if isInTurn {
		return whyInTurn
	}
	if dialog, isShown := screens.Dialog(screen); isShown {
		return "its screen shows " + dialog.Name
	}
	// The row itself is redrawn as its clock ticks, so it stays in the
	// screen kept beside the reason, never in the reason.
	if isRunning {
		return "its screen shows work it started still running"
	}
	if !screens.IsReady(screen) || !screens.ComposerEmpty(screen) {
		return "its screen does not show it waiting at an empty prompt"
	}
	return ""
}

// deliverNative submits text in terminal's native terminal as cfo send does
// for a native goblin: taken once the harness's own prompt hook reports it or
// its screen shows it working.
func (c *CFOConnection) deliverNative(ctx context.Context, terminal state.TaskMeta, text string) error {
	sender := spawn.Service{StateDir: c.State, PromptSince: func(hostID, _ string, since time.Time) (bool, error) {
		return NativeHostPromptSince(c.State, hostID, since)
	}}
	return sender.SendNative(ctx, terminal, text)
}

// readCFOWoken reads the highest sequence a typed wake line covered and when
// it was typed; none has been when the file is missing.
func readCFOWoken(stateDir string) (int, time.Time, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, cfoWokenFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, time.Time{}, nil
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	seq, at, _ := strings.Cut(strings.TrimSpace(string(data)), " ")
	covered, err := strconv.Atoi(seq)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("cfo wake: unreadable %s: %w", cfoWokenFile, err)
	}
	typedAt, _ := time.Parse(time.RFC3339Nano, at)
	return covered, typedAt, nil
}

func writeCFOWoken(stateDir string, seq int, at time.Time) error {
	return fsx.AtomicWriteFile(filepath.Join(stateDir, cfoWokenFile), []byte(strconv.Itoa(seq)+" "+at.UTC().Format(time.RFC3339Nano)+"\n"))
}
