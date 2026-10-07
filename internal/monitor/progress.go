package monitor

import (
	"context"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// ProgressSample is what a goblin's own work looks like from outside its
// pane. agent_status says whether the harness is in a turn; this says whether
// anything underneath it is moving.
type ProgressSample struct {
	// TranscriptAt is when the goblin's own records last moved: its
	// conversation written, or any sub-agent, background shell or monitor
	// under it active (see fleettree.Tree.ActivityAt); zero when none was
	// found.
	TranscriptAt time.Time
	// Jobs names each job of processes the harness started after launching
	// that is still running - a tool command, a background job, a monitor
	// loop - as "name (pid N)", and each sub-agent, background shell or
	// monitor still working under it, which hold its turn the same way.
	Jobs []string
	// JobCPU is the processor time those processes and everything under them
	// have used.
	JobCPU time.Duration
}

// ProgressProber reads a goblin's progress evidence. The monitor consults it
// only for a goblin a stale wake is otherwise due for, so its cost is paid
// rarely and never on a healthy, short turn. An error means some evidence
// could not be read; the sample still carries whatever was.
type ProgressProber interface {
	InspectProgress(ctx context.Context, meta state.TaskMeta, sample EndpointSample) (ProgressSample, error)
}

// PaneProcesses reads a pane's operating-system identity.
type PaneProcesses interface {
	PaneProcessInfo(ctx context.Context, target herdr.Target) (herdr.PaneProcessInfo, error)
}

// HostProgress reads progress evidence on this machine from the goblin's
// fleet tree, the reader the board's family tree reads through, so the
// stale rules and the board never disagree about what a goblin is doing.
type HostProgress struct {
	Panes PaneProcesses
	// StateDir is where native terminals' hosts record the program each
	// runs, the harness of a native task.
	StateDir string
	// Home is the user's home directory, where every harness keeps its
	// transcripts. Empty skips the transcript.
	Home string
	// Tree is the reader, shared with the board where one process runs
	// both; nil reads through one of its own over Home, which takes the
	// conversation the board recorded in StateDir.
	Tree *fleettree.Reader

	once sync.Once
}

// tree is the reader the prober reads through.
func (h *HostProgress) tree() *fleettree.Reader {
	h.once.Do(func() {
		if h.Tree == nil {
			h.Tree = &fleettree.Reader{Home: h.Home, Recorded: func(meta state.TaskMeta) string {
				// A record that cannot be read proves no conversation, and
				// the harness's own record of its process still can.
				session, _ := fleettree.OwnedSession(h.StateDir, meta)
				return session
			}}
		}
	})
	return h.Tree
}

// InspectProgress reads the goblin's tree: its own records' last activity,
// and the jobs and children that hold its turn. The harness of a native task
// is the program its terminal runs; otherwise it is whatever Herdr reports
// in the pane's foreground, and a pane back at its shell has no harness and
// so no processes of its own. An error means some evidence could not be
// read; the sample still carries whatever was.
func (h *HostProgress) InspectProgress(ctx context.Context, meta state.TaskMeta, sample EndpointSample) (ProgressSample, error) {
	harnessPID, started, pidErr := h.harness(ctx, meta, sample)
	// The harness the terminal runs now is the one whose records are read,
	// as Herdr reports it for a pane.
	if sample.Harness != "" {
		meta.Harness = sample.Harness
	}
	tree, treeErr := h.tree().Read(ctx, fleettree.Goblin{Meta: meta, HarnessPID: harnessPID, HarnessStarted: started, Session: sample.Session})
	progress := ProgressSample{TranscriptAt: tree.ActivityAt()}
	if pidErr != nil || harnessPID == 0 {
		return progress, pidErr
	}
	if treeErr != nil {
		return progress, treeErr
	}
	progress.Jobs, progress.JobCPU = tree.Jobs()
	return progress, nil
}

// harness returns the process id of the task's harness, 0 for a pane back at
// its shell, and when a native terminal's host recorded it started, which
// tells it from a later process Windows gave its id.
func (h *HostProgress) harness(ctx context.Context, meta state.TaskMeta, sample EndpointSample) (int, time.Time, error) {
	if meta.Backend == "native" {
		record, err := host.ReadRecord(h.StateDir, meta.ID)
		if err != nil {
			return 0, time.Time{}, err
		}
		return record.ChildPID, record.ChildStart, nil
	}
	info, err := h.Panes.PaneProcessInfo(ctx, sample.Endpoint.Target)
	if err != nil {
		return 0, time.Time{}, err
	}
	if info.ForegroundProcessGroupID == info.ShellPID {
		return 0, time.Time{}, nil
	}
	return info.ForegroundProcessGroupID, time.Time{}, nil
}
