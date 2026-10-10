package spawn

import (
	"github.com/fpresta0607/code-goblins/internal/state"
	"os"
)

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
