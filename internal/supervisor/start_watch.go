package supervisor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/startwatch"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// startWatchEvery is how often the supervisor reads what each goblin and the
// CFO's terminal have started: often enough to see one command started again
// and again, and cheap. A dry run on the live machine on 2026-10-11, with
// seven terminals at work, took 58 ms a reading at the median and 188 ms at
// the 90th percentile, 0.9 percent of one processor.
const startWatchEvery = 5 * time.Second

// startWatchOwnersEvery is how often the start watch reads again whose
// starts it watches and which goblins rest after their report. Reading every
// task's records took 90 ms on 2026-10-11, too much for every reading, and
// a goblin starts, pauses or comes to rest far less often.
const startWatchOwnersEvery = 30 * time.Second

// keepStartWatch watches what the fleet starts on its own loop, away from
// the supervisor's (startwatch). It ends nothing.
func (s *Service) keepStartWatch(ctx context.Context) {
	watching := &startWatching{}
	ticker := time.NewTicker(startWatchEvery)
	defer ticker.Stop()
	for {
		if err := s.watchStarts(watching, time.Now().UTC()); err != nil {
			s.mu.Lock()
			if s.fleetErr == nil || !strings.Contains(s.fleetErr.Error(), err.Error()) {
				s.fleetErr = errors.Join(s.fleetErr, err)
			}
			s.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// startWatching is what the start watch keeps between its readings: the
// watch, whether each process's console shows, which is asked once in its
// life, whether the supervisor hid the console window each shown goblin
// process showed, which it tries once in the process's life, and the owners
// it watches with their generations and which of them rest after their
// report, as last read at ownersAt.
type startWatching struct {
	watch       startwatch.Watch
	consoles    map[startedProcess]bool
	hidden      map[startedProcess]bool
	owners      []fleettree.Goblin
	generations map[string]string
	resting     map[string]startwatch.Rest
	ownersAt    time.Time
}

// startedProcess is one process by its id and its creation.
type startedProcess struct {
	pid     int
	created int64
}

// watchStarts takes one reading of what each goblin whose terminal runs, and
// the CFO's terminal, have started, and wakes the CFO once for each thing
// the watch finds.
func (s *Service) watchStarts(watching *startWatching, now time.Time) error {
	stateDir := s.Store.Home.State
	if watching.consoles == nil {
		watching.consoles, watching.hidden = map[startedProcess]bool{}, map[startedProcess]bool{}
	}
	if watching.ownersAt.IsZero() || now.Sub(watching.ownersAt) >= startWatchOwnersEvery || now.Before(watching.ownersAt) {
		watching.owners, watching.generations = startWatchOwners(stateDir)
		watching.resting = map[string]startwatch.Rest{}
		for _, owner := range watching.owners {
			if rest, isResting := s.resting(owner.Meta); isResting {
				watching.resting[owner.Meta.ID] = rest
			}
		}
		watching.ownersAt = now
	}
	consoles := watching.consoles
	placed, err := s.Options.Tree.Placed(watching.owners)
	if err != nil {
		return fmt.Errorf("start watch: read the fleet's processes: %w", err)
	}
	listWindows, consoleShown, hideConsole := s.Options.Windows, s.Options.ConsoleShown, s.Options.HideConsole
	if listWindows == nil {
		listWindows = proc.WindowOwners
	}
	if consoleShown == nil {
		consoleShown = proc.ConsoleShown
	}
	if hideConsole == nil {
		hideConsole = hideConsoleOnDesktop
	}
	windows, err := listWindows()
	if err != nil {
		return fmt.Errorf("start watch: list the desktop's windows: %w", err)
	}
	reading := startwatch.Reading{At: now, Generations: watching.generations, Resting: watching.resting, CFO: NativeCFOTerminal}
	running := map[startedProcess]bool{}
	for _, process := range placed {
		key := startedProcess{process.PID, process.Created}
		running[key] = true
		shown := ""
		if windows[process.PID] {
			shown = "a window of its own"
		} else {
			isShown, isAsked := consoles[key]
			if !isAsked {
				// A process that ended before it was asked shows nothing. One
				// in its first second may not have its console yet, so it is
				// asked again at the next reading.
				isShown, _ = consoleShown(process.PID, windows)
				if now.Sub(process.Started) >= time.Second {
					consoles[key] = isShown
				}
			}
			if isShown {
				shown = "a console window"
			}
		}
		reading.Processes = append(reading.Processes, startwatch.Process{Placed: process, Shown: shown})
	}
	for key := range consoles {
		if !running[key] {
			delete(consoles, key)
		}
	}
	for key := range watching.hidden {
		if !running[key] {
			delete(watching.hidden, key)
		}
	}
	problems := hideGoblinConsoles(reading.Processes, watching.hidden, hideConsole)
	for _, finding := range watching.watch.Read(reading) {
		if _, isNew, err := wake.AppendFirst(stateDir, finding.Identity, "check", finding.Owner, finding.Detail); err != nil || !isNew {
			problems = errors.Join(problems, err)
			continue
		}
		if _, err := wake.PublishEpisode(stateDir); err != nil {
			problems = errors.Join(problems, err)
		}
	}
	return problems
}

// hideGoblinConsoles hides the console window each goblin's process that
// shows one opened, as a careless background start does: a console Windows
// gave a program started alone, shown as a window of its own or in Windows
// Terminal, which stays blank for as long as a heavy run waits and ends the
// run when the Overlord closes it. A window of the CFO's terminal's is left
// for the CFO to answer for. Each process is tried once in its life, the
// first started first, so a console a program shares with what it runs is
// hidden through the program, which the wake names. hidden keeps what each
// try found, and each process shown is marked with it.
func hideGoblinConsoles(processes []startwatch.Process, hidden map[startedProcess]bool, hideConsole func(int) (bool, error)) error {
	var shown []int
	for at, process := range processes {
		if process.Shown != "" && process.Owner != NativeCFOTerminal {
			shown = append(shown, at)
		}
	}
	slices.SortStableFunc(shown, func(a, b int) int { return processes[a].Started.Compare(processes[b].Started) })
	var problems error
	for _, at := range shown {
		key := startedProcess{processes[at].PID, processes[at].Created}
		if _, isTried := hidden[key]; !isTried {
			isHidden, err := hideConsole(processes[at].PID)
			if err != nil {
				problems = errors.Join(problems, fmt.Errorf("start watch: hide the console window of %s's pid %d: %w", processes[at].Owner, processes[at].PID, err))
			}
			hidden[key] = isHidden
		}
		processes[at].IsHidden = hidden[key]
	}
	return problems
}

// startWatchOwners are the terminals whose starts the watch reads: each live
// goblin's whose terminal runs and that no pause or stop holds, and the
// CFO's, with the generation of each.
func startWatchOwners(stateDir string) ([]fleettree.Goblin, map[string]string) {
	var owners []fleettree.Goblin
	generations := map[string]string{}
	for _, meta := range liveTasks(stateDir) {
		if meta.Backend != "native" {
			continue
		}
		if record, err := state.ReadLifecycle(stateDir, meta.ID); err == nil && record.Generation == meta.SpawnGen && record.SuppressesMonitoring(stateDir) {
			continue
		}
		record, err := host.ReadRecord(stateDir, meta.ID)
		if err != nil || !host.Running(record) {
			continue
		}
		owners = append(owners, fleettree.Goblin{Meta: meta, HarnessPID: record.ChildPID, HarnessStarted: record.ChildStart})
		generations[meta.ID] = meta.SpawnGen
	}
	if record, err := host.ReadRecord(stateDir, NativeCFOTerminal); err == nil && host.Running(record) {
		owners = append(owners, fleettree.Goblin{Meta: state.TaskMeta{ID: NativeCFOTerminal}, HarnessPID: record.ChildPID, HarnessStarted: record.ChildStart})
		generations[NativeCFOTerminal] = record.ChildStart.UTC().Format(time.RFC3339Nano)
	}
	return owners, generations
}

// resting is the goblin's report, when it reported done or failed and rests
// since, by the rule the janitor's sweep reads a goblin at rest by.
func (s *Service) resting(meta state.TaskMeta) (startwatch.Rest, bool) {
	if meta.ID == NativeCFOTerminal || janitor.AtRestSince(s.Store.Home.State, meta.ID).IsZero() {
		return startwatch.Rest{}, false
	}
	lines, err := s.statusTail(meta.ID)
	if err != nil {
		return startwatch.Rest{}, false
	}
	reportedAt, report := ownReport(lines, spawnTime(meta.SpawnGen))
	if kind := reportKind(report); kind != "done" && kind != "failed" {
		return startwatch.Rest{}, false
	}
	return startwatch.Rest{Report: report, ReportedAt: reportedAt}, true
}
