package supervisor

import (
	"context"
	"errors"
	"fmt"
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
// and again, and cheap. Measured on 2026-10-11 with nine terminals at work, a
// reading took 37 ms at the median and 101 ms at the 90th percentile, about
// a third of a percent of one processor.
const startWatchEvery = 5 * time.Second

// keepStartWatch watches what the fleet starts on its own loop, away from
// the supervisor's (startwatch). It ends nothing.
func (s *Service) keepStartWatch(ctx context.Context) {
	watch := &startwatch.Watch{}
	consoles := map[startedProcess]bool{}
	ticker := time.NewTicker(startWatchEvery)
	defer ticker.Stop()
	for {
		if err := s.watchStarts(watch, consoles, time.Now().UTC()); err != nil {
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

// startedProcess is one process by its id and its creation.
type startedProcess struct {
	pid     int
	created int64
}

// watchStarts takes one reading of what each goblin whose terminal runs, and
// the CFO's terminal, have started, and wakes the CFO once for each thing
// the watch finds. consoles keeps whether each process's console shows,
// which is asked once in its life.
func (s *Service) watchStarts(watch *startwatch.Watch, consoles map[startedProcess]bool, now time.Time) error {
	stateDir := s.Store.Home.State
	owners, generations := startWatchOwners(stateDir)
	placed, err := s.Options.Tree.Placed(owners)
	if err != nil {
		return fmt.Errorf("start watch: read the fleet's processes: %w", err)
	}
	listWindows, consoleShown := s.Options.Windows, s.Options.ConsoleShown
	if listWindows == nil {
		listWindows = proc.WindowOwners
	}
	if consoleShown == nil {
		consoleShown = proc.ConsoleShown
	}
	windows, err := listWindows()
	if err != nil {
		return fmt.Errorf("start watch: list the desktop's windows: %w", err)
	}
	reading := startwatch.Reading{At: now, Generations: generations, Resting: map[string]startwatch.Rest{}, CFO: NativeCFOTerminal}
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
	for _, owner := range owners {
		if rest, isResting := s.resting(owner.Meta); isResting {
			reading.Resting[owner.Meta.ID] = rest
		}
	}
	var problems error
	for _, finding := range watch.Read(reading) {
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
