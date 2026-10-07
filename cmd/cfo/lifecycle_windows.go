package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func defaultTaskLifecycle(ctx context.Context, h home.Home, request lifecycle.Request, revision string) (state.Lifecycle, error) {
	meta, err := state.ReadTaskMeta(h.State, request.ID)
	if errors.Is(err, os.ErrNotExist) && request.Action == "stop" {
		return lifecycle.StopQueued(h, request, revision)
	}
	if err != nil {
		return state.Lifecycle{}, err
	}
	if data, err := fsx.ReadFile(filepath.Join(h.State, ".supervisor.json")); err == nil {
		var database supervisor.Database
		if err := json.Unmarshal(data, &database); err != nil {
			return state.Lifecycle{}, err
		}
		session := database.Sessions[database.TaskSessions[meta.ID]]
		if session.Generation == meta.SpawnGen && session.TaskID == meta.ID {
			request.Session = session.NativeID
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return state.Lifecycle{}, err
	}
	root, err := pipeline.DefaultRoot()
	if err != nil {
		return state.Lifecycle{}, err
	}
	commands := execx.OSRunner{}
	gate := pipeline.Reader{Root: root, Commands: commands}
	runtime := defaultCommandRuntime()
	var resources lifecycle.Resources
	service := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
		Helpers: func(ctx context.Context, meta state.TaskMeta, record *state.Lifecycle) ([]string, error) {
			return reachHelpers(ctx, h, meta, record, runtime.taskLifecycle)
		},
		Prepare: pauseInstruction(runtime, h),
		Stop: func(ctx context.Context, meta state.TaskMeta, record *state.Lifecycle) ([]string, error) {
			bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			var err error
			resources, err = lifecycle.TaskResources(bounded, h, meta, gate)
			stopped, teardown, stopErr := lifecycle.StopResources(bounded, resources)
			for _, process := range teardown {
				isTracked := false
				for _, prior := range record.Teardown {
					if prior.PID == process.PID && prior.Started.Equal(process.Started) {
						isTracked = true
						break
					}
				}
				if !isTracked {
					record.Teardown = append(record.Teardown, process)
				}
			}
			return stopped, errors.Join(err, stopErr)
		},
		Checkpoint: func(ctx context.Context, meta state.TaskMeta, record *state.Lifecycle) error {
			if resources.Gate.ID == "" {
				return nil
			}
			record.GateRun, record.GateIntent, record.GateHead = resources.Gate.ID, resources.Gate.Intent, resources.Gate.Head
			if err := state.WriteLifecycle(h.State, *record); err != nil {
				return err
			}
			preserved, err := gate.Interrupt(ctx, meta.Project, meta.Worktree, resources.Gate.Branch, resources.Gate.ID)
			if err != nil {
				return err
			}
			record.GateHead = preserved.Head
			record.Kept = append(record.Kept, "gate commits "+preserved.Head+"; validation restarts on Resume")
			record.Stopped = append(record.Stopped, "validation run "+preserved.ID)
			return nil
		},
		Resume: func(ctx context.Context, meta state.TaskMeta, prior state.Lifecycle) error {
			return resumeTask(ctx, h, runtime, commands, gate, meta, prior)
		},
		IsRunning: func(ctx context.Context, meta state.TaskMeta) (bool, error) {
			if meta.Backend == "native" {
				record, err := host.ReadRecord(h.State, meta.ID)
				if errors.Is(err, os.ErrNotExist) {
					return false, nil
				}
				if err != nil || !host.Running(record) {
					return false, err
				}
				client, err := host.Dial(record)
				if err != nil {
					return false, err
				}
				if err := client.Close(); err != nil {
					return false, err
				}
				return host.Running(record), nil
			}
			client := &herdr.Client{Commands: commands, Session: meta.HerdrSession}
			status, err := client.AgentStatus(ctx, herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID})
			return status == herdr.AgentAlive, err
		},
		Archive: func(ctx context.Context, meta state.TaskMeta, record *state.Lifecycle) (lifecycle.Preservation, error) {
			holder := ""
			if meta.Parent != "" {
				if parent, err := state.ReadTaskMeta(h.State, meta.Parent); err == nil {
					holder = parent.Worktree
				}
			}
			preserved, err := lifecycle.PreserveWork(ctx, commands, meta, holder)
			if err != nil {
				return preserved, err
			}
			record.Kept = preserved.Kept
			if err := state.WriteLifecycle(h.State, *record); err != nil {
				return preserved, err
			}
			if queued, err := fleet.ReadQueuedTask(h, meta.ID); err == nil {
				if err := fleet.RemoveQueuedTask(h, meta.ID, queued.Revision); err != nil {
					return preserved, err
				}
			} else if !errors.Is(err, fleet.ErrNotQueued) && !errors.Is(err, os.ErrNotExist) {
				return preserved, err
			}
			_, err = runtime.cleanup(ctx, h, meta.ID, !preserved.CanRemove)
			return preserved, err
		},
		Memory: func() (uint64, uint64, error) {
			memory, err := supervisor.MachineMemory()
			return memory.Available, memory.CommitAvailable, err
		},
		Admit: func() error {
			memory, err := supervisor.MachineMemory()
			if err != nil {
				return err
			}
			disk, err := supervisor.MachineDisk(h)
			if err != nil {
				return fmt.Errorf("free disk cannot be read, so nothing resumes: %w", err)
			}
			return supervisor.CheckLaunch(h, memory, disk)
		},
		Notify: func(record state.Lifecycle) error {
			return lifecycle.Report(h.State, record)
		},
	}}
	return service.Run(ctx, request)
}

func resumeTask(ctx context.Context, h home.Home, runtime commandRuntime, commands execx.Runner, gate pipeline.Reader, meta state.TaskMeta, prior state.Lifecycle) error {
	if meta.Backend != "native" {
		return fmt.Errorf("resume: task %s runs in backend %q; only a task in a native terminal can resume; retire it with cfo cleanup %s --force-archive", meta.ID, meta.Backend, meta.ID)
	}
	choice, choiceErr := state.ReadEngineChoice(h.State, meta.ID)
	if choiceErr != nil && !errors.Is(choiceErr, os.ErrNotExist) {
		return choiceErr
	}
	hasChoice := choiceErr == nil && choice.Generation == meta.SpawnGen
	if prior.GateRun != "" {
		branch, err := commands.Run(ctx, execx.Request{Dir: meta.Worktree, Name: "git", Args: []string{"symbolic-ref", "--short", "HEAD"}})
		if err != nil || branch.ExitCode != 0 {
			return errors.New("cannot read the paused validation branch")
		}
		if err := gate.RestartInterrupted(ctx, meta.Project, meta.Worktree, pipeline.InterruptedRun{ID: prior.GateRun, Branch: strings.TrimSpace(string(branch.Stdout)), Intent: prior.GateIntent}); err != nil {
			return err
		}
	}
	handoff := ""
	if prior.HandoffSaved {
		handoff = prior.Handoff
	}
	session := prior.Session
	pausedAt := prior.Started
	if prior.Pause != nil {
		pausedAt = prior.Pause.At
	}
	if pausedAt.IsZero() || time.Since(pausedAt) >= 24*time.Hour || pausedAt.After(time.Now()) {
		session = ""
	}
	request := spawn.SwitchRequest{ID: meta.ID, Generation: meta.SpawnGen, ForceDirty: true, BriefPath: meta.Brief, IsResume: true, ResumeSession: session, ResumeHandoff: handoff, ResumeNote: prior.ResumeNote}
	if hasChoice {
		request.Harness, request.Model, request.Effort = harness.Kind(choice.Harness), choice.Model, choice.Effort
		if choice.Harness != meta.Harness {
			request.ResumeSession = ""
		}
	}
	_, err := runtime.switchTask(ctx, h, request)
	if err == nil && hasChoice {
		return state.RemoveEngineChoice(h.State, meta.ID)
	}
	return err
}

// pauseInstruction types the instruction to write its handoff file into the
// goblin of a task being paused. A goblin in a turn takes it at its next tool
// call, so the pause then waits for the handoff as for any delivered one.
func pauseInstruction(runtime commandRuntime, h home.Home) func(context.Context, state.TaskMeta, string) error {
	return func(ctx context.Context, meta state.TaskMeta, handoff string) error {
		if err := runtime.sendText(ctx, h, meta.ID, lifecycle.PauseInstruction(handoff)); !errors.Is(err, fleet.ErrQueuedForToolCall) {
			return err
		}
		return nil
	}
}
