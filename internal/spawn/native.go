package spawn

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// goblinSettingsFile is the Claude Code settings file, in the home's state
// folder, that every Claude Code goblin starts with.
const goblinSettingsFile = "goblin-claude-settings.json"

// goblinSettings writes the settings file a goblin of this home on kind
// starts with, which holds the goblin hooks, and returns its path: written
// anew at each launch, so it is this build's set. Only Claude Code takes
// one, and for any other harness it writes nothing and returns "".
func (s *Service) goblinSettings(kind harness.Kind) (string, error) {
	if kind != harness.Claude {
		return "", nil
	}
	settings, err := install.GoblinSettings(s.HomeRoot)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.StateDir, goblinSettingsFile)
	if err := fsx.AtomicWriteFile(path, settings); err != nil {
		return "", fmt.Errorf("write the goblin hooks to %s: %w", path, err)
	}
	return path, nil
}

// BrowserSessionVariable names the session chrome-devtools-axi keeps its
// bridge and its browser under. A terminal with none shares the tool's one
// unnamed session with every other program of the user, and on 2026-10-09 a
// bridge of that session held 2.5 GB that no owner could be proved for.
const BrowserSessionVariable = "CHROME_DEVTOOLS_AXI_SESSION"

// The launch stamps explicit task identity. Parentage comes only from an
// explicitly supplied session identity or Codex's native thread environment.
// The task's browser session is its own, named by its id, which is always a
// name the tool takes: at most 64 letters, digits, dots, dashes and
// underscores.
func nativeEnvironment(env map[string]string, meta state.TaskMeta) {
	env["CFO_TASK_ID"], env["CFO_SPAWN_GEN"] = meta.ID, meta.SpawnGen
	env[BrowserSessionVariable] = meta.ID
	parent, harness := os.Getenv("CFO_SESSION_ID"), os.Getenv("CFO_SESSION_HARNESS")
	if thread := os.Getenv("CODEX_THREAD_ID"); parent == "" && thread != "" {
		parent, harness = thread, "codex"
	}
	if harness != "codex" && harness != "claude" && harness != "pi" {
		parent, harness = "", ""
	}
	env["CFO_PARENT_SESSION_ID"], env["CFO_PARENT_HARNESS"] = parent, harness
	env["CFO_ROOT_SESSION_ID"] = os.Getenv("CFO_ROOT_SESSION_ID")
}
