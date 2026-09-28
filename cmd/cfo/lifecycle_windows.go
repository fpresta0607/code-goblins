package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
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
	if data, err := os.ReadFile(filepath.Join(h.State, ".supervisor.json")); err == nil {
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
		Prepare: func(ctx context.Context, meta state.TaskMeta, handoff string) error {
			return runtime.sendText(ctx, h, meta.ID, "Pause requested. You have five seconds to reach a stopping point, write a handoff to "+handoff+", and push your branch if its mode allows. Your session and processes will then stop; retain all work and never bypass a gate.")
		},
		Stop: func(ctx context.Context, meta state.TaskMeta) ([]string, error) {
			bounded, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			var err error
			resources, err = lifecycle.TaskResources(bounded, h.State, meta, gate)
			stopped, stopErr := lifecycle.StopResources(bounded, resources)
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
			_, err := runtime.switchTask(ctx, h, spawn.SwitchRequest{ID: meta.ID, Generation: meta.SpawnGen, Session: meta.HerdrSession, ForceDirty: true, BriefPath: meta.Brief, IsResume: true, ResumeSession: prior.Session, ResumeHandoff: handoff})
			return err
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
			preserved, err := lifecycle.PreserveWork(ctx, commands, meta)
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
		Memory: func() (uint64, error) { available, _, err := supervisor.MachineMemory(); return available, err },
		Notify: func(record state.Lifecycle) error {
			return lifecycle.Report(h.State, record)
		},
	}}
	return service.Run(ctx, request)
}
