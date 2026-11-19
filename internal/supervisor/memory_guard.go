package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	// memoryGuardEvery is how often the memory guard reads memory and the
	// processes. A minute was too slow: the PC could run out of memory between
	// the readings that found it short.
	memoryGuardEvery = 5 * time.Second
	// startSeenWithin is how soon after its terminal's host started the guard
	// must first see a goblin for its start to be measured: a goblin seen
	// later has taken part of its memory already.
	startSeenWithin = time.Minute
)

// hostRun names one run of a goblin's terminal host, so a goblin whose host
// was replaced is a new start.
type hostRun struct {
	pid     int
	started int64
}

// guardedTree is the private working set a goblin's process tree held at the
// guard's previous reading.
type guardedTree struct {
	host  hostRun
	bytes uint64
}

// startWatch is a goblin start the guard measures: the most private working
// set its process tree held since the guard first saw its host.
type startWatch struct {
	host hostRun
	peak uint64
}

// settledStart is a measured start, to be kept as a sample of its harness.
type settledStart struct {
	harness string
	cost    StartCost
}

// memoryGuard is what the memory guard remembers between its readings, in
// memory only, under its own mutex: each goblin's process tree at the reading
// before, the starts it measures, and the reading it last acted on.
type memoryGuard struct {
	mu      sync.Mutex
	trees   map[string]guardedTree
	watches map[string]startWatch
	last    *GuardReading
}

// guardedGoblin is a native goblin whose terminal host runs.
type guardedGoblin struct {
	meta state.TaskMeta
	host host.Record
}

// observe takes the guard's reading of goblins at now: it returns how many
// bytes of private working set each goblin's process tree grew since the
// reading before, and the starts whose first launchSettle has passed, whose
// measuring is over. A reading with no process list has no growth, and the
// next one has none to compare with.
func (g *memoryGuard) observe(goblins []guardedGoblin, processes []fleettree.Process, isListed bool, now time.Time) (map[string]uint64, []settledStart) {
	g.mu.Lock()
	defer g.mu.Unlock()
	growth := map[string]uint64{}
	trees := map[string]guardedTree{}
	watches := map[string]startWatch{}
	var settled []settledStart
	for _, goblin := range goblins {
		id := goblin.meta.ID
		run := hostRun{goblin.host.HostPID, goblin.host.Started.UnixNano()}
		var bytes uint64
		if isListed {
			bytes = treeMemory(processes, goblin.host.HostPID)
			trees[id] = guardedTree{run, bytes}
			if before, exists := g.trees[id]; exists && before.host == run {
				growth[id] = bytes - min(bytes, before.bytes)
			}
		}
		age := now.Sub(goblin.host.Started)
		watch, isWatched := g.watches[id]
		if !isWatched || watch.host != run {
			watch, isWatched = startWatch{host: run}, age < startSeenWithin
		}
		if !isWatched {
			continue
		}
		watch.peak = max(watch.peak, bytes)
		switch {
		case age < launchSettle:
			watches[id] = watch
		case watch.peak > 0:
			settled = append(settled, settledStart{goblin.meta.Harness, StartCost{Bytes: watch.peak, At: now, Task: id}})
		}
	}
	g.trees, g.watches = trees, watches
	return growth, settled
}

// record keeps reading as the reading the guard last acted on.
func (g *memoryGuard) record(reading GuardReading) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.last = &reading
}

// reading is the reading the guard last acted on, or nil when it has acted on
// none.
func (g *memoryGuard) reading() *GuardReading {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
		return nil
	}
	reading := *g.last
	return &reading
}

// keepMemoryGuard reads memory and the processes every `every` until ctx ends.
// It runs apart from the fleet's reading once a minute, since a PC short of
// memory has little time. A reading that fails is published once, not at
// every reading, until a reading succeeds again.
func (s *Service) keepMemoryGuard(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	isFailing := false
	for {
		err := s.guardMemory(time.Now().UTC())
		if err != nil && !isFailing {
			s.publish(err)
		}
		isFailing = err != nil
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// guardMemory takes one reading of the memory guard at now. It follows every
// live native goblin's process tree and measures the cost of the starts it
// sees (writeStartCost), and when the lesser of the free memory and commit is
// under the floor from the home's settings it pauses one goblin at once
// (pauseOneForMemory). It does nothing where no memory is read.
func (s *Service) guardMemory(now time.Time) error {
	dispatch := s.Options.Dispatch
	if dispatch == nil || dispatch.Memory == nil {
		return nil
	}
	memory, err := dispatch.Memory()
	if err != nil {
		return fmt.Errorf("the memory guard cannot read memory: %w", err)
	}
	floor, err := MachineMemoryFloor(s.Store.Home)
	if err != nil {
		return err
	}
	var processes []fleettree.Process
	var isListed bool
	if dispatch.Processes != nil {
		var listErr error
		processes, listErr = dispatch.Processes()
		isListed = listErr == nil
	}
	live, problems := s.guardedGoblins()
	growth, settled := s.guard.observe(live, processes, isListed, now)
	for _, start := range settled {
		problems = errors.Join(problems, writeStartCost(s.Store.Home.State, start.harness, start.cost))
	}
	if min(memory.Available, memory.CommitAvailable) >= floor {
		return problems
	}
	return errors.Join(problems, s.pauseOneForMemory(live, growth, memory, floor, now))
}

// guardedGoblins are the native goblins whose terminal host runs, the lowest in
// the Overlord's In progress order first.
func (s *Service) guardedGoblins() ([]guardedGoblin, error) {
	stateDir := s.Store.Home.State
	tasks, problems := inProgressOrder(s.Store.Home)
	slices.Reverse(tasks)
	var live []guardedGoblin
	for _, meta := range tasks {
		if meta.Backend != "native" {
			continue
		}
		hostRecord, err := host.ReadRecord(stateDir, meta.ID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		if host.Running(hostRecord) {
			live = append(live, guardedGoblin{meta, hostRecord})
		}
	}
	return live, problems
}

// pauseOneForMemory pauses one goblin of live for the reading memory, which
// is under floor, and keeps the reading and what it did as the guard's last.
// While a start or another change is under way, a pause included, or the PC
// restarts, it does nothing: a change under way frees what it frees only once
// it ends, and the next reading under the floor pauses the next goblin.
func (s *Service) pauseOneForMemory(live []guardedGoblin, growth map[string]uint64, memory Memory, floor uint64, now time.Time) error {
	s.starts.Lock()
	isBusy := s.isRestartingPC() || s.starting != "" || len(s.changing) > 0
	s.starts.Unlock()
	if isBusy {
		return nil
	}
	candidates, problems := s.pausableGoblins(live, growth)
	reading := GuardReading{At: now, Available: memory.Available, CommitAvailable: memory.CommitAvailable, Floor: floor}
	pick, isFound, nothing := pickToPause(candidates, hostsPushingOrMerging(), now)
	if !isFound {
		reading.Action = nothing
		s.guard.record(reading)
		return problems
	}
	evidence := fmt.Sprintf("%.1f GB of memory and %.1f GB of commit free, under the %g GB floor; %s was %s",
		gigabytes(memory.Available), gigabytes(memory.CommitAvailable), gigabytes(floor), pick.meta.ID, pick.why)
	began, err := s.pauseAtFloor(pick.meta, pick.record, "memory", "", evidence)
	if began {
		reading.Action = fmt.Sprintf("paused %s: %s", pick.meta.ID, pick.why)
		s.guard.record(reading)
	}
	return errors.Join(problems, err)
}
