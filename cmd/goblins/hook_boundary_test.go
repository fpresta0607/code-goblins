package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/sandbox"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// containTask makes the task stateDir holds for hookGoblin a sandbox task
// whose worktree's .git link, written now, is the one its spawn recorded.
func containTask(t *testing.T, stateDir string) state.TaskMeta {
	t.Helper()
	meta, err := state.ReadTaskMeta(stateDir, hookGoblin)
	if err != nil {
		t.Fatal(err)
	}
	link := "gitdir: " + filepath.ToSlash(filepath.Join(t.TempDir(), "worktrees", hookGoblin))
	if err := os.WriteFile(filepath.Join(meta.Worktree, ".git"), []byte(link+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta.SandboxImage, meta.SandboxGitLink = "node:22", link
	if meta.Scratch == "" {
		meta.Scratch = t.TempDir()
	}
	if meta.TaskTmp == "" {
		meta.TaskTmp = t.TempDir()
	}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

// Every harness's seal holds a sandbox task's goblin to running ceo, git and
// gh on the host, from a worktree whose .git link is still the one its spawn
// recorded: Claude Code's through the board, Codex's and pi's as commands.
func TestEverySealHoldsASandboxTasksShellToItsHostRule(t *testing.T) {
	for _, test := range []struct {
		name, command string
		commandOf     func(meta state.TaskMeta) string
		arrange       func(t *testing.T, meta state.TaskMeta)
		refusal       string
	}{
		{name: "host git from an intact worktree", command: "git status"},
		{name: "the project's tests on the host", command: "npm test", refusal: "only in its container"},
		{name: "the gate's checks on the host", command: "goblins gate test", refusal: "runs the project's own code on the host"},
		{
			name: "host git after the container replaced the .git link", command: "git status",
			arrange: func(t *testing.T, meta state.TaskMeta) {
				if err := os.WriteFile(filepath.Join(meta.Worktree, ".git"), []byte("gitdir: "+filepath.ToSlash(filepath.Join(meta.Worktree, "planted"))+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			refusal: "the link its spawn recorded",
		},
		{
			name: "host git after the container wrote a .gitmodules", command: "git status",
			arrange: func(t *testing.T, meta state.TaskMeta) {
				if err := os.WriteFile(filepath.Join(meta.Worktree, ".gitmodules"), []byte("[submodule \"sub\"]\n\tpath = sub\n\tignore = none\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			refusal: "cannot use submodules",
		},
		{name: "git the worktree holds, by a relative path", command: "./tools/git.exe status", refusal: "the container can write it"},
		{name: "git the worktree holds, by its path", commandOf: func(meta state.TaskMeta) string {
			return filepath.ToSlash(filepath.Join(meta.Worktree, "tools", "git.exe")) + " status"
		}, refusal: "the container can write it"},
		{name: "goblins the scratch folder holds", commandOf: func(meta state.TaskMeta) string {
			return filepath.ToSlash(filepath.Join(meta.Scratch, "goblins.exe")) + " notify x --working y"
		}, refusal: "the container can write it"},
		{name: "git by a path with backslashes, which Bash takes off", commandOf: func(meta state.TaskMeta) string {
			return filepath.Join(meta.Scratch, "git.exe") + " status"
		}, refusal: "a backslash outside quotes"},
		{
			name: "host git while the container made a repository deep in the worktree", command: "git status",
			arrange: func(t *testing.T, meta state.TaskMeta) {
				makeNestedGit(t, meta.Worktree)
			},
			refusal: "node_modules/pkg/.git",
		},
		{
			name: "removing that repository in the container", command: "goblins sandbox run -- rm -rf node_modules/pkg/.git",
			arrange: func(t *testing.T, meta state.TaskMeta) {
				makeNestedGit(t, meta.Worktree)
			},
		},
		{
			name: "host git while a container of the task runs", command: "git status",
			arrange: func(t *testing.T, meta state.TaskMeta) {
				holdSandboxRun(t, meta, "ceo-sandbox-x-running", true)
			},
			refusal: "ceo-sandbox-x-running of this task runs",
		},
		{
			name: "host git after a run ended without removing its container", command: "git status",
			arrange: func(t *testing.T, meta state.TaskMeta) {
				holdSandboxRun(t, meta, "ceo-sandbox-x-left", false)
			},
			refusal: "the next goblins sandbox run removes it",
		},
		{
			name: "a report while a container of the task runs", command: "goblins notify x --working y",
			arrange: func(t *testing.T, meta state.TaskMeta) {
				holdSandboxRun(t, meta, "ceo-sandbox-x-running", true)
			},
		},
	} {
		for _, entry := range []string{"board", "goblin-seal-codex", "goblin-seal-pi"} {
			t.Run(test.name+"/"+entry, func(t *testing.T) {
				// Arrange
				var ask func(command string) string
				var meta state.TaskMeta
				if entry == "board" {
					board, h, _ := goblinHookBoard(t)
					meta = containTask(t, h.State)
					ask = func(command string) string {
						response := postHook(board, http.MethodPost, install.GoblinSealPath, shellCallBody(t, command), goblinVariables())
						if response.Code != http.StatusOK {
							t.Fatalf("status = %d, body = %q, want 200", response.Code, response.Body.String())
						}
						if response.Body.Len() == 0 {
							return ""
						}
						var answer struct {
							Output struct {
								Reason string `json:"permissionDecisionReason"`
							} `json:"hookSpecificOutput"`
						}
						if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
							t.Fatal(err)
						}
						return answer.Output.Reason
					}
				} else {
					meta = containTask(t, goblinHookHome(t))
					tool := map[string]string{"goblin-seal-codex": "Bash", "goblin-seal-pi": "bash"}[entry]
					ask = func(command string) string {
						var stdout, stderr bytes.Buffer
						if exit := runHook(entry, sessionShellCall(t, meta.Worktree, tool, command), &stdout, &stderr); exit != 0 || stderr.Len() != 0 {
							t.Fatalf("exit = %d, stderr = %q, want 0 and the answer on stdout", exit, stderr.String())
						}
						if stdout.Len() == 0 {
							return ""
						}
						return strings.Join(sealRefusals(t, stdout.String()), "\n")
					}
				}
				if test.arrange != nil {
					test.arrange(t, meta)
				}

				command := test.command
				if test.commandOf != nil {
					command = test.commandOf(meta)
				}

				// Act
				refusal := ask(command)

				// Assert
				if test.refusal == "" && refusal != "" {
					t.Fatalf("refusal = %q, want %q to run", refusal, command)
				}
				if test.refusal != "" && (!strings.HasPrefix(refusal, "[sandbox] ") || !strings.Contains(refusal, test.refusal)) {
					t.Fatalf("refusal = %q, want a [sandbox] refusal saying %q", refusal, test.refusal)
				}
			})
		}
	}
}

// makeNestedGit makes a repository's .git deep in worktree, where the
// container could make one.
func makeNestedGit(t *testing.T, worktree string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(worktree, "node_modules", "pkg", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// holdSandboxRun records the container name as one of meta's goblins sandbox
// run started: by this process, which runs, when isRunning, or by a process
// that ended.
func holdSandboxRun(t *testing.T, meta state.TaskMeta, name string, isRunning bool) {
	t.Helper()
	runs := filepath.Join(meta.TaskTmp, sandbox.RunsDir)
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	if isRunning {
		if _, err := lock.AcquireExclusiveNamedFor(runs, name, "a test's container"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(runs, name) })
		return
	}
	ended := exec.Command("cmd.exe", "/c", "exit 0")
	if err := ended.Run(); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(lock.Info{PID: ended.Process.Pid, OwnerPID: ended.Process.Pid, Start: time.Now().Add(-time.Minute), Acquired: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, name), record, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Codex's and pi's seals hold every goblin's tool calls to its task's path
// boundary, as the board holds Claude Code's: a shell command that reads an
// ssh key and a pi write outside the worktree are refused before they run,
// naming the path, and a call within the boundary runs.
func TestCodexAndPiSealsHoldAGoblinToItsPathBoundary(t *testing.T) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	for _, test := range []struct {
		name, entry, tool string
		input             func(worktree string) map[string]string
		refused           string
	}{
		{"a Codex command that reads an ssh key", "goblin-seal-codex", "Bash", func(string) map[string]string { return map[string]string{"command": "cat ~/.ssh/id_ed25519"} }, filepath.Join(userHome, ".ssh", "id_ed25519")},
		{"a pi command that copies the ssh folder after moving home", "goblin-seal-pi", "bash", func(string) map[string]string { return map[string]string{"command": "cd ~ && cp -r .ssh ./keys"} }, filepath.Join(userHome, ".ssh")},
		{"a pi write outside the worktree", "goblin-seal-pi", "write", func(string) map[string]string { return map[string]string{"path": outside, "content": "x"} }, outside},
		{"a Codex command in the worktree", "goblin-seal-codex", "Bash", func(string) map[string]string { return map[string]string{"command": "git status"} }, ""},
		{"a pi write in the worktree", "goblin-seal-pi", "write", func(worktree string) map[string]string {
			return map[string]string{"path": filepath.Join(worktree, "main.go"), "content": "x"}
		}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := goblinHookHome(t)
			meta, err := state.ReadTaskMeta(stateDir, "pd-keys")
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(map[string]any{"session_id": "s1", "cwd": meta.Worktree, "hook_event_name": "PreToolUse", "tool_name": test.tool, "tool_input": test.input(meta.Worktree)})
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runHook(test.entry, strings.NewReader(string(data)), &stdout, &stderr)

			// Assert
			if exit != 0 || stderr.Len() != 0 {
				t.Fatalf("exit = %d, stderr = %q, want 0 and the answer on stdout", exit, stderr.String())
			}
			if test.refused == "" {
				if stdout.Len() != 0 {
					t.Fatalf("stdout = %q, want none, so the call runs", stdout.String())
				}
				return
			}
			refusals := sealRefusals(t, stdout.String())
			if len(refusals) != 1 || !strings.HasPrefix(refusals[0], "[path-boundary] ") || !strings.Contains(strings.ToLower(refusals[0]), strings.ToLower(test.refused)) {
				t.Fatalf("refusals = %q, want one [path-boundary] refusal naming %s", refusals, test.refused)
			}
		})
	}
}
