//go:build !windows

package main

import (
	"context"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// forgetGateDaemonTask removes nothing: only Windows keeps a gate daemon's
// sign-in task as a scheduled task this build removes.
func forgetGateDaemonTask(context.Context, execx.Runner, string) error { return nil }
