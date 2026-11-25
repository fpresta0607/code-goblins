package connections

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	// machineLife is how long a check of this PC stands before it is made again.
	machineLife = 6 * time.Hour
	// projectLife is how long a check of a project stands.
	projectLife = time.Hour
	// checkLimit bounds one check of a level, which can run several probes.
	checkLimit = 5 * time.Minute
)

// The levels a refresh names.
const (
	RefreshMachine = "machine"
	RefreshProject = "project"
	RefreshGoblin  = "goblin"
)

var (
	// ErrUnknownLevel is a refresh of a level that is not one of the three.
	ErrUnknownLevel = errors.New("Choose the machine, a project or a goblin to recheck.")
	// ErrNotListed is a refresh of a project or goblin the snapshots do not list.
	ErrNotListed = errors.New("That project or goblin is not listed. Reload the page.")
)

// job is one recheck waiting for the worker.
type job struct{ level, scope string }

// goblinFile is a goblin's snapshot as its file keeps it: with the offset its
// session record was read to and the record that offset belongs to.
type goblinFile struct {
	Goblin
	Offset     int64  `json:"offset"`
	Transcript string `json:"transcript"`
}

// Snapshots holds what the Connectors page shows: the PC's check, each
// project's check and each running goblin's read, in memory and in files under
// one folder. Reading them starts nothing. Rechecks run one at a time in the
// background, on a change and when asked.
type Snapshots struct {
	// CheckMachine, CheckProject and ReadGoblin make the checks, and Transcript
	// finds a goblin's session record. They are fields so a test can replace
	// them and prove a read of the view called none.
	CheckMachine func(ctx context.Context) Level
	CheckProject func(ctx context.Context, name, path string) Level
	ReadGoblin   func(meta state.TaskMeta, transcript string, from int64, prior Goblin) (Goblin, int64)
	Transcript   func(meta state.TaskMeta) string
	// Env is where the checks look, and what repairs are planned from.
	Env Environment
	// Now is the clock results are dated and judged old by.
	Now func() time.Time
	// Failed is told of a file that could not be kept.
	Failed func(error)

	dir    string
	ctx    context.Context
	cancel context.CancelFunc
	work   sync.WaitGroup
	// reading lets one goblin be read at a time.
	reading sync.Mutex

	mu        sync.Mutex
	machine   Level
	projects  map[string]Project
	goblins   map[string]goblinFile
	tasks     func() []state.TaskMeta
	queue     []job
	isWorking bool
	sums      map[string]fileSum
}

// NewSnapshots reads back the snapshots kept under dir, which are shown at once
// and rechecked in the background as they are old or their facts changed.
func NewSnapshots(env Environment, dir string) *Snapshots {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Snapshots{
		CheckMachine: env.CheckMachine, CheckProject: env.CheckProject, ReadGoblin: env.ReadGoblin,
		Env: env, Now: time.Now, dir: dir, ctx: ctx, cancel: cancel,
		projects: map[string]Project{}, goblins: map[string]goblinFile{}, sums: map[string]fileSum{},
	}
	s.Transcript = s.sessionTranscript
	s.load()
	return s
}

// Close ends the background worker and waits for the check it is making.
func (s *Snapshots) Close() {
	s.cancel()
	s.work.Wait()
}

// Read is every level as it stands, at once: no check, no process and no probe.
func (s *Snapshots) Read() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	counts := map[string]int{}
	view := View{Machine: s.machine, Projects: make([]Project, 0, len(s.projects)), Goblins: make([]Goblin, 0, len(s.goblins))}
	view.Machine.Items = nonNil(view.Machine.Items)
	for _, kept := range s.goblins {
		goblin := kept.Goblin
		goblin.Items = nonNil(goblin.Items)
		view.Goblins = append(view.Goblins, goblin)
		counts[goblin.Project]++
	}
	for _, project := range s.projects {
		project.Items = nonNil(project.Items)
		view.Projects = append(view.Projects, project)
	}
	slices.SortFunc(view.Goblins, func(a, b Goblin) int {
		return strings.Compare(a.Project+"\x00"+a.Task, b.Project+"\x00"+b.Task)
	})
	slices.SortFunc(view.Projects, func(a, b Project) int {
		if counts[a.Name] != counts[b.Name] {
			return counts[b.Name] - counts[a.Name]
		}
		return strings.Compare(a.Name, b.Name)
	})
	return view
}

func nonNil(items []Item) []Item {
	if items == nil {
		return []Item{}
	}
	return items
}

// Refresh queues a background recheck of the machine, of one project or of one
// goblin, and marks it Checking at once. A request for one already waiting is
// merged into it.
func (s *Snapshots) Refresh(level, scope string) error {
	switch level {
	case RefreshMachine:
		scope = ""
	case RefreshProject:
		if !s.hasProject(scope) {
			return ErrNotListed
		}
	case RefreshGoblin:
		s.mu.Lock()
		_, isListed := s.goblins[scope]
		s.mu.Unlock()
		if !isListed {
			return ErrNotListed
		}
	default:
		return ErrUnknownLevel
	}
	s.enqueue(job{level, scope})
	return nil
}

// ReadNow reads one goblin again at once, from its records alone, as the page
// asks after a grant or a revoke changed what its record gives it.
func (s *Snapshots) ReadNow(meta state.TaskMeta) { s.readGoblin(meta, true) }

// enqueue puts a job last in the queue unless it waits there already, marks its
// level Checking and starts the worker if none runs.
func (s *Snapshots) enqueue(next job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil || slices.Contains(s.queue, next) {
		return
	}
	s.queue = append(s.queue, next)
	switch next.level {
	case RefreshMachine:
		s.machine.Checking = true
	case RefreshProject:
		project := s.projects[next.scope]
		project.Checking = true
		s.projects[next.scope] = project
	case RefreshGoblin:
		kept := s.goblins[next.scope]
		kept.Checking = true
		s.goblins[next.scope] = kept
	}
	if !s.isWorking {
		s.isWorking = true
		s.work.Add(1)
		go s.worker()
	}
}

// worker makes the queued checks one at a time and ends when none is left.
func (s *Snapshots) worker() {
	defer s.work.Done()
	for {
		s.mu.Lock()
		if len(s.queue) == 0 || s.ctx.Err() != nil {
			s.isWorking = false
			s.mu.Unlock()
			return
		}
		next := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		switch next.level {
		case RefreshMachine:
			s.checkMachine()
		case RefreshProject:
			s.checkProject(next.scope)
		case RefreshGoblin:
			s.rereadGoblin(next.scope)
		}
	}
}

// judged is the result of a check the time limit ended: what the level showed
// before, said not to have been rechecked. A check the supervisor's close cut
// short is no result at all.
func (s *Snapshots) judged(ctx context.Context, result, before Level) (Level, bool) {
	if s.ctx.Err() != nil {
		return Level{}, false
	}
	if ctx.Err() != nil {
		result = Level{Items: before.Items, Error: "The check did not finish in 5 minutes.", Basis: result.Basis}
	}
	now := s.Now().UTC()
	result.CheckedAt, result.Items = now, stamped(result.Items, now)
	return result, true
}

// stamped is items with the time of the check on each that has none.
func stamped(items []Item, at time.Time) []Item {
	items = slices.Clone(items)
	for index := range items {
		if items[index].CheckedAt.IsZero() {
			items[index].CheckedAt = at
		}
	}
	return items
}

func (s *Snapshots) isQueued(next job) bool { return slices.Contains(s.queue, next) }

func (s *Snapshots) checkMachine() {
	s.mu.Lock()
	before := s.machine
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, checkLimit)
	defer cancel()
	result, isResult := s.judged(ctx, s.CheckMachine(ctx), before)
	if !isResult {
		return
	}
	s.mu.Lock()
	result.Checking = s.isQueued(job{RefreshMachine, ""})
	s.machine = result
	s.mu.Unlock()
	result.Checking = false
	s.save(machineFile, result)
}

func (s *Snapshots) checkProject(name string) {
	s.mu.Lock()
	before, isListed := s.projects[name]
	s.mu.Unlock()
	if !isListed {
		return
	}
	basis := s.projectBasis(name, before.Path)
	ctx, cancel := context.WithTimeout(s.ctx, checkLimit)
	defer cancel()
	checked := s.CheckProject(ctx, name, before.Path)
	checked.Basis = basis
	result, isResult := s.judged(ctx, checked, before.Level)
	if !isResult {
		return
	}
	s.mu.Lock()
	current, isStillListed := s.projects[name]
	if isStillListed {
		result.Checking = s.isQueued(job{RefreshProject, name})
		current.Level = result
		s.projects[name] = current
	}
	s.mu.Unlock()
	if isStillListed {
		current.Level.Checking = false
		s.save(projectFile(name), current)
	}
}

// rereadGoblin reads a goblin a refresh named again, or lets it go when it is
// no longer running.
func (s *Snapshots) rereadGoblin(task string) {
	s.mu.Lock()
	tasks := s.tasks
	s.mu.Unlock()
	if tasks != nil {
		for _, meta := range tasks() {
			if meta.ID == task {
				s.readGoblin(meta, true)
				return
			}
		}
	}
	s.dropGoblin(task)
}
