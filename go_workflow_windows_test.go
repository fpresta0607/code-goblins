package codegoblins

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflowStep is the run script of the step named step in job of the
// workflow at path.
func workflowStep(t *testing.T, path, job, step string) string {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(source, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range workflow.Jobs[job].Steps {
		if candidate.Name == step {
			return candidate.Run
		}
	}
	t.Fatalf("%s has no step %q in job %s", path, step, job)
	return ""
}

// The go workflow installs SQLite with Chocolatey, whose feed once answered
// 503 and failed the run: the install is tried again after a wait, and gives
// up after its third attempt.
func TestGoWorkflowRetriesTheSQLiteInstall(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("the step runs in PowerShell 7, which is not installed")
	}
	step := workflowStep(t, filepath.Join(".github", "workflows", "go.yml"), "test", "Ensure SQLite integration tests can run")
	for name, test := range map[string]struct {
		// failures is how many installs fail before one succeeds; -1 is
		// every one.
		failures     int
		wantAttempts int
		wantOK       bool
	}{
		"two failures, then the install": {2, 3, true},
		"a feed that stays down":         {-1, 3, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange: choco says each attempt, fails as many times as
			// asked, and then installs a sqlite3 beside itself.
			choco := "@echo choco %*\r\n"
			if test.failures < 0 {
				choco += "@exit /b 1\r\n"
			}
			for failure := 1; failure <= test.failures; failure++ {
				choco += fmt.Sprintf("@if not exist \"%%~dp0failure-%d\" (type nul>\"%%~dp0failure-%d\" & exit /b 1)\r\n", failure, failure)
			}
			choco += "@(echo @echo sqlite 3.46.0)>\"%~dp0sqlite3.cmd\"\r\n"
			// As GitHub Actions runs a pwsh step: stop on an error, and end
			// with the last native command's exit code. A wait is said, not
			// taken.
			script := filepath.Join(t.TempDir(), "step.ps1")
			body := "$ErrorActionPreference = 'stop'\r\n" +
				"function Start-Sleep { param([int]$Seconds) Write-Output \"wait $Seconds\" }\r\n" +
				step + "\r\nif ((Test-Path -LiteralPath variable:\\LASTEXITCODE)) { exit $LASTEXITCODE }\r\n"
			if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd, _, _ := strippedCommand(t, "", map[string]string{"choco": choco}, pwsh, "-NoProfile", "-NonInteractive", "-Command", ". '"+script+"'")

			// Act
			out, err := cmd.CombinedOutput()

			// Assert
			output := string(out)
			attempts, waits := strings.Count(output, "choco install sqlite --yes --no-progress"), strings.Count(output, "wait ")
			if attempts != test.wantAttempts {
				t.Errorf("choco install ran %d times, want %d:\n%s", attempts, test.wantAttempts, output)
			}
			if waits != attempts-1 {
				t.Errorf("the step waited %d times between %d attempts, want once between each two:\n%s", waits, attempts, output)
			}
			if test.wantOK && (err != nil || !strings.Contains(output, "sqlite 3.46.0")) {
				t.Errorf("step = %v, want SQLite installed and its version printed:\n%s", err, output)
			}
			if !test.wantOK && err == nil {
				t.Errorf("the step succeeded with no SQLite installed:\n%s", output)
			}
		})
	}
}
