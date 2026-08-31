package monitor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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
	cmd := exec.CommandContext(ctx, "no-mistakes", "axi", "status")
	cmd.Dir = meta.Worktree
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return sample, err
	}
	return parseGateStatus(string(out), sample), nil
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
