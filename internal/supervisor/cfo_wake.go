package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	if len(fresh) == 0 || now.Sub(typedAt) < cfoWakeGap {
		return nil
	}
	primary, live := livePrimary(stateDir)
	if !live || primary.Host == "" || CFOWakeFor(primary.Agent) != CFOWakeTyped {
		return nil
	}
	return connection.typeWake(ctx, primary, wakeLine(fresh), fresh[len(fresh)-1].Seq, now)
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
func (c *CFOConnection) typeWake(ctx context.Context, primary primaryRegistration, text string, covered int, now time.Time) error {
	screens, readable := harness.NativeScreens(harness.Kind(primary.Agent))
	if !readable {
		return nil
	}
	if err := c.verify(ctx, primary); err != nil {
		return err
	}
	record, err := host.ReadRecord(c.State, primary.Host)
	if err != nil {
		return err
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
				return ctx.Err()
			}
		}
		// A screen that cannot be read, or shows the CFO in a turn, at a
		// dialog or with text in its composer, is looked at again on the next
		// tick.
		screen, err := read(record)
		if err != nil || !idleAtEmptyComposer(screens, screen) {
			return nil
		}
	}
	if err := writeCFOWoken(c.State, covered, now); err != nil {
		return err
	}
	deliver := c.Deliver
	if deliver == nil {
		deliver = c.deliverNative
	}
	if err := deliver(ctx, state.TaskMeta{ID: primary.Host, Harness: primary.Agent}, text); err != nil {
		return fmt.Errorf("a wake line was typed into the CFO's native terminal %s, and nothing proves it taken: %w", primary.Host, err)
	}
	return nil
}

// idleAtEmptyComposer reports whether screen shows a harness waiting at its
// composer with nothing in it: no turn, no dialog, no work running on it.
func idleAtEmptyComposer(screens harness.Screens, screen []string) bool {
	_, dialog := screens.Dialog(screen)
	_, running := harness.RunningWork(screen)
	return screens.IsReady(screen) && !dialog && !running && screens.ComposerEmpty(screen)
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
