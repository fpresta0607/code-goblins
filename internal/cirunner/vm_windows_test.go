package cirunner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Every script HyperV runs is one Windows PowerShell parses without error, so
// none fails first on the machine that installs the service.
func TestHyperVScriptsParse(t *testing.T) {
	// Arrange
	commands := &recorder{result: execx.Result{Stdout: []byte("Off")}}
	eachHyperVCall(HyperV{Commands: commands})
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	const parse = "$errors = $null; [void][System.Management.Automation.Language.Parser]::ParseInput($env:CG_CI_SCRIPT, [ref]$null, [ref]$errors); $errors | ForEach-Object { $_.Message }; exit $errors.Count"

	for _, request := range commands.ran {
		text := script(request)
		command := execx.CommandContext(context.Background(), shell, "-NoProfile", "-NonInteractive", "-Command", parse)
		command.Env = append(os.Environ(), "CG_CI_SCRIPT="+text)

		// Act
		output, err := command.CombinedOutput()

		// Assert
		if err != nil {
			t.Errorf("%q does not parse: %v: %s", text, err, strings.TrimSpace(string(output)))
		}
	}
}
