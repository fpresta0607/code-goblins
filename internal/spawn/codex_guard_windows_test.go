package spawn

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/harness"
)

// nativeVariable is the value env, a native host environment, gives name.
func nativeVariable(env []string, name string) string {
	for _, entry := range env {
		if entryName, value, _ := strings.Cut(entry, "="); strings.EqualFold(entryName, name) {
			return value
		}
	}
	return ""
}

// A goblin's terminal finds its codex guard first on PATH, ahead of the
// user's own codex, so a codex it starts by name, from a rig, a probe or a
// test, meets the guard while Codex would bill credits. A terminal a rig in
// a guarded terminal starts, such as a scratch home's goblin, keeps that
// guard behind its own, and a launch that is no task's, as doctor's proof of
// a harness, gets none.
func TestAGoblinsTerminalFindsItsCodexGuardFirstOnPath(t *testing.T) {
	stateDir := t.TempDir()
	own := filepath.Join(stateDir, "codex-guard", "g1")
	rigs := filepath.Join(t.TempDir(), "state", "codex-guard", "live-1")
	userPath := `C:\Users\someone\AppData\Roaming\npm;C:\Windows\System32`
	for _, test := range []struct {
		name      string
		launchEnv map[string]string
		inherited string
		wantPath  string
		wantGuard string
	}{
		{name: "a goblin's terminal", launchEnv: map[string]string{harness.RoleVariable: harness.RoleGoblin, "CFO_TASK_ID": "g1"}, wantPath: own + ";" + userPath, wantGuard: own},
		{name: "a goblin a guarded rig spawns", launchEnv: map[string]string{harness.RoleVariable: harness.RoleGoblin, "CFO_TASK_ID": "g1"}, inherited: rigs, wantPath: own + ";" + rigs + ";" + userPath, wantGuard: own + ";" + rigs},
		{name: "a guard inherited twice is kept once", launchEnv: map[string]string{"CFO_TASK_ID": "g1"}, inherited: rigs + ";" + own, wantPath: own + ";" + rigs + ";" + userPath, wantGuard: own + ";" + rigs},
		{name: "a launch that is no task's", launchEnv: map[string]string{harness.RoleVariable: harness.RoleGoblin}, wantPath: userPath},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_CODEX_GUARD", test.inherited)
			service := Service{StateDir: stateDir, HomeRoot: filepath.Dir(stateDir)}
			userEnv := []string{"Path=" + userPath, "USERPROFILE=" + `C:\Users\someone`}

			// Act
			env := service.nativeHostEnvironment(userEnv, harness.Launch{Env: test.launchEnv}, auth.Result{})

			// Assert
			if got := nativeVariable(env, "PATH"); got != test.wantPath {
				t.Errorf("PATH = %q, want %q", got, test.wantPath)
			}
			if got := nativeVariable(env, "CFO_CODEX_GUARD"); got != test.wantGuard {
				t.Errorf("CFO_CODEX_GUARD = %q, want %q", got, test.wantGuard)
			}
		})
	}
}
