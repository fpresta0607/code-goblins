package supervisor

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// Run launches exactly the shell an item names: never another one when it is
// missing, and never the WSL bash.exe under SystemRoot for Git Bash.
func TestRunShellPathFindsExactlyTheNamedShell(t *testing.T) {
	const powershell = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	for _, test := range []struct {
		name, shell string
		onPath      map[string]string
		files       []string
		want, fails string
	}{
		{name: "Windows PowerShell", shell: "powershell", files: []string{powershell}, want: powershell},
		{name: "Windows PowerShell missing", shell: "powershell", fails: "Windows PowerShell 5.1 is not installed"},
		{name: "PowerShell 7", shell: "pwsh", onPath: map[string]string{"pwsh": `C:\Program Files\PowerShell\7\pwsh.exe`}, want: `C:\Program Files\PowerShell\7\pwsh.exe`},
		{name: "PowerShell 7 missing never falls back", shell: "pwsh", files: []string{powershell}, fails: "PowerShell 7 (pwsh) is not on PATH"},
		{name: "Git Bash beside git", shell: "bash", onPath: map[string]string{"git": `C:\Program Files\Git\cmd\git.exe`}, files: []string{`C:\Program Files\Git\bin\bash.exe`, `C:\Windows\System32\bash.exe`}, want: `C:\Program Files\Git\bin\bash.exe`},
		{name: "Git Bash beside mingw64 git", shell: "bash", onPath: map[string]string{"git": `C:\Program Files\Git\mingw64\bin\git.exe`}, files: []string{`C:\Program Files\Git\bin\bash.exe`}, want: `C:\Program Files\Git\bin\bash.exe`},
		{name: "no git never uses WSL bash", shell: "bash", onPath: map[string]string{"bash": `C:\Windows\System32\bash.exe`}, files: []string{`C:\Windows\System32\bash.exe`}, fails: "git is not on PATH"},
		{name: "a bash under SystemRoot is never Git Bash", shell: "bash", onPath: map[string]string{"git": `C:\Windows\cmd\git.exe`}, files: []string{`C:\Windows\bin\bash.exe`}, fails: "not installed beside"},
		{name: "Git without its bash", shell: "bash", onPath: map[string]string{"git": `C:\Program Files\Git\cmd\git.exe`}, fails: "not installed beside"},
		{name: "unknown shell", shell: "cmd", fails: "unknown shell"},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookPath := func(name string) (string, error) {
				if path, ok := test.onPath[name]; ok {
					return path, nil
				}
				return "", errors.New("not found")
			}
			exists := func(path string) bool { return slices.Contains(test.files, path) }
			got, err := runShellPath(test.shell, lookPath, exists, `C:\Windows`)
			if test.fails != "" {
				if err == nil || !strings.Contains(err.Error(), test.fails) {
					t.Fatalf("runShellPath = %q, %v; want an error naming %q", got, err, test.fails)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("runShellPath = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}
