package pipeline

import (
	"context"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/pipeline/pipelinetest"
)

// A fixture read that a loaded machine slows past the state-read bound is
// still answered, where CheckStart's tests failed on it (#1603) and the
// source-resume tests in cmd/goblins could too (#1983).
func TestAFixtureReadSlowedPastTheStateReadBoundIsAnswered(t *testing.T) {
	// Arrange
	answer := execx.Result{Stdout: []byte(`[{"branch":"fix/connections"}]`)}
	reader := Reader{Root: stateRoot(t), Commands: pipelinetest.FixtureCommands{Runner: sqliteRun{takes: 2 * stateQueryTimeout, result: answer}}}

	// Act
	branch, err := reader.RunBranch(context.Background(), "C:/project", "run")

	// Assert
	if err != nil || branch != "fix/connections" {
		t.Fatalf("got %q, %v; want the fixture read answered", branch, err)
	}
}
