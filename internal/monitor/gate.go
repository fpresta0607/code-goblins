package monitor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// GateSample is what a goblin's no-mistakes run looks like from the outside:
// whether a run is active, which step it is on, and how long that step has
// been the active one. NoCI records that the worktree has no GitHub Actions
// workflows at all, which is the one shape in which a ci step can never
// complete on its own.
type GateSample struct {
	Active       bool
	Step         string
	ActiveFor    time.Duration
	LastActivity string
	NoCI         bool
}

// GateProber reads a goblin's gate state. The monitor consults it only for a
// goblin that has read `working` past the busy-turn budget, so a probe cost
// of one subprocess is paid rarely and never on a healthy fleet.
type GateProber interface {
	InspectGate(ctx context.Context, meta state.TaskMeta) (GateSample, error)
}

// ExecGateProber shells out to `no-mistakes axi status` in the task worktree.
type ExecGateProber struct{}

// gateStatusBudget bounds one `no-mistakes axi status`, which answers in well
// under a second. The monitor's whole scan waits on it, and a supervisor whose
// scan never returns keeps its lock while its heartbeat stops, so nothing can
// take supervision over.
const gateStatusBudget = 30 * time.Second

var (
	activeStepLine = regexp.MustCompile(`^\s*([a-z_]+),running,([0-9hms]+),"?([^"]*)"?`)
)

func (ExecGateProber) InspectGate(ctx context.Context, meta state.TaskMeta) (GateSample, error) {
	sample := GateSample{}
	if meta.Worktree == "" {
		return sample, nil
	}
	if _, err := os.Stat(filepath.Join(meta.Worktree, ".github", "workflows")); err != nil {
		sample.NoCI = true
	}
	ctx, cancel := context.WithTimeout(ctx, gateStatusBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "no-mistakes", "axi", "status")
	cmd.Dir = meta.Worktree
	// A process the status call leaves running, such as a daemon it starts,
	// inherits the output pipe; without a delay Wait holds until that process
	// exits, which for a daemon is never.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return sample, err
	}
	return parseGateStatus(string(out), sample), nil
}

// gateProbeEvery is how long one reading of a worktree's gate stands for. The
// monitor asks a goblin's gate once a minute once it has been busy past
// busyTurnMax, and each `no-mistakes axi status` starts about ten git
// processes, so a reading stands for five minutes: a gate step wedged past
// the hour-long budget is named at most five minutes later.
const gateProbeEvery = 5 * time.Minute

// RecentGateProber reads each worktree's gate through Probe at most once per
// gateProbeEvery. Within the period it answers the last reading, with the
// active step's age moved on by the time since it was read.
type RecentGateProber struct {
	Probe GateProber
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu       sync.Mutex
	readings map[string]gateReading
}

type gateReading struct {
	at     time.Time
	sample GateSample
	err    error
}

func (p *RecentGateProber) InspectGate(ctx context.Context, meta state.TaskMeta) (GateSample, error) {
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	p.mu.Lock()
	reading, ok := p.readings[meta.Worktree]
	p.mu.Unlock()
	if !ok || now.Sub(reading.at) >= gateProbeEvery {
		sample, err := p.Probe.InspectGate(ctx, meta)
		reading = gateReading{at: now, sample: sample, err: err}
		p.mu.Lock()
		if p.readings == nil {
			p.readings = map[string]gateReading{}
		}
		p.readings[meta.Worktree] = reading
		p.mu.Unlock()
	}
	sample := reading.sample
	if sample.Active {
		sample.ActiveFor += now.Sub(reading.at)
	}
	return sample, reading.err
}

// parseGateStatus reads the active_steps row out of `axi status`. Only the
// active row matters: a completed or pending step is never what a goblin is
// wedged on.
func parseGateStatus(out string, sample GateSample) GateSample {
	inActive := false
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "active_steps[") {
			inActive = true
			continue
		}
		if !inActive {
			continue
		}
		m := activeStepLine.FindStringSubmatch(line)
		if m == nil {
			// The first non-matching line after the header ends the block.
			if trimmed != "" {
				break
			}
			continue
		}
		sample.Active = true
		sample.Step = m[1]
		if d, err := time.ParseDuration(m[2]); err == nil {
			sample.ActiveFor = d
		}
		sample.LastActivity = strings.TrimSpace(m[3])
		break
	}
	return sample
}
