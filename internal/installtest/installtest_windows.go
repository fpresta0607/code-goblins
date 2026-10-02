// Package installtest runs install.ps1 for its tests in a session stripped
// of this machine's own profile, so nothing an install reaches installs onto
// the machine. Its own tests are those of the no-mistakes install, a package
// apart from the root package's install tests so that each package's time
// stays well inside go test's timeout on CI.
package installtest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// userEnvFileName is the file in a stripped session's LOCALAPPDATA that
// stands in for the user-scope environment, so no install a test runs writes
// this machine's own.
const userEnvFileName = "user-env.json"

// WindowsPowerShell is Windows PowerShell 5.1, present on every Windows and
// the one install.cmd always starts.
func WindowsPowerShell() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

// OneLineShells are the PowerShells the one-line install must work in:
// Windows PowerShell 5.1, always present, and PowerShell 7 where installed.
func OneLineShells(t *testing.T) []string {
	t.Helper()
	shells := []string{WindowsPowerShell()}
	if pwsh, err := exec.LookPath("pwsh.exe"); err == nil {
		shells = append(shells, pwsh)
	}
	return shells
}

// StrippedCommand is name with args against the release served at base. It
// gets folders of its own for every per-user location, a file standing in for
// the user-scope environment, and a PATH with only Windows and the stand-ins
// stubs names on it, each a .cmd with the given text, so nothing it could
// reach installs onto this machine. The module path is Windows PowerShell's
// own modules alone: with none named, Windows PowerShell takes the machine's
// from the registry, and a session whose profile is empty has no module
// cache, so it reads every module on that path before its first command runs.
// A GitHub runner has hundreds there, which cost each session about 20
// seconds.
func StrippedCommand(t *testing.T, base string, stubs map[string]string, name string, args ...string) (cmd *exec.Cmd, local, temp string) {
	t.Helper()
	local, temp, profile, bin := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for tool, script := range stubs {
		if err := os.WriteFile(filepath.Join(bin, tool+".cmd"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	system := os.Getenv("SystemRoot")
	cmd = execx.Command(name, args...)
	cmd.Dir = temp
	cmd.Env = []string{
		"SystemRoot=" + system,
		"windir=" + system,
		"SystemDrive=" + os.Getenv("SystemDrive"),
		"ComSpec=" + os.Getenv("ComSpec"),
		"PATHEXT=" + os.Getenv("PATHEXT"),
		"ProgramFiles=" + os.Getenv("ProgramFiles"),
		"ProgramData=" + os.Getenv("ProgramData"),
		"PATH=" + bin + ";" + filepath.Join(system, "System32") + ";" + filepath.Join(system, "System32", "WindowsPowerShell", "v1.0"),
		"PSModulePath=" + filepath.Join(system, "System32", "WindowsPowerShell", "v1.0", "Modules"),
		"USERPROFILE=" + profile,
		"APPDATA=" + filepath.Join(profile, "Roaming"),
		"LOCALAPPDATA=" + local,
		"TEMP=" + temp,
		"TMP=" + temp,
		"CODE_GOBLINS_RELEASE_BASE=" + base,
		"CFO_USER_ENV_FILE=" + filepath.Join(local, userEnvFileName),
	}
	return cmd, local, temp
}

// sessions bounds how many commands Run has started and not yet seen end.
var sessions = make(chan struct{}, 4)

// Run runs cmd to its end and returns its combined output. The tests that
// run installs run in parallel, since each is its own session in folders of
// its own and spends its time waiting for PowerShell; Run lets four run at
// once and no more, whatever the machine's processor count, because each is
// a PowerShell session and everything it starts.
func Run(cmd *exec.Cmd) ([]byte, error) {
	sessions <- struct{}{}
	defer func() { <-sessions }()
	return cmd.CombinedOutput()
}

// ServeRelease serves a release holding binary and sums, or nothing at all.
func ServeRelease(t *testing.T, binary []byte, sums string) string {
	t.Helper()
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case binary != nil && r.URL.Path == "/cfo.exe":
			_, _ = w.Write(binary)
		case binary != nil && r.URL.Path == "/SHA256SUMS":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(release.Close)
	return release.URL
}
