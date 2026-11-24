package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// RecordName is the services record in the home's state folder: every
// project's stack, who holds it, and whether goblins started the engine.
const RecordName = "services.json"

// Record is what goblins knows of the stacks it started or shares.
type Record struct {
	// Engine says whether goblins started the Docker engine, which it then
	// stops once no stack it holds is left.
	Engine Engine `json:"engine"`
	// Stacks is each project's stack, by project name. A stack nobody holds
	// stays in the record for its measured cost.
	Stacks map[string]Stack `json:"stacks,omitempty"`
	// Admitting is each project's start that holds the machine's turn and
	// waits for the memory its services cost, by project name, so the
	// supervisor starts and resumes no goblin into that memory.
	Admitting map[string]Admitting `json:"admitting,omitempty"`
}

// Admitting is one start of a project's stack that waits for memory or is
// starting, and the memory it was admitted with.
type Admitting struct {
	Task string `json:"task"`
	// Cost is what the start is expected to take, in bytes.
	Cost uint64 `json:"cost"`
	// PID is the process that runs the start, and Started when that process
	// began, which tells it from a later process the system gave its pid.
	PID     int       `json:"pid"`
	Started time.Time `json:"started,omitzero"`
	Since   time.Time `json:"since"`
}

// IsLive reports whether the process that runs the start is still the same
// process: its pid runs and began when the start's process did. A start that
// could not read its own process's start cannot be told from a later process
// and counts as live.
func (a Admitting) IsLive() bool {
	started, isRunning := proc.StartTime(a.PID)
	return isRunning && (a.Started.IsZero() || started.Equal(a.Started))
}

// Engine is the Docker engine as goblins found it.
type Engine struct {
	StartedByCEO bool      `json:"started_by_ceo"`
	Since        time.Time `json:"since,omitzero"`
}

// Stack is one project's compose stack.
type Stack struct {
	Project  string `json:"project"`
	Checkout string `json:"checkout"`
	Compose  string `json:"compose"`
	EnvFile  string `json:"env_file,omitempty"`
	// Services are the services the holders asked for.
	Services []string `json:"services,omitempty"`
	// Holders are the tasks holding the stack. The stack is up while any
	// does, and the last to release it stops what goblins started.
	Holders []Hold `json:"holders,omitempty"`
	// Owned says goblins started the stack from nothing, so the last release
	// takes the whole compose project down.
	Owned bool `json:"owned,omitempty"`
	// Started are the services goblins started beside a stack that was already
	// running when it came, which the last release stops and nothing else.
	Started []string `json:"started,omitempty"`
	// Since is when the stack came up for its holders.
	Since time.Time `json:"since,omitzero"`
	// Cost is what the stack was last measured to cost.
	Cost Cost `json:"cost,omitzero"`
	// MemoryBeforeStart is the free memory read before a start that has not
	// measured itself yet. A start cut short, as the memory floor ends one,
	// never does, so the stop that follows measures from this reading.
	MemoryBeforeStart uint64 `json:"memory_before_start,omitempty"`
}

// Hold is one task holding a stack.
type Hold struct {
	Task  string    `json:"task"`
	Since time.Time `json:"since"`
}

// Cost is the memory a stack takes from the machine, engine included when
// goblins started the engine for it: the larger of the drop in available memory
// while it started and the rise while it stopped, in its latest run.
type Cost struct {
	Bytes      uint64    `json:"bytes"`
	MeasuredAt time.Time `json:"measured_at"`
}

// IsUp reports whether any task holds the stack.
func (s Stack) IsUp() bool { return len(s.Holders) > 0 }

// Holds reports whether task holds the stack.
func (s Stack) Holds(task string) bool {
	return slices.ContainsFunc(s.Holders, func(hold Hold) bool { return hold.Task == task })
}

// HolderIDs are the holders' task ids, in the order they came.
func (s Stack) HolderIDs() []string {
	ids := make([]string, len(s.Holders))
	for index, hold := range s.Holders {
		ids[index] = hold.Task
	}
	return ids
}

// Sorted returns the record's stacks by project name.
func (r Record) Sorted() []Stack {
	stacks := make([]Stack, 0, len(r.Stacks))
	for _, stack := range r.Stacks {
		stacks = append(stacks, stack)
	}
	sort.Slice(stacks, func(i, j int) bool { return stacks[i].Project < stacks[j].Project })
	return stacks
}

// ReadRecord reads the services record of stateDir. A home that never
// started a stack has an empty one.
func ReadRecord(stateDir string) (Record, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, RecordName))
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, nil
	}
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("services: read %s: %w", RecordName, err)
	}
	return record, nil
}

// WriteRecord keeps the services record in stateDir.
func WriteRecord(stateDir string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, RecordName), append(data, '\n'))
}

// LiveIn reports, for a task id, whether stateDir holds the task's record. A
// record that exists but cannot be read is live: only a record that is gone,
// as cleanup archives it, ends a task's hold.
func LiveIn(stateDir string) func(task string) bool {
	return func(task string) bool {
		if state.ValidTaskID(task) != nil {
			return false
		}
		_, err := os.Stat(state.TaskMetaPath(stateDir, task))
		return !errors.Is(err, os.ErrNotExist)
	}
}
