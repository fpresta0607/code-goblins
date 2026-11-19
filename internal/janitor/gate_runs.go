package janitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
)

// gateRetention is how long the gate keeps a finished run's leftover checkout
// when its config.yaml sets no worktree.retention: no-mistakes'
// DefaultWorktreeRetention.
const gateRetention = 24 * time.Hour

// GatePastRetention is what the gate's home keeps against its own rule: the
// checkouts of runs its database records as completed whose worktree.retention
// ended before a gate run last finished, which its reaper removes. The reaper
// runs at the daemon's start and as each run finishes, never on a timer, so a
// checkout whose retention ended since is one it has not yet had a turn at. A
// failed or cancelled run's checkout the gate may keep on purpose, for work it
// could still continue, so only a completed run's is past its rule. Linked are
// those under a repository folder that is a junction or a link, which its
// reaper does not reach (no-mistakes#261); Unlinked those under a folder of
// its own, which it reaches and still left.
type GatePastRetention struct {
	Retention time.Duration `json:"retention"`
	Linked    GateRuns      `json:"linked,omitzero"`
	Unlinked  GateRuns      `json:"unlinked,omitzero"`
}

// GateRuns is a count of the gate's completed runs' checkouts and when the
// oldest of them was last written.
type GateRuns struct {
	Count  int       `json:"count"`
	Oldest time.Time `json:"oldest"`
}

func (runs *GateRuns) add(written time.Time) {
	if runs.Count == 0 || written.Before(runs.Oldest) {
		runs.Oldest = written
	}
	runs.Count++
}

// gatePastRetention reads, in the gate's home root, every run checkout under
// its worktrees folder whose run its database records as completed and whose
// retention, from when it was last written, ended before a gate run last
// finished. It returns nil when the gate keeps every checkout, its retention
// set to unlimited or 0, and an error, measuring nothing, when its
// configuration, its database or a folder cannot be read.
func gatePastRetention(ctx context.Context, root string) (*GatePastRetention, error) {
	retention, err := readGateRetention(root)
	if err != nil || retention == 0 {
		return nil, err
	}
	completed, lastFinished, err := finishedGateRuns(ctx, filepath.Join(root, "state.sqlite"))
	if err != nil {
		return nil, err
	}
	past := &GatePastRetention{Retention: retention}
	worktrees := filepath.Join(root, "worktrees")
	repositories, err := os.ReadDir(worktrees)
	if errors.Is(err, fs.ErrNotExist) {
		return past, nil
	}
	if err != nil {
		return nil, err
	}
	for _, repository := range repositories {
		isLinked := repository.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0
		if !repository.IsDir() && !isLinked {
			continue
		}
		folder := filepath.Join(worktrees, repository.Name())
		runs, err := os.ReadDir(folder)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			if !run.IsDir() || !completed[run.Name()] {
				continue
			}
			info, err := os.Stat(filepath.Join(folder, run.Name()))
			if err != nil {
				return nil, err
			}
			if !info.ModTime().Add(retention).Before(lastFinished) {
				continue
			}
			if isLinked {
				past.Linked.add(info.ModTime())
			} else {
				past.Unlinked.add(info.ModTime())
			}
		}
	}
	return past, nil
}

// readGateRetention reads worktree.retention from the gate's config.yaml as
// no-mistakes reads it: unset is gateRetention, and unlimited, none, off,
// never or a duration of 0 or less is 0, which keeps every checkout.
func readGateRetention(root string) (time.Duration, error) {
	data, err := fsx.ReadFile(filepath.Join(root, "config.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return gateRetention, nil
	}
	if err != nil {
		return 0, err
	}
	var config struct {
		Worktree struct {
			Retention string `yaml:"retention"`
		} `yaml:"worktree"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return 0, fmt.Errorf("the gate's config.yaml: %w", err)
	}
	value := strings.ToLower(strings.TrimSpace(config.Worktree.Retention))
	switch value {
	case "":
		return gateRetention, nil
	case "unlimited", "none", "off", "never":
		return 0, nil
	}
	retention, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("the gate's worktree.retention %q: %w", config.Worktree.Retention, err)
	}
	return max(retention, 0), nil
}

// finishedGateRuns reads the gate's database at path, in process as goblins
// reads it elsewhere: the id of every run it records as completed, and when a
// run last finished, the latest update of a run in any of the gate's finished
// states, which is when its reaper last ran.
func finishedGateRuns(ctx context.Context, path string) (map[string]bool, time.Time, error) {
	result, err := gatedb.Runner{}.Run(ctx, execx.Request{Name: "sqlite3", Args: []string{"-readonly", "-json", path, "SELECT id, status, updated_at FROM runs WHERE status IN ('completed', 'failed', 'cancelled', 'checks_passed', 'ci_monitor_interrupted', 'handed_off')"}})
	if err != nil {
		return nil, time.Time{}, err
	}
	if result.ExitCode != 0 {
		return nil, time.Time{}, fmt.Errorf("the gate's database: %s", strings.TrimSpace(string(result.Stderr)))
	}
	var rows []struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		UpdatedAt int64  `json:"updated_at"`
	}
	if len(result.Stdout) > 0 {
		if err := json.Unmarshal(result.Stdout, &rows); err != nil {
			return nil, time.Time{}, fmt.Errorf("the gate's database: %w", err)
		}
	}
	completed := make(map[string]bool, len(rows))
	var lastFinished time.Time
	for _, row := range rows {
		if row.Status == "completed" {
			completed[row.ID] = true
		}
		if finished := time.Unix(row.UpdatedAt, 0); finished.After(lastFinished) {
			lastFinished = finished
		}
	}
	return completed, lastFinished, nil
}
