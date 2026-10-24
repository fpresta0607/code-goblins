package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// The tests below run this binary as the home's cfo.exe, as a harness runs
// it, so they prove what reaches the harness. What reaches the CFO is
// TestCodexAndPiGoblinSealsStopAGoblinBeforePythonRunsAPlantedStartHook's to
// prove: a test binary is refused the home it inherits, so the seal it runs
// as tells no one and asks for the command again.

// Codex runs a hook's command in the session's Windows PowerShell, which
// ends with 1 for every exit code but 0, so a Codex goblin's seal is proven
// as Codex runs it: the command its launch's PreToolUse override names, run
// by powershell.exe -NoProfile -Command with the payload on its stdin. In a
// worktree with a planted start hook it ends with 0 and a refusal Codex
// reads, naming the file and its package.
func TestCodexReadsItsGoblinSealsRefusalThroughItsShell(t *testing.T) {
	// Arrange
	stateDir := goblinHookHome(t)
	worktree := t.TempDir()
	plantStartHook(t, worktree, "litellm_init.pth,sha256=AAAA,90\n")
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args, err := nativehook.CodexGoblinArguments(program, os.Getenv("CFO_HOME"), stateDir)
	if err != nil {
		t.Fatal(err)
	}
	_, override, _ := strings.Cut(args[len(args)-1], "command='")
	command, _, _ := strings.Cut(override, "'")
	shell := exec.Command("powershell.exe", "-NoProfile", "-Command", command)
	shell.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	shell.Stdin = sessionShellCall(t, worktree, "Bash", "uv run pytest")
	var stdout, stderr bytes.Buffer
	shell.Stdout, shell.Stderr = &stdout, &stderr

	// Act
	err = shell.Run()

	// Assert
	if err != nil {
		t.Fatalf("%s ended with %v (stderr %q), want 0, the only exit Codex's shell passes on", command, err, stderr.String())
	}
	refusals := sealRefusals(t, stdout.String())
	if len(refusals) != 1 || !strings.Contains(refusals[0], "Stopped before Python could start") || !strings.Contains(refusals[0], "litellm_init.pth, from litellm-1.107.0,") {
		t.Fatalf("refusals = %q, want one naming the planted file and its package", refusals)
	}
}

// piToolCall is what a pi goblin's extension answers a tool call with:
// whether it blocks the call, and why.
type piToolCall struct {
	Block  bool   `json:"block"`
	Reason string `json:"reason"`
}

// A pi goblin's extension runs the goblin seal before each bash call, the
// one pi tool that runs a command, and blocks the call with the seal's
// reason while a planted start hook is there; another tool, or a bash call
// in a worktree with none, goes on. A seal that cannot run blocks the call
// too. The extension runs in Node as pi loads it, with a stand-in for pi's
// API.
func TestAPiGoblinsExtensionBlocksItsBashCallsWhileAStartHookIsThere(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("pi runs on Node, so a machine with no node on PATH runs no pi goblin")
	}
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		tool      string
		program   string
		isPlanted bool
		want      string
	}{
		{"a bash call while a start hook is there", "bash", program, true, "litellm_init.pth, from litellm-1.107.0,"},
		{"a read call while a start hook is there", "read", program, true, ""},
		{"a bash call with no start hook", "bash", program, false, ""},
		{"a bash call whose seal cannot run", "bash", filepath.Join(t.TempDir(), "gone", "cfo-hook.exe"), false, "the check cfo makes before each command could not run"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			goblinHookHome(t)
			worktree := t.TempDir()
			if test.isPlanted {
				plantStartHook(t, worktree, "litellm_init.pth,sha256=AAAA,90\n")
			}
			dir := t.TempDir()
			driver := filepath.Join(dir, "driver.mjs")
			files := map[string]string{
				filepath.Join(dir, "seal.mjs"): install.GoblinPiExtension(test.program),
				driver: `import seal from "./seal.mjs";
const handlers = {};
seal({on: (name, handler) => { handlers[name] = handler; }});
const [tool, command, cwd] = process.argv.slice(2);
const answer = await handlers.tool_call({toolName: tool, input: {command}}, {cwd});
process.stdout.write(JSON.stringify(answer ?? null));
`,
			}
			for path, content := range files {
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			run := exec.Command(node, driver, test.tool, `.venv\Scripts\python.exe -m pytest`, worktree)
			run.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
			var stderr bytes.Buffer
			run.Stderr = &stderr

			// Act
			output, err := run.Output()

			// Assert
			if err != nil {
				t.Fatalf("node ended with %v (stderr %q)", err, stderr.String())
			}
			var answer *piToolCall
			if err := json.Unmarshal(output, &answer); err != nil {
				t.Fatalf("the extension answered %q: %v", output, err)
			}
			switch {
			case test.want == "" && answer != nil:
				t.Fatalf("the extension answered %+v, want the call to go on", *answer)
			case test.want != "" && (answer == nil || !answer.Block || !strings.Contains(answer.Reason, test.want)):
				t.Fatalf("the extension answered %+v, want the call blocked with %q", answer, test.want)
			}
		})
	}
}
