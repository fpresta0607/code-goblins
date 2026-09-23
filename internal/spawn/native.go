package spawn

import (
	"github.com/fpresta0607/code-goblins/internal/state"
	"os"
)

// The launch stamps explicit task identity. Parentage comes only from an
// explicitly supplied session identity or Codex's native thread environment.
func nativeEnvironment(env map[string]string, meta state.TaskMeta) {
	env["CFO_TASK_ID"], env["CFO_SPAWN_GEN"] = meta.ID, meta.SpawnGen
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
