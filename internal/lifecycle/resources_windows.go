package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
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

// TaskResources is the task-to-resources boundary. A task's helpers are its
// own, so their resources join its set without changing Pause or Stop's
// termination rules: stopping a parent ends whatever its helpers' own stops
// left. Every directory it names is a task's own by its identity: the
// worktree spawn made for it in the home, or where an older build put it,
// the extra worktrees it recorded beside that, its task temporary directory
// and its scratch folder, so a record that names anything else stops
// nothing.
func TaskResources(ctx context.Context, h home.Home, meta state.TaskMeta, gate pipeline.Reader) (Resources, error) {
	resources, err := heldResources(ctx, h, meta, gate.Commands)
	if err != nil {
		return resources, err
	}
	return withGate(ctx, meta, gate, resources)
}

// heldResources are what a task and its helpers hold themselves, without the
// task's gate run.
func heldResources(ctx context.Context, h home.Home, meta state.TaskMeta, commands execx.Runner) (Resources, error) {
	resources, err := ownResources(ctx, h, meta, commands)
	if err != nil {
		return resources, err
	}
	helpers, err := state.HelpersOf(h.State, meta.ID)
	if err != nil {
		return resources, err
	}
	for _, helper := range helpers {
		owned, err := ownResources(ctx, h, helper, commands)
		if err != nil {
			return resources, fmt.Errorf("helper %s: %w", helper.ID, err)
		}
		resources.Directories = append(resources.Directories, owned.Directories...)
		resources.Hosts = append(resources.Hosts, owned.Hosts...)
	}
	return resources, nil
}

// ownResources are the directories and terminal one task holds itself.
func ownResources(ctx context.Context, h home.Home, meta state.TaskMeta, commands execx.Runner) (Resources, error) {
	var resources Resources
	stateDir := h.State
	project := filepath.Base(filepath.Clean(meta.Project))
	if !filepath.IsAbs(meta.Project) || !slices.ContainsFunc(h.OwnWorktrees(meta.Project, meta.ID), func(own string) bool { return strings.EqualFold(filepath.Clean(meta.Worktree), own) }) {
		return resources, errors.New("task worktree is not its isolated project worktree")
	}
	if !strings.EqualFold(filepath.Clean(meta.TaskTmp), filepath.Join(stateDir, "tasktmp", meta.ID)) {
		return resources, errors.New("task scratch directory does not match its task identity")
	}
	resources.Directories = []string{meta.Worktree, meta.TaskTmp}
	for _, extra := range meta.Extras {
		name := filepath.Base(filepath.Clean(extra))
		if !slices.ContainsFunc(h.WorktreeRoots(), func(root string) bool {
			return strings.EqualFold(filepath.Dir(filepath.Clean(extra)), filepath.Join(root, project))
		}) || !strings.HasPrefix(strings.ToLower(name), strings.ToLower(meta.ID)+"-") {
			return resources, errors.New("task extra worktree is not one beside its own")
		}
		resources.Directories = append(resources.Directories, extra)
	}
	if meta.Scratch != "" {
		if !slices.ContainsFunc(h.ScratchRoots(), func(root string) bool {
			return strings.EqualFold(filepath.Clean(meta.Scratch), filepath.Join(root, meta.ID))
		}) {
			return resources, errors.New("task scratch folder does not match its task identity")
		}
		resources.Directories = append(resources.Directories, meta.Scratch)
	}
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
		client := &herdr.Client{Commands: commands, Session: meta.HerdrSession}
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
	return resources, nil
}

// withGate adds the task's open gate run, and its worktree, to resources.
func withGate(ctx context.Context, meta state.TaskMeta, gate pipeline.Reader, resources Resources) (Resources, error) {
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

// stopBound bounds identifying a task's resources and sweeping its
// processes; its terminals end on a wait of their own.
const stopBound = 10 * time.Second

// StopTask ends what a task holds, for a pause or a stop: its terminals
// first, which ends its goblin, then whatever the sweep finds in its
// directories and its terminals' jobs. Once its terminals have ended, a
// gate whose state could not be read in time, as while the no-mistakes
// daemon runs other sessions' gates, or a sweep that ran out of time, is an
// UnfinishedStop. Each process still finishing its Windows teardown is kept
// in record.
//
// A memory pause leaves the task's open gate run alone: the run is no part
// of what it ends, so nothing sweeps the run's worktree, aborts it or records
// it for a restart, and its goblin picks it back up when it resumes. A run
// holds an hour or more of review and tests, the pause is taken for the
// goblin's own memory, and on a machine short of memory the abort itself
// fails: on 2026-10-09 Murray's memory pause could not abort his live run,
// and no resume could bring him back.
func StopTask(ctx context.Context, h home.Home, meta state.TaskMeta, gate pipeline.Reader, record *state.Lifecycle) (Resources, []string, error) {
	bounded, cancel := context.WithTimeout(ctx, stopBound)
	defer cancel()
	var resources Resources
	var err error
	if record.Action == "pause" && record.Pause != nil && record.Pause.Reason == "memory" {
		resources, err = heldResources(bounded, h, meta, gate.Commands)
	} else {
		resources, err = TaskResources(bounded, h, meta, gate)
	}
	stopped, teardown, stopErr := StopResources(bounded, resources)
	for _, process := range teardown {
		if !slices.ContainsFunc(record.Teardown, func(prior state.TeardownProcess) bool {
			return prior.PID == process.PID && prior.Started.Equal(process.Started)
		}) {
			record.Teardown = append(record.Teardown, process)
		}
	}
	var unfinished UnfinishedStop
	if err != nil && len(resources.Hosts) > 0 && (stopErr == nil || errors.As(stopErr, &unfinished)) {
		return resources, stopped, UnfinishedStop{Err: errors.Join(err, unfinished.Err)}
	}
	return resources, stopped, errors.Join(err, stopErr)
}

func StopResources(ctx context.Context, resources Resources) ([]string, []state.TeardownProcess, error) {
	return stopResources(ctx, resources, Terminate)
}

// hostStopWait bounds ending the task's terminals, which goes ahead whatever
// is left of the bound on the rest of the stop.
const hostStopWait = 10 * time.Second

func stopResources(ctx context.Context, resources Resources, stop func(context.Context, Identity) (bool, error)) ([]string, []state.TeardownProcess, error) {
	stopped := []string{}
	var teardown []state.TeardownProcess
	finished := map[Identity]bool{}
	// Ending a host ends its job, so the services in it are kept first.
	if err := keepServicesPastHosts(resources.Hosts); err != nil {
		return stopped, teardown, err
	}
	// The terminals end first, by their own identities, on a wait of their
	// own: ending one ends the goblin's harness and the job under it, which
	// hold its memory, and the sweep below reads every process on the
	// machine, which on a machine short of memory can run out of time before
	// it ends anything. Once they have ended, what the sweep meets is an
	// UnfinishedStop.
	hosts, cancel := context.WithTimeout(context.WithoutCancel(ctx), hostStopWait)
	defer cancel()
	for _, host := range resources.Hosts {
		label := fmt.Sprintf("terminal host pid %d", host.PID)
		isTeardown, err := stop(hosts, host)
		if err != nil {
			return stopped, teardown, fmt.Errorf("%s: %w", label, err)
		}
		stopped = append(stopped, label)
		finished[host] = true
		if isTeardown {
			teardown = append(teardown, state.TeardownProcess{PID: host.PID, Started: host.Started, Name: "terminal host"})
		}
	}
	unfinished := func(err error) error {
		if len(resources.Hosts) > 0 {
			return UnfinishedStop{Err: err}
		}
		return err
	}
	for sweep := 0; sweep < 4; sweep++ {
		processes, err := Inventory(ctx, resources.Directories, resources.Hosts)
		if err != nil {
			return stopped, teardown, unfinished(err)
		}
		var pending []Process
		for _, process := range processes {
			if !finished[Identity{PID: process.PID, Started: process.Started}] {
				pending = append(pending, process)
			}
		}
		if len(pending) == 0 {
			return stopped, teardown, nil
		}
		type result struct {
			isTeardown bool
			err        error
		}
		results := make([]result, len(pending))
		var requests sync.WaitGroup
		for index, process := range pending {
			requests.Go(func() {
				results[index].isTeardown, results[index].err = stop(ctx, Identity{PID: process.PID, Started: process.Started})
			})
		}
		requests.Wait()
		var failures error
		for index, process := range pending {
			label := fmt.Sprintf("%s pid %d", process.Name, process.PID)
			if err := results[index].err; err != nil {
				failures = errors.Join(failures, fmt.Errorf("%s: %w", label, err))
			} else {
				stopped = append(stopped, label)
				finished[Identity{PID: process.PID, Started: process.Started}] = true
				if results[index].isTeardown {
					teardown = append(teardown, state.TeardownProcess{PID: process.PID, Started: process.Started, Name: process.Name})
				}
			}
		}
		if failures != nil {
			return stopped, teardown, unfinished(failures)
		}
		select {
		case <-ctx.Done():
			return stopped, teardown, unfinished(ctx.Err())
		case <-time.After(75 * time.Millisecond):
		}
	}
	remaining, err := Inventory(ctx, resources.Directories, resources.Hosts)
	if err != nil {
		return stopped, teardown, unfinished(err)
	}
	if len(remaining) > 0 {
		var names []string
		for _, process := range remaining {
			if !finished[Identity{PID: process.PID, Started: process.Started}] {
				names = append(names, fmt.Sprintf("%s pid %d", process.Name, process.PID))
			}
		}
		if len(names) > 0 {
			return stopped, teardown, unfinished(fmt.Errorf("task processes remain: %s", strings.Join(names, ", ")))
		}
	}
	return stopped, teardown, nil
}

// keepServicesPastHosts stops a task host's job from ending a machine service
// in it (proc.Service) when the host ends: teardown ends the host, and with it
// the job's last handle, which would end every process still in the job.
func keepServicesPastHosts(hosts []Identity) error {
	if len(hosts) == 0 {
		return nil
	}
	services, err := proc.RunningServices()
	if err != nil {
		return fmt.Errorf("identify the machine services running: %w", err)
	}
	for _, host := range hosts {
		if started, exists := proc.StartTime(host.PID); !exists || !started.Equal(host.Started) {
			continue
		}
		members, err := proc.JobProcesses(host.PID)
		if err != nil {
			return fmt.Errorf("read task host %d job: %w", host.PID, err)
		}
		if slices.ContainsFunc(members, func(member proc.Entry) bool { return services[member.PID] != proc.NoService }) {
			if err := proc.KeepJobsOnClose(host.PID); err != nil {
				return err
			}
		}
	}
	return nil
}
