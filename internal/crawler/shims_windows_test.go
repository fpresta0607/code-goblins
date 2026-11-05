package crawler

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// A goblin starts goblin-crawl by name from the shell its harness runs, with
// the connector's bin folder on its PATH, and the connector is handed every
// argument as it was typed, one with a space in it included, from a home
// whose own path has a space in it too.
func TestTheCommandSetupWritesStartsTheConnectorWithItsArgumentsFromEveryShell(t *testing.T) {
	// Arrange
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("goblin-crawl runs on Node, so a machine with no node on PATH has no crawler to start")
	}
	home := filepath.Join(t.TempDir(), "a home")
	if _, err := Setup(context.Background(), home, source("v1"), &npm{shouldInstall: true}); err != nil {
		t.Fatal(err)
	}
	system := filepath.Join(os.Getenv("SystemRoot"), "System32")
	want := []string{"https://site.test/a page", "--out", "my crawl"}
	// Each shell is handed its command line as written: Go would quote an
	// argument cmd reads with backslashes, which cmd does not take.
	shells := []struct {
		name    string
		program string
		line    string
	}{
		{"cmd", filepath.Join(system, "cmd.exe"), `cmd.exe /d /c goblin-crawl "https://site.test/a page" --out "my crawl"`},
		{"PowerShell", filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), `powershell.exe -NoProfile -NonInteractive -Command "goblin-crawl 'https://site.test/a page' --out 'my crawl'; exit $LASTEXITCODE"`},
		{"Git Bash", gitBashShell(), `sh.exe -c "goblin-crawl 'https://site.test/a page' --out 'my crawl'"`},
	}

	for _, shell := range shells {
		t.Run(shell.name, func(t *testing.T) {
			if shell.program == "" {
				t.Skip("Git Bash's sh.exe is not beside git on this machine")
			}
			command := execx.Command(shell.program)
			command.SysProcAttr.CmdLine = shell.line
			command.Env = append(os.Environ(), "PATH="+BinDir(home)+string(os.PathListSeparator)+os.Getenv("PATH"))

			// Act
			output, err := command.Output()

			// Assert
			var got []string
			if err != nil || json.Unmarshal(output, &got) != nil || !slices.Equal(got, want) {
				t.Errorf("goblin-crawl from %s printed %q (%v), want the arguments %q", shell.name, output, err, want)
			}
		})
	}
}

// gitBashShell is Git Bash's sh.exe: the first sh.exe in a bin folder above
// the git on PATH, or empty where there is none. bash on PATH can be WSL's
// launcher, which is not Git Bash.
func gitBashShell() string {
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	for dir := filepath.Dir(git); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		shell := filepath.Join(dir, "bin", "sh.exe")
		if info, err := os.Stat(shell); err == nil && !info.IsDir() {
			return shell
		}
	}
	return ""
}
