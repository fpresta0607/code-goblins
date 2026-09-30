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
