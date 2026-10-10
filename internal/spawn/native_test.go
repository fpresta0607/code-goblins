package spawn

import (
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A browser bridge belongs to one owner. chrome-devtools-axi keeps one bridge
// and one browser for each session name, and a terminal that names none
// shares the tool's unnamed session with every other program of the user: on
// 2026-10-09 a bridge of that session held 2.5 GB that no owner could be
// proved for. A task's terminal names its own session, by its id, whatever
// the user's environment or the project's manifest named.
func TestATasksTerminalNamesItsOwnBrowserSession(t *testing.T) {
	// Arrange
	env := map[string]string{"CHROME_DEVTOOLS_AXI_SESSION": "the-users-own"}

	// Act
	nativeEnvironment(env, state.TaskMeta{ID: "cg-process-cleanup", SpawnGen: "s1"})

	// Assert
	if got := env["CHROME_DEVTOOLS_AXI_SESSION"]; got != "cg-process-cleanup" {
		t.Fatalf("the terminal's browser session = %q, want its task's id", got)
	}
}
