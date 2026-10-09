package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// repairHome is the command of run item repair-home-v0.5.3, 2026-10-07: its
// last line is one Windows PowerShell cannot parse, so his click ran none of
// it and the item failed.
const repairHome = "Set-Location 'C:\\dev\\code-goblins'\n" +
	"\"before: marker present = $(Test-Path 'C:\\dev\\code-goblins\\.cfo-home')\"\n" +
	"& 'C:\\dev\\code-goblins\\cfo.exe' install 2>&1 | Select-Object -Last 25\n" +
	"\"exit code: $LASTEXITCODE\"\n" +
	"\"after: marker present = $(Test-Path 'C:\\dev\\code-goblins\\.cfo-home')\"\n" +
	"$settings = Get-Content (Join-Path $env:USERPROFILE '.claude\\settings.json') -Raw | ConvertFrom-Json\n" +
	"\"Command Center allow rules now in your Claude Code settings: $(@($settings.permissions.allow | Where-Object { $_ -match '^(Bash|PowerShell)\\(cfo ' }).Count)\"\n"

// A command its own shell cannot parse would run nothing, so it is refused
// before it reaches the Command Center, with the line its shell names.
func TestARunCommandItsShellCannotParseIsRefused(t *testing.T) {
	// Act
	err := CheckRunCommand("powershell", repairHome)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "does not parse") || !strings.Contains(err.Error(), "line 7") {
		t.Fatalf("CheckRunCommand(repair-home-v0.5.3) = %v, want it refused naming line 7", err)
	}
}

func TestARunCommandItsShellParsesIsTaken(t *testing.T) {
	for shell, command := range map[string]string{
		"powershell": "Set-Location 'C:\\dev'\n\"exit code: $LASTEXITCODE\"\n",
		"bash":       "cd /c/dev && echo \"ready: $(date)\"\n",
	} {
		t.Run(shell, func(t *testing.T) {
			if shell == "bash" {
				if _, err := exec.LookPath("git"); err != nil {
					t.Skip("Git Bash is not installed here")
				}
			}

			// Act
			err := CheckRunCommand(shell, command)

			// Assert
			if err != nil {
				t.Errorf("CheckRunCommand(%s) = %v, want it taken", shell, err)
			}
		})
	}
}

func TestABashRunCommandItCannotParseIsRefused(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git Bash is not installed here")
	}

	// Act
	err := CheckRunCommand("bash", "echo start\nif [ -d /c/dev ]; then\n  echo there\n")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("CheckRunCommand(bash) = %v, want it refused", err)
	}
}

// cfo run-request checks the command before it asks the supervisor for
// anything: a command that cannot parse is never published.
func TestPublishRunRefusesACommandItsShellCannotParse(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	file := filepath.Join(dir, "repair-home.ps1")
	if err := os.WriteFile(file, []byte(repairHome), 0o600); err != nil {
		t.Fatal(err)
	}
	h := home.Home{Root: dir, State: filepath.Join(dir, "state")}

	// Act
	err := PublishRun(h, RunRequest{ID: "repair-home-v0.5.3", Title: "Finish the v0.5.3 install", Shell: "powershell", CommandFile: file})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("PublishRun = %v, want the command refused for its line 7 before the supervisor is asked", err)
	}
}
