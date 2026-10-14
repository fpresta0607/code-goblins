package cirunner

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Every script the install runs is one Windows PowerShell parses without
// error, so none fails first on the Overlord's machine.
func TestInstallScriptsParse(t *testing.T) {
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	const parse = "$errors = $null; [void][System.Management.Automation.Language.Parser]::ParseInput($env:CG_CI_SCRIPT, [ref]$null, [ref]$errors); $errors | ForEach-Object { $_.Message }; exit $errors.Count"
	for name, text := range map[string]string{"check": checkScript, "folder": folderScript, "disk": diskScript, "settings": settingsScript, "vm": vmScript, "checkpoint": checkpointScript, "service": serviceScript, "start": startScript, "machine": machineScript} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			command := execx.CommandContext(context.Background(), shell, "-NoProfile", "-NonInteractive", "-Command", parse)
			command.Env = append(os.Environ(), "CG_CI_SCRIPT=$ErrorActionPreference = 'Stop'; "+text)

			// Act
			output, err := command.CombinedOutput()

			// Assert
			if err != nil {
				t.Errorf("the %s script does not parse: %v: %s", name, err, strings.TrimSpace(string(output)))
			}
		})
	}
}

// ServiceSID names the account Windows would give any service, as sc.exe
// showsid does, which needs no service to exist.
func TestServiceSIDIsWhatWindowsSays(t *testing.T) {
	for _, name := range []string{"cg-ci-runner", "Code-Goblins-Test", "x"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			sc := filepath.Join(os.Getenv("SystemRoot"), "System32", "sc.exe")

			// Act
			output, err := execx.CommandContext(context.Background(), sc, "showsid", name).Output()

			// Assert
			said := regexp.MustCompile(`S-1-5-80(-\d+){5}`).FindString(string(output))
			if err != nil || said == "" || ServiceSID(name) != said {
				t.Errorf("ServiceSID(%q) = %q; sc.exe showsid says %q (%v)", name, ServiceSID(name), said, err)
			}
		})
	}
}
