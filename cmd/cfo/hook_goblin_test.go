package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// goblinHookHome is a scratch home whose goblin pd-keys runs the goblin
// hooks, as its terminal names them, in a worktree of its own that holds
// nothing yet.
func goblinHookHome(t *testing.T) string {
	t.Helper()
	root := useScratchHome(t)
	t.Setenv(harness.RoleVariable, harness.RoleGoblin)
	t.Setenv("CFO_TASK_ID", "pd-keys")
	t.Setenv("CLAUDE_PROJECT_DIR", t.TempDir())
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return stateDir
}

func shellCall(t *testing.T, command string) *strings.Reader {
	t.Helper()
	data, err := json.Marshal(map[string]any{"session_id": "s1", "tool_name": "Bash", "tool_input": map[string]string{"command": command}})
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(data))
}

func TestGoblinSealRefusesBrowserCommandsOnlyWhileItsTaskIsSealed(t *testing.T) {
	for _, test := range []struct {
		name      string
		arrange   func(t *testing.T, stateDir string)
		command   string
		isRefused bool
	}{
		{"a snapshot while sealed", sealTask("pd-keys", time.Hour), "chrome-devtools-axi snapshot", true},
		{"any spelling of the tool while sealed", sealTask("pd-keys", time.Hour), "npx Chrome-DevTools-AXI eval 'document.body.innerText'", true},
		{"another command while sealed", sealTask("pd-keys", time.Hour), "git status", false},
		{"a snapshot with no seal", func(*testing.T, string) {}, "chrome-devtools-axi snapshot", false},
		{"a snapshot after the seal ran out", sealTask("pd-keys", -time.Second), "chrome-devtools-axi snapshot", false},
		{"a snapshot while another task is sealed", sealTask("pd-other", time.Hour), "chrome-devtools-axi snapshot", false},
		{"a snapshot under a damaged seal", func(t *testing.T, stateDir string) {
			sealTask("pd-keys", time.Hour)(t, stateDir)
			if err := os.WriteFile(filepath.Join(stateDir, "browser-seals", "pd-keys.json"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "chrome-devtools-axi snapshot", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := goblinHookHome(t)
			test.arrange(t, stateDir)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runHook("goblin-seal", shellCall(t, test.command), &stdout, &stderr)

			// Assert
			if isRefused := exit == 2 && strings.Contains(stderr.String(), `"permissionDecision":"deny"`) && strings.Contains(stderr.String(), "key fetch is reading"); isRefused != test.isRefused {
				t.Fatalf("exit = %d, stderr = %q, want refused %t", exit, stderr.String(), test.isRefused)
			}
		})
	}
}

// The goblin hooks run on a goblin's tool calls all day, so a call that is
// no browser command, or whose output holds no key, costs them no file read
// and no look at the machine's processes.
func TestGoblinHooksReadNothingForAnOrdinaryCall(t *testing.T) {
	// Arrange
	goblinHookHome(t)
	opened, listed := fsx.Opens(), proc.Lists()
	var stdout, stderr bytes.Buffer

	// Act
	seal := runHook("goblin-seal", shellCall(t, "git status"), &stdout, &stderr)
	alarm := runHook("goblin-key-alarm", toolOutput(t, "toolu_1", "commit 9f67ba752f1e637b1d58a0e13e1c07a692648c2c"), &stdout, &stderr)

	// Assert
	if seal != 0 || alarm != 0 {
		t.Fatalf("seal = %d, alarm = %d, want both calls left as they were", seal, alarm)
	}
	if opened, listed = fsx.Opens()-opened, proc.Lists()-listed; opened != 0 || listed != 0 {
		t.Fatalf("the hooks opened %d file(s) and listed processes %d time(s), want neither", opened, listed)
	}
}

// On 2026-10-21 a hijacked release's .pth file ran a stealer at every Python
// start of the goblins that installed it. A fresh install that leaves such a
// file stops the goblin before its next shell command, which is the first
// that could start Python, and every command after it until the file is
// gone. It wakes the CFO with the file and its package once while that wake
// waits for him, and again once he has acknowledged it and the file is
// still there.
func TestGoblinSealStopsAGoblinBeforePythonRunsAPlantedStartHook(t *testing.T) {
	// Arrange
	stateDir := goblinHookHome(t)
	planted := plantStartHook(t, os.Getenv("CLAUDE_PROJECT_DIR"), "litellm_init.pth,sha256=AAAA,90\n")
	var stdout, stderr bytes.Buffer

	// Act
	exits := []int{
		runHook("goblin-seal", shellCall(t, `.venv\Scripts\python.exe -m pytest`), &stdout, &stderr),
		runHook("goblin-seal", shellCall(t, "git status"), &stdout, &stderr),
	}
	first, err := wake.Pending(stateDir)
	if err != nil || len(first) == 0 {
		t.Fatalf("wakes = %+v, %v, want the CFO woken", first, err)
	}
	if err := wake.AckThrough(stateDir, first[len(first)-1].Seq); err != nil {
		t.Fatal(err)
	}
	exits = append(exits, runHook("goblin-seal", shellCall(t, "uv sync --locked"), &stdout, &stderr))
	again, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(planted); err != nil {
		t.Fatal(err)
	}
	after := runHook("goblin-seal", shellCall(t, `.venv\Scripts\python.exe -m pytest`), &stdout, &stderr)

	// Assert
	for index, exit := range exits {
		if exit != 2 {
			t.Fatalf("command %d exit = %d, want it refused before it ran", index+1, exit)
		}
	}
	refusals := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	for _, refusal := range refusals {
		if !strings.Contains(refusal, `"permissionDecision":"deny"`) || !strings.Contains(refusal, "Stopped before Python could start") || !strings.Contains(refusal, "litellm_init.pth, from litellm-1.107.0,") {
			t.Fatalf("refusal = %q, want the planted file and its package named", refusal)
		}
	}
	if after != 0 || len(refusals) != 3 {
		t.Fatalf("exit once the file is gone = %d, refusals = %d, want the command let through", after, len(refusals))
	}
	for name, wakes := range map[string][]wake.Record{"before he acknowledged it": first, "after he acknowledged it": again} {
		if len(wakes) != 1 || wakes[0].Kind != "review" || wakes[0].Key != "pd-keys" || !strings.Contains(wakes[0].Detail, "pd-keys was stopped before its next shell command: "+planted+", from litellm-1.107.0,") || strings.Contains(wakes[0].Detail, "subprocess") {
			t.Fatalf("wakes %s = %+v, want one naming the file and its package, never its code", name, wakes)
		}
	}
}

// sessionShellCall is the payload a Codex goblin's seal, or a pi goblin's,
// gets before tool runs command: the folder its session started in, cwd,
// and the call, as Codex writes it and as the pi extension does.
func sessionShellCall(t *testing.T, cwd, tool, command string) *strings.Reader {
	t.Helper()
	data, err := json.Marshal(map[string]any{"session_id": "s1", "cwd": cwd, "hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": map[string]string{"command": command}, "tool_use_id": "call_1", "turn_id": "turn_1"})
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(data))
}

// sealRefusals are the refusals a Codex or pi seal wrote on stdout, one
// reason a line.
func sealRefusals(t *testing.T, stdout string) []string {
	t.Helper()
	var reasons []string
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var answer struct {
			HookSpecificOutput struct {
				HookEventName            string `json:"hookEventName"`
				PermissionDecision       string `json:"permissionDecision"`
				PermissionDecisionReason string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(line), &answer); err != nil || answer.HookSpecificOutput.HookEventName != "PreToolUse" || answer.HookSpecificOutput.PermissionDecision != "deny" {
			t.Fatalf("stdout line %q is no PreToolUse refusal (%v)", line, err)
		}
		reasons = append(reasons, answer.HookSpecificOutput.PermissionDecisionReason)
	}
	return reasons
}

// A Codex or pi goblin is stopped as a Claude Code goblin is, before its
// next shell command and every one after it until the planted file is gone,
// with the CFO woken with the file and its package once while that wake
// waits for him and again once he has acknowledged it. Its seal looks in the
// folder its session names, never CLAUDE_PROJECT_DIR, and answers with exit
// 0 and its refusal on stdout, the one answer Codex's shell passes on.
func TestCodexAndPiGoblinSealsStopAGoblinBeforePythonRunsAPlantedStartHook(t *testing.T) {
	for _, test := range []struct{ hook, tool string }{
		{"goblin-seal-codex", "Bash"},
		{"goblin-seal-pi", "bash"},
	} {
		t.Run(test.hook, func(t *testing.T) {
			// Arrange
			stateDir := goblinHookHome(t)
			worktree := t.TempDir()
			planted := plantStartHook(t, worktree, "litellm_init.pth,sha256=AAAA,90\n")
			var stdout, stderr, afterOutput bytes.Buffer
			call := func(command string, output *bytes.Buffer) int {
				return runHook(test.hook, sessionShellCall(t, worktree, test.tool, command), output, &stderr)
			}

			// Act
			exits := []int{call(`.venv\Scripts\python.exe -m pytest`, &stdout), call("git status", &stdout)}
			first, err := wake.Pending(stateDir)
			if err != nil || len(first) == 0 {
				t.Fatalf("wakes = %+v, %v, want the CFO woken", first, err)
			}
			if err := wake.AckThrough(stateDir, first[len(first)-1].Seq); err != nil {
				t.Fatal(err)
			}
			exits = append(exits, call("uv sync --locked", &stdout))
			again, err := wake.Pending(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(planted); err != nil {
				t.Fatal(err)
			}
			exits = append(exits, call(`.venv\Scripts\python.exe -m pytest`, &afterOutput))

			// Assert
			for index, exit := range exits {
				if exit != 0 {
					t.Fatalf("command %d exit = %d, want 0, the only exit Codex's shell passes on", index+1, exit)
				}
			}
			if stderr.Len() != 0 || afterOutput.Len() != 0 {
				t.Fatalf("stderr = %q, output once the file is gone = %q, want the refusals on stdout and the last command let through", stderr.String(), afterOutput.String())
			}
			refusals := sealRefusals(t, stdout.String())
			for _, refusal := range refusals {
				if !strings.Contains(refusal, "Stopped before Python could start") || !strings.Contains(refusal, "litellm_init.pth, from litellm-1.107.0,") {
					t.Fatalf("refusal = %q, want the planted file and its package named", refusal)
				}
			}
			if len(refusals) != 3 {
				t.Fatalf("refusals = %q, want one for each command while the file was there", refusals)
			}
			for name, wakes := range map[string][]wake.Record{"before he acknowledged it": first, "after he acknowledged it": again} {
				if len(wakes) != 1 || wakes[0].Kind != "review" || wakes[0].Key != "pd-keys" || !strings.Contains(wakes[0].Detail, "pd-keys was stopped before its next shell command: "+planted+", from litellm-1.107.0,") || strings.Contains(wakes[0].Detail, "subprocess") {
					t.Fatalf("wakes %s = %+v, want one naming the file and its package, never its code", name, wakes)
				}
			}
		})
	}
}

// Claude Code's Monitor tool runs a command in the background as Bash runs
// one, such as a goblin's test run it watches, so a planted start hook
// stops it before it runs as it stops a shell command.
func TestGoblinSealStopsAMonitorCommandBeforePythonRuns(t *testing.T) {
	// Arrange
	goblinHookHome(t)
	plantStartHook(t, os.Getenv("CLAUDE_PROJECT_DIR"), "litellm_init.pth,sha256=AAAA,90\n")
	data, err := json.Marshal(map[string]any{"session_id": "s1", "tool_name": "Monitor", "tool_input": map[string]string{"command": "uv run pytest -q", "description": "the test run"}})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runHook("goblin-seal", strings.NewReader(string(data)), &stdout, &stderr)

	// Assert
	if exit != 2 || !strings.Contains(stderr.String(), `"permissionDecision":"deny"`) || !strings.Contains(stderr.String(), "litellm_init.pth, from litellm-1.107.0,") {
		t.Fatalf("exit = %d, stderr = %q, want the Monitor command refused before it ran", exit, stderr.String())
	}
}

// A goblin told the CFO knows ends its turn and waits for him, so when no
// wake could be queued it is asked to run the command again, which tries
// once more, rather than to wait for a CFO who was never told.
func TestGoblinSealAsksForTheCommandAgainWhenTheCFOCouldNotBeTold(t *testing.T) {
	// Arrange
	stateDir := goblinHookHome(t)
	t.Setenv("CFO_TASK_ID", "")
	plantStartHook(t, os.Getenv("CLAUDE_PROJECT_DIR"), "litellm_init.pth,sha256=AAAA,90\n")
	var stdout, stderr bytes.Buffer

	// Act
	exit := runHook("goblin-seal", shellCall(t, `.venv\Scripts\python.exe -m pytest`), &stdout, &stderr)

	// Assert
	if exit != 2 || !strings.Contains(stderr.String(), "The CFO could not be told of it yet, so run the command again in a minute") || strings.Contains(stderr.String(), "has been told") {
		t.Fatalf("exit = %d, stderr = %q, want the command refused and asked for again", exit, stderr.String())
	}
	if pending, err := wake.Pending(stateDir); err != nil || len(pending) != 0 {
		t.Fatalf("wakes = %+v, %v, want none", pending, err)
	}
}

// An install writes a file before the RECORD that names its package, so a
// command run beside an install that finds a start hook no package names
// yet waits with no wake, and the CFO is told only of one still unnamed
// once the install has ended.
func TestGoblinSealWaitsOutAnInstallBeforeWakingTheCFO(t *testing.T) {
	// Arrange
	stateDir := goblinHookHome(t)
	plantStartHook(t, os.Getenv("CLAUDE_PROJECT_DIR"), "")
	var stdout, stderr bytes.Buffer

	// Act
	exit := runHook("goblin-seal", shellCall(t, "Get-Content install.log -Tail 5"), &stdout, &stderr)

	// Assert
	if exit != 2 || !strings.Contains(stderr.String(), "no installed package names it yet, as while an install is still writing") {
		t.Fatalf("exit = %d, stderr = %q, want the command to wait out the install", exit, stderr.String())
	}
	if pending, err := wake.Pending(stateDir); err != nil || len(pending) != 0 {
		t.Fatalf("wakes = %+v, %v, want none while an install may still name it", pending, err)
	}
}

// plantStartHook makes the environment a fresh install of the hijacked
// release leaves in worktree, its RECORD as record says, and returns the
// planted .pth file.
func plantStartHook(t *testing.T, worktree, record string) string {
	t.Helper()
	site := filepath.Join(worktree, ".venv", "Lib", "site-packages")
	planted := filepath.Join(site, "litellm_init.pth")
	files := map[string]string{
		filepath.Join(site, "..", "..", "pyvenv.cfg"): "uv = 0.9.30\nversion_info = 3.12.10\n",
		planted: "import os, subprocess; subprocess.Popen(['python', 'init.py'])\n",
	}
	if record != "" {
		files[filepath.Join(site, "litellm-1.107.0.dist-info", "RECORD")] = record
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return planted
}

func sealTask(task string, lasting time.Duration) func(*testing.T, string) {
	return func(t *testing.T, stateDir string) {
		t.Helper()
		if err := browserlogin.Seal(stateDir, task, time.Now().Add(lasting)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGoblinHooksDoNothingOutsideAGoblin(t *testing.T) {
	// Arrange
	stateDir := goblinHookHome(t)
	t.Setenv(harness.RoleVariable, "")
	sealTask("pd-keys", time.Hour)(t, stateDir)
	var stdout, stderr bytes.Buffer

	// Act
	seal := runHook("goblin-seal", shellCall(t, "chrome-devtools-axi snapshot"), &stdout, &stderr)
	alarm := runHook("goblin-key-alarm", toolOutput(t, "toolu_1", "STRIPE_TEST_SECRET_KEY="+keyShaped("sk"+"_test_")), &stdout, &stderr)

	// Assert
	if seal != 0 || alarm != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("seal = %d, alarm = %d, output %q %q, want nothing outside a goblin", seal, alarm, stdout.String(), stderr.String())
	}
	if pending, err := wake.Pending(stateDir); err != nil || len(pending) != 0 {
		t.Fatalf("wakes = %+v, %v, want none", pending, err)
	}
}

// keyShaped is a value of a secret key's shape, made at run time so no
// source file holds one.
func keyShaped(prefix string) string {
	return prefix + "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
}

func toolOutput(t *testing.T, call, output string) *strings.Reader {
	t.Helper()
	data, err := json.Marshal(map[string]any{"session_id": "s1", "tool_name": "Read", "tool_use_id": call, "tool_response": map[string]any{"file": map[string]string{"content": output}}})
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(data))
}

func TestGoblinKeyAlarmWakesTheCFOOnceACallWithTheKindAndNeverTheKey(t *testing.T) {
	// Arrange
	stateDir := goblinHookHome(t)
	key := keyShaped("sk" + "_test_")
	var stdout, stderr bytes.Buffer

	// Act
	exits := []int{
		runHook("goblin-key-alarm", toolOutput(t, "toolu_1", "STRIPE_TEST_SECRET_KEY="+key+"\nDATABASE_URL=postgres://db"), &stdout, &stderr),
		runHook("goblin-key-alarm", toolOutput(t, "toolu_1", "STRIPE_TEST_SECRET_KEY="+key), &stdout, &stderr),
		runHook("goblin-key-alarm", toolOutput(t, "toolu_2", "commit 9f67ba752f1e637b1d58a0e13e1c07a692648c2c"), &stdout, &stderr),
		runHook("goblin-key-alarm", toolOutput(t, "toolu_3", key+" and "+keyShaped("ghp"+"_")), &stdout, &stderr),
	}

	// Assert
	for index, exit := range exits {
		if exit != 0 {
			t.Fatalf("call %d exit = %d, want the tool call left as it was", index+1, exit)
		}
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output %q %q, want the goblin told nothing", stdout.String(), stderr.String())
	}
	pending, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("wakes = %+v, want one for each call that held a key", pending)
	}
	for index, want := range []string{"pd-keys's Read output held a Stripe secret key, so that key is in its transcript now", "held a Stripe secret key and a GitHub token"} {
		record := pending[index]
		if record.Kind != "review" || record.Key != "pd-keys" || !strings.Contains(record.Detail, want) || strings.Contains(record.Detail, key) || strings.Contains(record.Detail, "q7Lm2Xv9") {
			t.Fatalf("wake %d = %+v, want %q and no key", index+1, record, want)
		}
	}
}
