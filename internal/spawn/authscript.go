package spawn

import (
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/harness"
)

// authScriptName is the restricted credential script every pane shell
// dot-sources before the harness starts. It is regenerated, never edited:
// a hand-appended line is exactly how a credential stops matching the store.
const authScriptName = "auth.ps1"

// writeAuthScript renders env with the generator the dispatch path uses and
// writes it to the task's tasktmp credential script. Names the launch
// contract owns are dropped, so a stored credential cannot redirect GOTMPDIR
// or the state override when the script is re-sourced later. The variable
// count is returned because a refresh line reports it.
func writeAuthScript(taskTmp string, env map[string]string) (string, int, error) {
	filtered := make(map[string]string, len(env))
	for name, value := range env {
		if reservedLaunchName(nil, name) {
			continue
		}
		filtered[name] = value
	}
	script, err := harness.RenderEnvScript(filtered)
	if err != nil {
		return "", 0, err
	}
	path := filepath.Join(taskTmp, authScriptName)
	if err := auth.WriteSecretFile(path, script); err != nil {
		return "", 0, err
	}
	return path, len(filtered), nil
}
