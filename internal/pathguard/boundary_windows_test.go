package pathguard

import (
	"path/filepath"
	"strings"
	"testing"
)

func gitBash(path string) string {
	slashed := filepath.ToSlash(path)
	return "/" + strings.ToLower(slashed[:1]) + slashed[2:]
}

// On Windows a path is read as Git Bash and PowerShell read it: a drive as
// /c/, Git Bash's /tmp as the folder its TMP names, and a Windows variable
// with backslashes after it.
func TestToolRefusalReadsGitBashAndPowerShellSpellingsOnWindows(t *testing.T) {
	f := newFixture(t)
	refused := []struct {
		name, tool string
		input      map[string]string
		path       string
	}{
		{"cat an ssh key by its Git Bash path", "Bash", map[string]string{"command": "cat " + gitBash(f.sshKey)}, f.sshKey},
		{"read an ssh key by USERPROFILE", "PowerShell", map[string]string{"command": `Get-Content $env:USERPROFILE\.ssh\id_ed25519`}, f.sshKey},
		{"append outside by descriptor by its Git Bash path", "Bash", map[string]string{"command": "go test ./... 2>>" + gitBash(filepath.Join(f.outside, "log.txt"))}, filepath.Join(f.outside, "log.txt")},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			// Act
			refusal := f.boundary.ToolRefusal(c.tool, toolInput(t, c.input))

			// Assert
			if !strings.Contains(strings.ToLower(refusal), strings.ToLower(c.path)) {
				t.Fatalf("%s %v gave %q, want it refused naming %s", c.tool, c.input, refusal, c.path)
			}
		})
	}
	t.Run("write to Git Bash's tmp", func(t *testing.T) {
		// Act
		refusal := f.boundary.ToolRefusal("Bash", toolInput(t, map[string]string{"command": "echo hi > /tmp/note.txt"}))

		// Assert
		if refusal != "" {
			t.Fatalf("a write to Git Bash's /tmp, which TMP names as the scratch folder, was refused: %s", refusal)
		}
	})
}
