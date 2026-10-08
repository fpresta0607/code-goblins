package devdrive

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The scripts cannot be run here, since they make and attach disks, so
// Windows PowerShell 5.1, which runs them, parses each and must find nothing
// wrong.
func TestWindowsPowerShellParsesTheScripts(t *testing.T) {
	for name, script := range map[string]string{
		"create": CreateScript(`C:\DevDrives\CodeGoblins.vhdx`, "D:", 200),
		"attach": CreateScript(`C:\DevDrives\CodeGoblins.vhdx`, "E:", 0),
		"trust":  TrustScript("D:"),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name+".ps1")
			if err := os.WriteFile(path, append([]byte("\xef\xbb\xbf"), script...), 0o600); err != nil {
				t.Fatal(err)
			}
			check := "$errors = $null; [void][System.Management.Automation.Language.Parser]::ParseFile('" + path + "', [ref]$null, [ref]$errors); $errors | ForEach-Object { $_.Message }; exit $errors.Count"
			output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", check).CombinedOutput()
			if err != nil || strings.TrimSpace(string(output)) != "" {
				t.Errorf("Windows PowerShell finds the %s script wrong: %v\n%s\n%s", name, err, output, script)
			}
		})
	}
}
