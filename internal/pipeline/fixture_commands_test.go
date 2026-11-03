package pipeline

import (
	"context"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// fixtureCommands runs a test's commands through its Runner, except that a
// sqlite3 read of a fixture database runs without the gate's one-minute bound
// on a state read and ends only with the test binary's own -timeout. That
// bound guards production against a wedged sqlite3, and a machine loaded by
// other runs has taken all of it only to start sqlite3, failing a fixture
// read before any of its test's assertions ran (#1603). The bound itself is
// checked with a stand-in sqlite3 in driver_test.go.
type fixtureCommands struct{ execx.Runner }

func (f fixtureCommands) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "sqlite3" {
		ctx = context.WithoutCancel(ctx)
	}
	return f.Runner.Run(ctx, request)
}

// A fixture read that a loaded machine slows past the state-read bound is
// still answered, where CheckStart's tests failed on it (#1603).
func TestAFixtureReadSlowedPastTheStateReadBoundIsAnswered(t *testing.T) {
	// Arrange
	answer := execx.Result{Stdout: []byte(`[{"branch":"fix/connections"}]`)}
	reader := Reader{Root: stateRoot(t), Commands: fixtureCommands{sqliteRun{takes: 2 * stateQueryTimeout, result: answer}}}

	// Act
	branch, err := reader.RunBranch(context.Background(), "C:/project", "run")

	// Assert
	if err != nil || branch != "fix/connections" {
		t.Fatalf("got %q, %v; want the fixture read answered", branch, err)
	}
}
