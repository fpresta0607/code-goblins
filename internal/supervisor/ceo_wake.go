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
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A Claude Code CEO is woken by its own Stop hook, which waits on the wake
// queue. A Codex or pi CEO has no such hook, so the supervisor wakes it the one
// way every harness takes input: it types one short wake line into the CEO's
// native terminal, only while its harness sits idle at an empty composer, so
// never mid-turn and never over text somebody left unsent, and proves it taken
// the way goblins send does.
const (
	// ceoWakeEvery is how often the supervisor looks for queued wakes the
	// CEO has not been told about.
	ceoWakeEvery = 5 * time.Second
	// ceoWakeGap is the least time between two wake lines typed into the CEO.
	ceoWakeGap = 30 * time.Second
	// ceoIdleRecheck is how far apart the two readings that find the CEO
	// idle at an empty composer are taken, so a harness caught between two
	// steps of one turn is not taken for idle.
	ceoIdleRecheck = time.Second
)

// ceoWakeStuckAfter is how long a queued wake may wait untyped before the
// Overlord is told that the CEO cannot be.
const ceoWakeStuckAfter = 5 * time.Minute

// ceoWakeSkipsFile records each queued wake the supervisor did not type, why
// and what the CEO's screen showed, so a CEO that was never told can be
// explained afterwards: from 01:17Z to 01:33Z on 2026-10-13 six wakes waited
// for an idle Codex CEO, and nothing said why. maxCEOWakeSkips bounds it.
const (
	ceoWakeSkipsFile = "ceo-wake-skips.jsonl"
	maxCEOWakeSkips  = 200
	// ceoWakeSkipRows is how many of the screen's last rows a skip keeps.
	ceoWakeSkipRows = 8
)

// ceoWakeSkip is one record of ceoWakeSkipsFile: when, the wakes left
// untyped, the newest of them, why in plain words, and the screen's last rows.
type ceoWakeSkip struct {
	At     time.Time `json:"at"`
	Wakes  int       `json:"wakes"`
	Newest int       `json:"newest"`
	Why    string    `json:"why"`
	Screen string    `json:"screen,omitempty"`
}

// backgroundWork is a running row that is no turn: a command the CEO left
// running, or the count of its background shells, at a prompt it waits at.
var backgroundWork = regexp.MustCompile(`^(?:[^(]*Running…|.*(^|·)\s*\d+ shells?\s*(·|$))`)

// ceoWakeSteady is how long one reason a CEO not in a turn was not told must
// hold, on every look, before the Overlord is: a CEO between two steps of a
// turn can show a frame that reads otherwise.
const ceoWakeSteady = time.Minute

// whyInTurn is why a wake is not typed into a CEO in the middle of a turn,
// which takes its wakes once the turn ends: it is never the Overlord's to
// restart for that, since a restart would end a CEO at work.
const whyInTurn = "its screen shows it in the middle of a turn"

// whyEnded and whyGone are why a wake is not typed into a CEO that no longer
// runs, or whose terminal now runs another program.
const (
	whyEnded = "the CEO that registered no longer runs"
	whyGone  = "its terminal no longer runs the CEO that registered"
)

// ceoWokenFile holds the highest wake sequence a typed wake line covered:
// the line sends the CEO to goblins drain, which shows all of them, so a record a
// line covered is never typed about again, answered or not.
const ceoWokenFile = ".ceo-wake-typed"

// CEOWake is how a queued wake reaches a CEO.
type CEOWake string

const (
	// CEOWakeNone: nothing delivers a wake to a CEO running this harness.
	CEOWakeNone CEOWake = ""
	// CEOWakeStopHook: the harness's own Stop hook waits on the wake queue
	// and reopens the turn, as Claude Code's does.
	CEOWakeStopHook CEOWake = "stop-hook"
	// CEOWakeTyped: the supervisor types one wake line into the CEO's
	// native terminal while it sits idle at an empty composer, as it does
	// for Codex and pi. It needs the CEO in a native terminal, which the
	// supervisor can read and type into; one in any other terminal is not
	// woken.
	CEOWakeTyped CEOWake = "typed-line"
)

// CEOWakeFor says how a CEO running agent, a harness by its id, is woken.
// It is the one place that knows, for the typed delivery here and for
// whatever tells a user which harnesses a CEO can run in.
func CEOWakeFor(agent string) CEOWake {
	switch harness.Kind(agent) {
	case harness.Claude:
		return CEOWakeStopHook
	case harness.Codex, harness.Pi:
		return CEOWakeTyped
	}
	return CEOWakeNone
}

// keepCEOAwake looks for wakes to type into the CEO every ceoWakeEvery until
// ctx ends, handing what went wrong to the next recovery cycle once however
// many looks met it.
func (s *Service) keepCEOAwake(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := s.wakeCEO(ctx, time.Now().UTC()); err != nil {
			s.mu.Lock()
			if s.ceoWakeErr == nil || !strings.Contains(s.ceoWakeErr.Error(), err.Error()) {
				s.ceoWakeErr = errors.Join(s.ceoWakeErr, err)
			}
			s.mu.Unlock()
		}
	}
}

// wakeCEO types one wake line into a registered Codex or pi CEO's native
// terminal when the queue holds records no line has covered, the CEO's
// harness sits idle at an empty composer, and no line was typed within
// ceoWakeGap. The records a line covers are marked before it is typed, so
// they stay covered when nothing proves it taken or the supervisor dies while
// it waits for the proof, because typing it twice is worse than asking the
// CEO to look: goblins send never types a line twice either.
func (s *Service) wakeCEO(ctx context.Context, now time.Time) error {
	connection := s.Options.CEO
	if connection == nil {
		return nil
	}
	stateDir := connection.State
	records, err := wake.Pending(stateDir)
	if err != nil {
		return err
	}
	covered, typedAt, err := readCEOWoken(stateDir)
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
		return s.untoldWakes(nil, ceoWakeSkip{}, now)
	}
	if now.Sub(typedAt) < ceoWakeGap {
		return nil
	}
	primary, _, err := readPrimary(stateDir)
	if err != nil {
		return nil
	}
	// A CEO its Stop hook wakes, or one in Herdr, takes its wakes itself.
	if primary.Host == "" || CEOWakeFor(primary.Agent) != CEOWakeTyped {
		connection.steadySince, connection.steadyWhy = time.Time{}, ""
		return s.Store.withdrawCEOUnwoken("The CEO now takes its wakes itself.")
	}
	if !primary.Process.VerifiedAlive() {
		return s.untoldWakes(fresh, ceoWakeSkip{Why: whyEnded}, now)
	}
	line := wakeLine(fresh)
	// While AFK mode is on the line says so, since a CEO with no Stop hook
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
// them: made once the oldest has waited ceoWakeStuckAfter, and taken back
// once none waits untyped. With no wake left untyped, typed or drained, it
// only takes it back.
func (s *Service) untoldWakes(fresh []wake.Record, skip ceoWakeSkip, now time.Time) error {
	if len(fresh) == 0 {
		// The next wake left untyped starts its own minute.
		s.Options.CEO.steadySince, s.Options.CEO.steadyWhy = time.Time{}, ""
		return s.Store.withdrawCEOUnwoken("The CEO was told of the fleet's updates.")
	}
	skip.At, skip.Wakes, skip.Newest = now.UTC(), len(fresh), fresh[len(fresh)-1].Seq
	connection := s.Options.CEO
	var recordErr error
	if key := strconv.Itoa(skip.Newest) + "\x00" + skip.Why; key != connection.skipped {
		if recordErr = recordCEOWakeSkip(connection.State, skip); recordErr == nil {
			connection.skipped = key
		}
	}
	// A CEO at work takes its wakes as the turn ends, and one that no longer
	// runs has no Restart to offer: neither is the Overlord's to restart, and
	// an item made earlier is taken back.
	switch skip.Why {
	case whyInTurn, whyGone, whyEnded:
		connection.steadySince, connection.steadyWhy = time.Time{}, ""
		return errors.Join(recordErr, s.Store.withdrawCEOUnwoken("The CEO is not one to restart now."))
	}
	if skip.Why != connection.steadyWhy {
		connection.steadySince, connection.steadyWhy = now, skip.Why
	}
	waited := now.Sub(fresh[0].Time)
	if waited < ceoWakeStuckAfter || now.Sub(connection.steadySince) < ceoWakeSteady {
		return recordErr
	}
	return errors.Join(recordErr, s.offerCEORestart(fresh[0].Seq, len(fresh), waited, skip.Why, now))
}

// recordCEOWakeSkip adds skip to ceoWakeSkipsFile, keeping the newest
// maxCEOWakeSkips.
func recordCEOWakeSkip(stateDir string, skip ceoWakeSkip) error {
	data, err := json.Marshal(skip)
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, ceoWakeSkipsFile)
	lines, err := fsx.ReadLines(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines = append(lines, string(data))
	lines = lines[max(0, len(lines)-maxCEOWakeSkips):]
	return fsx.AtomicWriteFile(path, []byte(strings.Join(lines, "\n")+"\n"))
}

// offerCEORestart puts before the Overlord, once for the oldest untyped wake
// oldest, a Command Center item in plain words whose one action is the
// board's own Restart the CEO, which the supervisor does itself: the CEO
// cannot be told why its wakes wait, since that would be one more wake. The
// item is never run as a command, since a CEO a run terminal started is not
// proven to outlive that terminal.
func (s *Service) offerCEORestart(oldest, wakes int, waited time.Duration, why string, now time.Time) error {
	if s.Store.offeredCEORestart(oldest, time.Now()) {
		return nil
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	id := "ceo-unwoken-" + hex.EncodeToString(nonce[:])
	identity := sha256.Sum256([]byte("ceo-unwoken\n" + id + "\n" + strconv.FormatInt(now.UnixNano(), 10)))
	updates := "fleet updates"
	if wakes == 1 {
		updates = "fleet update"
	}
	title := fmt.Sprintf("The CEO has not been told of %d %s for %d minutes, because %s. Restart the CEO so it reads them?", wakes, updates, int(waited/time.Minute), why)
	h := s.Store.Home
	command := "# This item restarts the CEO with the board's Restart the CEO, never as a command.\n"
	// The item is made when it is, whichever moment the look was for.
	made := time.Now().UTC()
	_, err := s.recordBoardRun(Run{ID: id, Identity: hex.EncodeToString(identity[:]), Title: title, Shell: "powershell", Command: command, Cwd: h.Root, State: "ready", CreatedAt: made, ExpiresAt: made.Add(runLifetime), CEOUnwoken: oldest})
	return err
}

// wakeLine is the one line typed into the CEO: how many wakes wait, whose,
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
	return bounded(fmt.Sprintf(fleettree.WakePrefix+"%d queued %s (%s); run goblins drain, handle what it shows and ack it with the WAKE_ACK_REQUIRED command it prints", len(records), noun, strings.Join(named, ", ")), 300)
}

// typeWake types text into the registered CEO's native terminal when its
// harness sits idle at an empty composer on two readings ceoIdleRecheck
// apart, marking the records up to covered as typed about at now first, and
// delivers it with the proof goblins send uses: its native hooks report the
// prompt taken, or its screen shows it working when it was not. A delivery
// that typed nothing leaves the records as they were.
func (c *CEOConnection) typeWake(ctx context.Context, primary primaryRegistration, text string, covered int, now time.Time) (ceoWakeSkip, error) {
	screens, readable := harness.NativeScreens(harness.Kind(primary.Agent))
	if !readable {
		return ceoWakeSkip{Why: "its screen is one this supervisor cannot read"}, nil
	}
	gone := ceoWakeSkip{Why: whyGone}
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
			case <-time.After(ceoIdleRecheck):
			case <-ctx.Done():
				return ceoWakeSkip{}, ctx.Err()
			}
		}
		// A screen that cannot be read, or shows the CEO in a turn, at a
		// dialog or with text in its composer, is looked at again on the next
		// tick.
		screen, err := read(record)
		if err != nil {
			return ceoWakeSkip{Why: "its screen could not be read"}, nil
		}
		if why := notIdle(screens, screen); why != "" {
			return ceoWakeSkip{Why: why, Screen: host.ScreenTail(screen, ceoWakeSkipRows)}, nil
		}
	}
	previous, previousAt, err := readCEOWoken(c.State)
	if err != nil {
		return ceoWakeSkip{}, err
	}
	if err := writeCEOWoken(c.State, covered, now); err != nil {
		return ceoWakeSkip{}, err
	}
	deliver := c.Deliver
	if deliver == nil {
		deliver = c.deliverNative
	}
	err = deliver(ctx, state.TaskMeta{ID: primary.Host, Harness: primary.Agent}, text)
	if errors.Is(err, fleet.ErrNotTyped) {
		// Nothing reached the terminal, so the records stay for the next look.
		if err := writeCEOWoken(c.State, previous, previousAt); err != nil {
			return ceoWakeSkip{}, err
		}
		if errors.Is(err, spawn.ErrInTurn) {
			return ceoWakeSkip{Why: whyInTurn}, nil
		}
		return ceoWakeSkip{}, fmt.Errorf("no wake line reached the CEO's native terminal %s: %w", primary.Host, err)
	}
	if err != nil {
		return ceoWakeSkip{}, fmt.Errorf("a wake line was typed into the CEO's native terminal %s, and nothing proves it taken: %w", primary.Host, err)
	}
	return ceoWakeSkip{}, nil
}

// notIdle says in plain words why screen does not show a harness waiting at
// an empty composer, or "" when it does.
func notIdle(screens harness.Screens, screen []string) string {
	// A turn is read first: a CEO at work may show a dialog's words in what
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

// deliverNative submits text in terminal's native terminal as goblins send does
// for a native goblin: taken once the harness's own prompt hook reports it or
// its screen shows it working. A turn that started after typeWake found the
// CEO idle, shown on the screen it reads just before typing, gets nothing.
func (c *CEOConnection) deliverNative(ctx context.Context, terminal state.TaskMeta, text string) error {
	sender := spawn.Service{StateDir: c.State, ReadScreen: c.ReadScreen, ShouldRefuseTurn: true, PromptSince: func(hostID, _ string, since time.Time) (bool, error) {
		return NativeHostPromptSince(c.State, hostID, since)
	}}
	return sender.SendNative(ctx, terminal, text)
}

// readCEOWoken reads the highest sequence a typed wake line covered and when
// it was typed; none has been when the file is missing.
func readCEOWoken(stateDir string) (int, time.Time, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, ceoWokenFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, time.Time{}, nil
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	seq, at, _ := strings.Cut(strings.TrimSpace(string(data)), " ")
	covered, err := strconv.Atoi(seq)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("ceo wake: unreadable %s: %w", ceoWokenFile, err)
	}
	typedAt, _ := time.Parse(time.RFC3339Nano, at)
	return covered, typedAt, nil
}

func writeCEOWoken(stateDir string, seq int, at time.Time) error {
	return fsx.AtomicWriteFile(filepath.Join(stateDir, ceoWokenFile), []byte(strconv.Itoa(seq)+" "+at.UTC().Format(time.RFC3339Nano)+"\n"))
}
