package fleettree

import (
	"context"
	"time"

	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// The package's shape before its reading is written: every reader reads
// nothing.

type Process struct {
	PID       int
	ParentPID int
	Exe       string
	Created   int64
	Started   time.Time
	CPU       time.Duration
	Memory    uint64
}

const HarnessLaunch = 2 * time.Minute

type job struct {
	root    Process
	members []Process
}

func (j job) cpu() time.Duration { return 0 }
func (j job) name() string       { return "" }

type harnessProcesses struct {
	harness Process
	all     []Process
	jobs    []job
}

func readHarness(root int, processes []Process, launch time.Duration) (harnessProcesses, bool) {
	return harnessProcesses{}, false
}

type jobFacts struct {
	commands map[int]string
	listens  map[int][]int
}

func classify(j job, facts jobFacts) (Group, string, string) { return "", "", "" }

type GateProgress interface {
	Progress(ctx context.Context, project, branch string) (pipeline.Progress, error)
	StepDetails(ctx context.Context, runID string) ([]pipeline.StepDetail, error)
}

const GateEvery = time.Minute

type gateReading struct {
	at       time.Time
	progress pipeline.Progress
	steps    []pipeline.StepDetail
	err      error
}

func gateNode(reading gateReading, born time.Time, processes []Process) (Node, bool) {
	return Node{}, false
}

type Reader struct {
	Home        string
	Recorded    func(state.TaskMeta) string
	Gate        GateProgress
	Processes   func() ([]Process, error)
	Listeners   func() (map[int][]int, error)
	CommandLine func(pid int) (string, error)
	Now         func() time.Time
}

type Goblin struct {
	Meta       state.TaskMeta
	HarnessPID int
	Session    string
}

func (r *Reader) Read(ctx context.Context, goblin Goblin) (Tree, error) { return Tree{}, nil }

func WrittenAt(path string) time.Time { return time.Time{} }

func SessionTranscript(home, harness, session string) string { return "" }

func OwnedSession(stateDir string, meta state.TaskMeta) (string, error) { return "", nil }
