package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/install"
)

// shellHook is the pre-tool hook the CFO's settings run for a call of tool,
// as install.Hooks registers it.
func shellHook(t *testing.T, root, tool string) string {
	t.Helper()
	for _, hook := range install.Hooks(root) {
		if hook.Event == "PreToolUse" && slices.Contains(strings.Split(hook.Matcher, "|"), tool) {
			return hook.Name
		}
	}
	t.Fatalf("no CFO hook guards a %s call", tool)
	return ""
}

// On 2026-10-10 the proof of the CFO's hooks with real Claude Code asked its
// stand-in CFO to run cd C:\. Claude Code on Windows took its PowerShell tool
// for it, the shell guards were registered for the Bash tool alone, and
// nothing refused the call. Every guard that holds for a Bash call holds for
// the same command through the PowerShell tool, written as PowerShell writes
// it, and what a Bash call may run a PowerShell call may run too.
func TestEveryGuardOfABashCallHoldsForTheSameCommandThroughThePowerShellTool(t *testing.T) {
	// Arrange
	root := newCFOHome(t)
	t.Setenv("PATH", t.TempDir())
	cases := []struct{ bash, powershell, code string }{
		{`cd C:\other`, `cd C:\other`, "cwd-relocation"},
		{"pushd ..", "Push-Location ..", "cwd-relocation"},
		{"go test ./... ; popd", "go test ./... ; Pop-Location", "cwd-relocation"},
		{"cd sub && go test ./...", "Set-Location sub; go test ./...", "cwd-relocation"},
		{"go test ./...\ncd sub", "go test ./...\ncd sub", "cwd-relocation"},
		{"sleep 5 & cd sub", "Start-Sleep 5; sl sub", "cwd-relocation"},
		{"cfo watch", "cfo watch", "watcher-direct"},
		{`C:\dev\code-goblins\cfo.exe watch`, `& "C:\dev\code-goblins\bin\cfo.exe" watch`, "watcher-direct"},
		{"cfo watch &", "Start-Process cfo -ArgumentList watch", "watcher-background"},
		{"cfo watch | tee log", "cfo watch | Tee-Object log", "watcher-pipeline"},
		{"cfo watch > out.txt", "cfo watch > out.txt", "watcher-redirection"},
		{"cd x && cfo watch", "cfo status; cfo watch", "watcher-bundled"},
		{"$(cfo watch)", "Invoke-Expression 'cfo watch'", "watcher-nested"},
		{"pkill -f cfo watch", "Get-CimInstance Win32_Process | Where-Object CommandLine -like '*cfo*watch*' | Stop-Process", "broad-watcher-kill"},
		{`echo $'cfo watch'`, "Write-Output @'\ncfo watch\n'@", "unclassifiable-protected-command"},
		{"echo cd", "Write-Output cd", ""},
		{`git -C C:\x status`, `git -C C:\x status`, ""},
		{`git commit -m "wip; cd later"`, `git commit -m "wip; cd later"`, ""},
		{"cfo watchdog-config", "cfo watchdog-config", ""},
		{"git log --oneline", "git log --oneline", ""},
	}
	for _, c := range cases {
		for _, call := range []struct{ tool, command string }{{"Bash", c.bash}, {"PowerShell", c.powershell}} {
			var stdout, stderr bytes.Buffer
			payload := strings.NewReader(`{"session_id":"s","tool_name":"` + call.tool + `","tool_input":{"command":` + quoteJSON(call.command) + `}}`)

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
