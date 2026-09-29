package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func taskRecovery(h home.Home, runtime commandRuntime) *supervisor.TaskRecovery {
	return &supervisor.TaskRecovery{
		State: h.State,
		Tasks: func() ([]state.TaskMeta, error) {
			scan, err := state.ScanIDs(h.State)
			if err != nil {
				return nil, err
			}
			tasks := make([]state.TaskMeta, len(scan.MetaIDs))
			for index, id := range scan.MetaIDs {
				tasks[index] = state.TaskMeta{ID: id}
			}
			return tasks, nil
		},
		Memory: supervisor.MachineMemory,
		IsRunning: func(ctx context.Context, task state.TaskMeta) (bool, error) {
			if task.Backend == "native" {
				record, err := host.ReadRecord(h.State, task.ID)
				if errors.Is(err, os.ErrNotExist) {
					return false, nil
				}
				if err != nil {
					return false, err
				}
				return host.Running(record), nil
			}
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			client := &herdr.Client{Commands: execx.OSRunner{}, Session: task.HerdrSession}
			status, err := client.AgentStatus(ctx, herdr.Target{Session: task.HerdrSession, Pane: task.HerdrPaneID})
			return status == herdr.AgentAlive, err
		},
		Resume: func(ctx context.Context, task state.TaskMeta, session string) error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			_, err := runtime.switchTask(ctx, h, spawn.SwitchRequest{ID: task.ID, ResumeSession: session})
			return err
		},
	}
}
