package pathguard

import (
	"strings"
	"testing"
)

// A recursive find or grep whose search root is a drive root walks the whole
// drive: on 2026-11-11 a goblin's grep -rn with a stray / ran for 1 hour 50
// minutes after its shell ended and held 6.7 million handles.
func TestToolRefusalWarnsOfARecursiveSearchFromTheDriveRoot(t *testing.T) {
	cases := []struct {
		name, tool, command string
	}{
		{"grep -rn from /", "Bash", "grep -rn x /"},
		{"grep with a stray / beside a file", "Bash", "grep -rn -A8 pattern / internal/proc/job_windows.go"},
		{"grep -r from Git Bash's drive C", "Bash", "grep -r x /c/"},
		{"grep -R from Git Bash's drive D without a slash", "Bash", "grep -R x /d"},
		{"grep --recursive from a Windows drive root", "PowerShell", `grep --recursive x C:\`},
		{"egrep -nr after another command", "Bash", "cd src && egrep -nr 'a|b' /"},
		{"find from /", "Bash", "find / -name y"},
		{"find from a Windows drive root", "PowerShell", `find C:\ -name y`},
		{"find from a drive root behind its options", "Bash", "find -L / -type d -path '*/x'"},
		{"find in a Monitor command", "Monitor", "find /c/ -name y"},
		{"find from PowerShell's drive root", "PowerShell", `find \ -name y`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)

			// Act
			refusal := f.boundary.ToolRefusal(test.tool, toolInput(t, map[string]string{"command": test.command}))

			// Assert
			if !strings.Contains(refusal, "[drive-root-search]") || !strings.Contains(refusal, "whole drive") {
				t.Fatalf("ToolRefusal(%s, %q) = %q, want a drive-root search warning", test.tool, test.command, refusal)
			}
		})
	}
}

func TestToolRefusalLetsASearchOfAFolderRun(t *testing.T) {
	cases := []struct {
		name, command string
	}{
		{"grep -rn in a folder", "grep -rn x ./src"},
		{"find from the working folder", "find . -name y"},
		{"grep of / as its pattern", "grep -r / src"},
		{"grep -e naming / as its pattern", "grep -rn -e / src"},
		{"grep of the drive root that is not recursive", "grep -n x /"},
		{"find of the drive root one level deep", "find / -maxdepth 1 -name y"},
		{"a recursive grep of a folder under the drive root", "grep -rn x /c/dev/project"},
		{"a recursive listing of the drive root", "ls -R /"},
		{"grep -rn continued onto a second line", "grep -rn \"pattern\" \\\n internal/ cmd/"},
		{"find with escaped parentheses", `find . \( -name '*.go' -o -name '*.md' \) -print`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)

			// Act
			refusal := f.boundary.ToolRefusal("Bash", toolInput(t, map[string]string{"command": test.command}))

			// Assert
			if refusal != "" {
				t.Fatalf("ToolRefusal(Bash, %q) = %q, want it to run", test.command, refusal)
			}
		})
	}
}
