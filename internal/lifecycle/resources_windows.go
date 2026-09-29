package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type Resources struct {
	Directories []string
	Hosts       []Identity
	Gate        pipeline.InterruptedRun
}

// TaskResources is the task-to-resources boundary. Supervisor-owned helpers
// can extend this set without changing Pause or Stop's termination rules.
func TaskResources(ctx context.Context, stateDir string, meta state.TaskMeta, gate pipeline.Reader) (Resources, error) {
	var resources Resources
	expected := filepath.Join(meta.Project, ".worktrees", "gb-"+meta.ID)
	if !filepath.IsAbs(meta.Project) || !strings.EqualFold(filepath.Clean(meta.Worktree), expected) {
		return resources, errors.New("task worktree is not its isolated project worktree")
	}
	if !strings.EqualFold(filepath.Clean(meta.TaskTmp), filepath.Join(stateDir, "tasktmp", meta.ID)) {
		return resources, errors.New("task scratch directory does not match its task identity")
	}
	resources.Directories = []string{meta.Worktree, meta.TaskTmp}
	slug := strings.Map(func(value rune) rune {
		if value >= 'a' && value <= 'z' || value >= '0' && value <= '9' {
			return value
		}
		return '-'
	}, strings.ToLower(filepath.Clean(meta.Worktree)))
	resources.Directories = append(resources.Directories, filepath.Join(os.TempDir(), "claude", slug))
	if meta.Backend == "native" {
		record, err := host.ReadRecord(stateDir, meta.ID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return resources, err
		}
		if err == nil && host.Running(record) {
			started, exists := proc.StartTime(record.HostPID)
			if !exists {
				return resources, errors.New("task host ended before identifying its resources")
			}
			client, err := host.Dial(record)
			if err != nil {
				return resources, fmt.Errorf("prove native task host: %w", err)
			}
			_ = client.Close()
			current, exists := proc.StartTime(record.HostPID)
			if !exists || !current.Equal(started) {
				return resources, errors.New("task host changed while identifying its resources")
			}
			resources.Hosts = append(resources.Hosts, Identity{PID: record.HostPID, Started: started})
		}
	} else if meta.Backend == "herdr" {
		client := &herdr.Client{Commands: gate.Commands, Session: meta.HerdrSession}
		target := herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}
		info, err := client.PaneProcessInfo(ctx, target)
		if err != nil {
			status, statusErr := client.AgentStatus(ctx, target)
			if statusErr != nil || status != herdr.AgentMissing {
				return resources, fmt.Errorf("identify legacy task pane: %w", errors.Join(err, statusErr))
			}
		}
		started, exists := proc.StartTime(info.ShellPID)
		if exists {
			current, err := client.PaneProcessInfo(ctx, target)
			if err != nil || current.ShellPID != info.ShellPID {
				return resources, errors.New("legacy task pane changed while identifying its resources")
			}
			if currentStart, exists := proc.StartTime(info.ShellPID); !exists || !currentStart.Equal(started) {
				return resources, errors.New("legacy task shell changed while identifying its resources")
			}
			resources.Hosts = append(resources.Hosts, Identity{PID: info.ShellPID, Started: started})
		}
	}
	if _, err := os.Stat(filepath.Join(gate.Root, "state.sqlite")); errors.Is(err, os.ErrNotExist) {
		return resources, nil
	} else if err != nil {
		return resources, err
	}
	result, err := gate.Commands.Run(ctx, execx.Request{Dir: meta.Worktree, Name: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err == nil && result.ExitCode == 1 {
		return resources, nil
	}
	if err != nil || result.ExitCode != 0 {
		return resources, errors.New("read the task branch before identifying its gate")
	}
	resources.Gate, err = gate.Interruption(ctx, meta.Project, strings.TrimSpace(string(result.Stdout)))
	if errors.Is(err, pipeline.ErrNoProgress) {
		return resources, nil
	}
	if err != nil {
		return resources, err
	}
	if resources.Gate.IsTerminal() {
		resources.Gate = pipeline.InterruptedRun{}
		return resources, nil
	}
	if resources.Gate.Worktree != "" {
		expected := filepath.Join(gate.Root, "worktrees", resources.Gate.RepoID, resources.Gate.ID)
		if !strings.EqualFold(filepath.Clean(resources.Gate.Worktree), expected) {
			return resources, errors.New("gate worktree differs from its verified run directory")
		}
		resources.Directories = append(resources.Directories, resources.Gate.Worktree)
	}
	return resources, nil
}

func StopResources(ctx context.Context, resources Resources) ([]string, error) {
	stopped := []string{}
	for sweep := 0; sweep < 4; sweep++ {
		processes, err := Inventory(ctx, resources.Directories, resources.Hosts)
		if err != nil {
			return stopped, err
		}
		if len(processes) == 0 {
			return stopped, nil
		}
		var failures error
		for _, process := range processes {
			label := fmt.Sprintf("%s pid %d", process.Name, process.PID)
			if err := Terminate(Identity{PID: process.PID, Started: process.Started}); err != nil {
				failures = errors.Join(failures, fmt.Errorf("%s: %w", label, err))
			} else {
				stopped = append(stopped, label)
			}
		}
		if failures != nil {
			return stopped, failures
		}
		select {
		case <-ctx.Done():
			return stopped, ctx.Err()
		case <-time.After(75 * time.Millisecond):
		}
	}
	remaining, err := Inventory(ctx, resources.Directories, resources.Hosts)
	if err != nil {
		return stopped, err
	}
	if len(remaining) > 0 {
		var names []string
		for _, process := range remaining {
			names = append(names, fmt.Sprintf("%s pid %d", process.Name, process.PID))
		}
		return stopped, fmt.Errorf("task processes remain: %s", strings.Join(names, ", "))
	}
	return stopped, nil
}
