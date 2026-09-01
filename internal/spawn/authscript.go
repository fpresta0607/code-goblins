package spawn

import (
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/harness"
)

// authScriptName is the restricted credential script every pane shell
// dot-sources before the harness starts. It is regenerated, never edited:
// a hand-appended line is exactly how a credential stops matching the store.
const authScriptName = "auth.ps1"

// writeAuthScript renders env with the generator the dispatch path uses and
// writes it to the task's tasktmp credential script. The script is rendered
// to a sibling temp file and renamed into place, so a shell that re-sources
// auth.ps1 mid-write reads either the whole old script or the whole new one,
// never a torn mix; the owner-only permissions land on the temp file before
// the swap. Names the launch contract owns are dropped, so a stored
// credential cannot redirect GOTMPDIR or the state override when the script
// is re-sourced later. The variable count is returned because a refresh line
// reports it.
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
	if err := os.MkdirAll(taskTmp, 0o700); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(taskTmp, authScriptName+".tmp-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", 0, err
	}
	if err := auth.WriteSecretFile(tmpPath, script); err != nil {
		os.Remove(tmpPath)
		return "", 0, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return "", 0, err
	}
	return path, len(filtered), nil
}
