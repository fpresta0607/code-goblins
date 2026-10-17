package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/codexguard"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// While Codex's included allowance is used up, a goblin on Claude Code that
// starts codex exec in its terminal, from cmd, PowerShell or Git Bash, meets
// its armed codex guard: the command prints one line that says why, exits 1
// and starts nothing, and the CFO is woken once, with the goblin and the
// command. On 2026-10-16 two such probes billed 380.5 Codex credits. The
// terminal's PATH holds no real Codex: the stand-in next on it records any
// start, so a guard that let codex through fails the test and spends nothing.
func TestAGoblinOnClaudeCodeStartingCodexExecWhileCodexsWeekIsOutIsRefused(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	until := time.Now().UTC().Add(5 * 24 * time.Hour).Truncate(time.Second)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := codexguard.Arm(stateDir, self, "g1", until); err != nil {
		t.Fatal(err)
	}
	standIn := t.TempDir()
	started := filepath.Join(t.TempDir(), "codex-started")
	for name, script := range map[string]string{
		"codex.cmd": "@echo started> \"" + started + "\"\r\n",
		"codex":     "#!/bin/sh\necho started > '" + filepath.ToSlash(started) + "'\n",
	} {
		if err := os.WriteFile(filepath.Join(standIn, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	system := filepath.Join(os.Getenv("SystemRoot"), "System32")
	path := strings.Join([]string{codexguard.Dir(stateDir, "g1"), standIn, system}, string(os.PathListSeparator))
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "PATH") && !strings.EqualFold(name, codexguard.Variable) {
			env = append(env, entry)
		}
	}
	env = append(env, "PATH="+path, "CFO_TASK_ID=g1", harness.RoleVariable+"="+harness.RoleGoblin)
	// Each shell is handed its command line as written: Go would quote an
	// argument cmd reads with backslashes, which cmd does not take.
	shells := []struct {
		name    string
		program string
		line    string
	}{
		{"cmd", filepath.Join(system, "cmd.exe"), `cmd.exe /c codex exec "probe the harness"`},
		{"PowerShell", filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), `powershell.exe -NoProfile -NonInteractive -Command "codex exec 'probe the harness'; exit $LASTEXITCODE"`},
		{"Git Bash", gitBashShell(), `sh.exe -c "codex exec 'probe the harness'"`},
	}

	for _, shell := range shells {
		t.Run(shell.name, func(t *testing.T) {
			if shell.program == "" {
				t.Skip("Git Bash's sh.exe is not beside git on this machine")
			}
			command := execx.Command(shell.program)
			command.SysProcAttr.CmdLine = shell.line
			command.Env = env
			var stdout, stderr strings.Builder
			command.Stdout, command.Stderr = &stdout, &stderr

			// Act
			err := command.Run()

			// Assert
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Errorf("codex exec ended with %v, want exit 1", err)
			}
			lines := strings.Split(strings.TrimSpace(stdout.String()+stderr.String()), "\n")
			if len(lines) != 1 || !strings.HasPrefix(lines[0], "codex: refused") || !strings.Contains(lines[0], until.Format(time.RFC3339)) || !strings.Contains(lines[0], "task g1") {
				t.Errorf("codex exec printed %q, want one line that refuses it until %s for task g1", lines, until.Format(time.RFC3339))
			}
			if _, err := os.Stat(started); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the codex after the guard on PATH started (%v)", err)
			}
		})
	}

	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "allowance" || records[0].Key != "g1" || !strings.Contains(records[0].Detail, "goblin g1 started `codex exec \"probe the harness\"`") {
		t.Errorf("wakes = %+v, want one allowance wake naming goblin g1 and its codex exec", records)
	}
}

// gitBashShell is Git Bash's sh.exe, which a Claude Code goblin's Bash tool
// runs: the first sh.exe in a bin folder above the git on PATH, which sits in
// its Git install's cmd, bin or mingw64/bin folder, or empty where there is
// none. bash on PATH can be WSL's launcher, which is not Git Bash.
func gitBashShell() string {
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	for dir := filepath.Dir(git); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if shell := filepath.Join(dir, "bin", "sh.exe"); fileExists(shell) {
			return shell
		}
	}
	return ""
}

// fileExists reports whether path names a file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// A codex guard started without the state folder, the task or the time the
// allowance renews, as no armed guard starts it, refuses all the same and
// names its usage.
func TestCodexGuardWithoutItsArgumentsStillStartsNothing(t *testing.T) {
	for name, args := range map[string][]string{
		"no arguments":    nil,
		"no state":        {"--task", "g1", "--until", "2026-10-22T12:00:13Z", "--", "exec"},
		"no renewal":      {"--state", t.TempDir(), "--task", "g1", "--", "exec"},
		"a bad renewal":   {"--state", t.TempDir(), "--task", "g1", "--until", "Thursday", "--", "exec"},
		"an unknown flag": {"--yolo"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var stderr strings.Builder

			// Act
			code := runCodexGuard(args, &stderr, time.Now())

			// Assert
			if code != 2 || !strings.Contains(stderr.String(), "usage: cfo codex-guard") {
				t.Errorf("cfo codex-guard %q = %d, %q, want 2 and its usage", args, code, stderr.String())
			}
		})
	}
}
