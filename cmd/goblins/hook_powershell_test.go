package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/install"
)

// shellHook is the pre-tool hook the CEO's settings run for a call of tool,
// as install.Hooks registers it.
func shellHook(t *testing.T, root, tool string) string {
	t.Helper()
	for _, hook := range install.Hooks(root) {
		if hook.Event == "PreToolUse" && slices.Contains(strings.Split(hook.Matcher, "|"), tool) {
			return hook.Name
		}
	}
	t.Fatalf("no CEO hook guards a %s call", tool)
	return ""
}

// On 2026-10-10 the proof of the CEO's hooks with real Claude Code asked its
// stand-in CEO to run cd C:\. Claude Code on Windows took its PowerShell tool
// for it, the shell guards were registered for the Bash tool alone, and
// nothing refused the call. Every guard that holds for a Bash call holds for
// the same command through the PowerShell tool, written as PowerShell writes
// it, and what a Bash call may run a PowerShell call may run too.
func TestEveryGuardOfABashCallHoldsForTheSameCommandThroughThePowerShellTool(t *testing.T) {
	// Arrange
	root := newCEOHome(t)
	t.Setenv("PATH", t.TempDir())
	cases := []struct{ bash, powershell, code string }{
		{`cd C:\other`, `cd C:\other`, "cwd-relocation"},
		{"pushd ..", "Push-Location ..", "cwd-relocation"},
		{"go test ./... ; popd", "go test ./... ; Pop-Location", "cwd-relocation"},
		{"cd sub && go test ./...", "Set-Location sub; go test ./...", "cwd-relocation"},
		{"go test ./...\ncd sub", "go test ./...\ncd sub", "cwd-relocation"},
		{"sleep 5 & cd sub", "Start-Sleep 5; sl sub", "cwd-relocation"},
		{"goblins watch", "goblins watch", "watcher-direct"},
		{`C:\dev\code-goblins\goblins.exe watch`, `& "C:\dev\code-goblins\bin\goblins.exe" watch`, "watcher-direct"},
		{"goblins watch &", "Start-Process goblins -ArgumentList watch", "watcher-background"},
		{"goblins watch | tee log", "goblins watch | Tee-Object log", "watcher-pipeline"},
		{"goblins watch > out.txt", "goblins watch > out.txt", "watcher-redirection"},
		{"cd x && goblins watch", "goblins status; goblins watch", "watcher-bundled"},
		{"$(goblins watch)", "Invoke-Expression 'goblins watch'", "watcher-nested"},
		{"pkill -f goblins watch", "Get-CimInstance Win32_Process | Where-Object CommandLine -like '*goblins*watch*' | Stop-Process", "broad-watcher-kill"},
		{`echo $'goblins watch'`, "Write-Output @'\ngoblins watch\n'@", "unclassifiable-protected-command"},
		{"echo cd", "Write-Output cd", ""},
		{`git -C C:\x status`, `git -C C:\x status`, ""},
		{`git commit -m "wip; cd later"`, `git commit -m "wip; cd later"`, ""},
		{"goblins watchdog-config", "goblins watchdog-config", ""},
		{"git log --oneline", "git log --oneline", ""},
	}
	for _, c := range cases {
		for _, call := range []struct{ tool, command string }{{"Bash", c.bash}, {"PowerShell", c.powershell}} {
			var stdout, stderr bytes.Buffer
			payload := strings.NewReader(hookPayload(t, "s", "", call.tool, call.command))

			// Act
			exit := runHook(shellHook(t, root, call.tool), payload, &stdout, &stderr)

			// Assert
			isRefused := exit == 2 && strings.Contains(stderr.String(), "["+c.code+"]")
			isAllowed := exit == 0 && stderr.Len() == 0
			if c.code != "" && !isRefused || c.code == "" && !isAllowed {
				t.Errorf("%s %q: exit %d, stderr %q; want code %q", call.tool, call.command, exit, stderr.String(), c.code)
			}
		}
	}
}
