// Package pipelinetest helps tests read a gate fixture through
// pipeline.Reader.
package pipelinetest

import (
	"context"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// FixtureCommands runs a test's commands through its Runner, except that a
// sqlite3 read of a fixture database runs without the gate's one-minute bound
// on a state read and ends only with the test binary's own -timeout. That
// bound guards production against a wedged sqlite3, and a machine loaded by
// other runs has taken all of it only to start sqlite3, failing a fixture
// read before any of its test's assertions ran (#1603, #1983). The bound
// itself is checked with a stand-in sqlite3 in the pipeline package's
// driver_test.go.
type FixtureCommands struct{ execx.Runner }

func (f FixtureCommands) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "sqlite3" {
		ctx = context.WithoutCancel(ctx)
	}
	return f.Runner.Run(ctx, request)
}
