//go:build !windows

package main

import (
	"context"
	"errors"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func defaultTaskLifecycle(context.Context, home.Home, lifecycle.Request, string) (state.Lifecycle, error) {
	return state.Lifecycle{}, errors.New("task process control is supported on Windows")
}

func endLeftProcesses(context.Context, home.Home, state.TaskMeta) ([]string, error) {
	return nil, nil
}

func openGateRun(home.Home) func(context.Context, state.TaskMeta) (string, error) {
	return func(context.Context, state.TaskMeta) (string, error) {
		return "", errors.New("a task's gate run is read on Windows")
	}
}
