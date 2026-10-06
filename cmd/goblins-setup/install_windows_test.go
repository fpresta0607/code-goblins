package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/installtest"
)

// installScript is the repository's install script, the one a release
// publishes.
func installScript(t *testing.T) []byte {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("..", "..", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	return script
}

// standInRelease serves a release whose cfo.exe is this test binary standing
// in for one whose every command succeeds, beside the repository's own
// install script.
func standInRelease(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"/cfo.exe":     binary,
		"/SHA256SUMS":  []byte(fmt.Sprintf("%x  cfo.exe\n", sha256.Sum256(binary))),
		"/install.ps1": installScript(t),
	}
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(release.Close)
	return release.URL
}

// strippedInstall is name with args in a session of its own against the
// release at base, as internal/installtest gives one, with a stand-in for
// every tool the install looks for, so nothing installs onto this machine:
// a script for each it runs as a command, this test binary as claude.exe,
// which a native terminal needs, and as the managed no-mistakes, which
// answers the version the script pins.
func strippedInstall(t *testing.T, base, name string, args ...string) (*exec.Cmd, string) {
	t.Helper()
	stubs := map[string]string{}
	for _, tool := range []string{"git", "gh", "codex", "pi", "tasks-axi", "quota-axi", "gh-axi", "chrome-devtools-axi", "lavish-axi", "npm", "winget"} {
		stubs[tool] = "@exit /b 0\r\n"
	}
	cmd, local, temp := installtest.StrippedCommand(t, base, stubs, name, args...)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	for _, copy := range []string{filepath.Join(tools, "claude.exe"), filepath.Join(local, "no-mistakes", "no-mistakes.exe")} {
		if err := os.MkdirAll(filepath.Dir(copy), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := copyFile(self, copy); err != nil {
			t.Fatal(err)
		}
	}
	pin := regexp.MustCompile(`\$noMistakesVersion = "([^"]+)"`).FindSubmatch(installScript(t))
	if pin == nil {
		t.Fatal("install.ps1 pins no no-mistakes version")
	}
	for i, variable := range cmd.Env {
		if path, ok := strings.CutPrefix(variable, "PATH="); ok {
			cmd.Env[i] = "PATH=" + tools + ";" + path
		}
	}
	cmd.Env = append(cmd.Env, cfoStandInVariable+"="+string(pin[1]))
	return cmd, temp
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o755)
}

// plainSteps are the step lines the install prints, in order.
var plainSteps = []string{
	"[1/4] Download Code Goblins",
	"[2/4] Check the download",
	"[3/4] Install Code Goblins and its tools",
	"[4/4] Open Code Goblins",
}

// The setup and the one-line install are one install underneath and say the
// same plain steps: the one-line install prints the four steps and no line
// that is not a plain one, keeping its details in its log, and the setup,
// running the same script from the same release, shows those four steps in
// order and ends well.
func TestTheSetupAndTheOneLineInstallSayTheSamePlainSteps(t *testing.T) {
	// Arrange
	base := standInRelease(t)
	line, lineTemp := strippedInstall(t, base, installtest.WindowsPowerShell(),
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "irm "+base+"/install.ps1 | iex; exit $LASTEXITCODE")
	window, windowTemp := strippedInstall(t, base, installtest.WindowsPowerShell())
	setup := Setup{
		Script: base + "/install.ps1",
		Shell:  installtest.WindowsPowerShell(),
		Log:    filepath.Join(windowTemp, "CodeGoblinsInstall.log"),
		Client: http.DefaultClient,
		Env:    window.Env,
	}
	var steps []int

	// Act
	printed, lineErr := line.CombinedOutput()
	setupErr := setup.Install(context.Background(), func(progress Progress) {
		if progress.Step > 0 {
			steps = append(steps, progress.Step)
		}
	})

	// Assert
	if lineErr != nil {
		t.Fatalf("the one-line install = %v:\n%s", lineErr, printed)
	}
	var said []string
	for _, text := range strings.Split(strings.ReplaceAll(string(printed), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(text) == "" {
			continue
		}
		if _, sentence, plain := readLine(text); !plain || sentence != "" {
			t.Errorf("the one-line install printed %q, which is not one of its plain lines", text)
		}
		if stepLine.MatchString(text) {
			said = append(said, text)
		}
	}
	if !slices.Equal(said, plainSteps) {
		t.Errorf("the one-line install said the steps %q, want %q", said, plainSteps)
	}
	if kept, err := os.ReadFile(filepath.Join(lineTemp, "CodeGoblinsInstall.log")); err != nil || !strings.Contains(string(kept), "Verified cfo.exe") {
		t.Errorf("the one-line install's log (%v) lacks its details:\n%s", err, kept)
	}
	if setupErr != nil {
		t.Fatalf("the setup = %v:\n%s", setupErr, setup.Details())
	}
	if want := []int{1, 1, 2, 3, 4}; !slices.Equal(steps, want) {
		t.Errorf("the setup showed the steps %v, want its own download, then the script's four: %v", steps, want)
	}
}

// The window names the steps the install script says, in its order, so
// what it lists before a step begins is what the step is.
func TestTheWindowNamesTheScriptsSteps(t *testing.T) {
	// Arrange
	named := regexp.MustCompile(`<span class="name">([^<]+)</span>`).FindAllStringSubmatch(page, -1)
	var shown []string
	for _, name := range named {
		shown = append(shown, name[1])
	}

	// Act
	list := regexp.MustCompile(`(?m)^\s*\$releaseSteps = @\((.*)\)`).FindSubmatch(installScript(t))

	// Assert
	if list == nil {
		t.Fatal("install.ps1 lists no $releaseSteps")
	}
	var steps []string
	for _, step := range regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(list[1], -1) {
		steps = append(steps, string(step[1]))
	}
	if !slices.Equal(shown, steps) || len(steps) != len(plainSteps) {
		t.Errorf("the window lists the steps %q, the install script says %q", shown, steps)
	}
}
