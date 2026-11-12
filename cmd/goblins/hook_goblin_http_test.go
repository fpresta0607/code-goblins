package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// hookGoblin is the goblin of the board's goblin hook tests.
const hookGoblin = "pd-keys"

// goblinHookBoard is the board's goblin hook handler for a scratch home whose
// goblin hookGoblin works in a worktree of its own, with the proof its
// terminal was given, and returns the home and the goblin's worktree.
func goblinHookBoard(t *testing.T) (goblinHookHandler, home.Home, string) {
	t.Helper()
	root := useScratchHome(t)
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	worktree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(h.State, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: hookGoblin, Harness: "claude", Worktree: worktree, Project: worktree, Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{hookGoblin, "pd-other"} {
		line := host.ProofSum("proof-of-"+task) + " " + time.Now().UTC().Format(time.RFC3339Nano) + "\n"
		if err := os.WriteFile(filepath.Join(h.State, "hosts", task+".proofs"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return goblinHookHandler{home: h}, h, worktree
}

// goblinVariables are the variables hookGoblin's hooks carry, as Claude Code
// fills them from its terminal's environment.
func goblinVariables() map[string]string {
	return map[string]string{harness.RoleVariable: harness.RoleGoblin, "GOBLINS_TASK_ID": hookGoblin, "GOBLINS_SPAWN_GEN": "3", host.IDVariable: hookGoblin, host.ProofVariable: "proof-of-" + hookGoblin}
}

// postHook posts body to path as a goblin's Claude Code posts its hook, with
// variables in their headers.
func postHook(handler http.Handler, method, path, body string, variables map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for variable, value := range variables {
		request.Header.Set(install.HookHeader(variable), value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func shellCallBody(t *testing.T, command string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"session_id": "s1", "hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]string{"command": command}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func toolCallBody(t *testing.T, call, output string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"session_id": "s1", "hook_event_name": "PostToolUse", "cwd": `C:\work`, "tool_name": "Read", "tool_use_id": call, "tool_response": map[string]any{"file": map[string]string{"content": output}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The board seals a goblin's command as the hook command it replaces did: a
// browser command waits while a key fetch reads in the goblin's browser, and
// every command while its worktree holds a start hook no package ships, the
// worktree its task record names. Any other command runs.
func TestTheBoardSealsAGoblinsCommand(t *testing.T) {
	for _, test := range []struct {
		name        string
		arrange     func(t *testing.T, stateDir, worktree string)
		command     string
		wantRefusal string
	}{
		{"a browser command while sealed", func(t *testing.T, stateDir, _ string) { sealTask(hookGoblin, time.Hour)(t, stateDir) }, "chrome-devtools-axi snapshot", "key fetch is reading"},
		{"another command while sealed", func(t *testing.T, stateDir, _ string) { sealTask(hookGoblin, time.Hour)(t, stateDir) }, "git status", ""},
		{"a browser command with no seal", func(*testing.T, string, string) {}, "chrome-devtools-axi snapshot", ""},
		{"a command beside a planted start hook", func(t *testing.T, _, worktree string) { plantStartHook(t, worktree, "") }, "git status", "runs at every Python start"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			board, h, worktree := goblinHookBoard(t)
			test.arrange(t, h.State, worktree)

			// Act
			response := postHook(board, http.MethodPost, install.GoblinSealPath, shellCallBody(t, test.command), goblinVariables())

			// Assert
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %q, want 200", response.Code, response.Body.String())
			}
			if test.wantRefusal == "" {
				if response.Body.Len() != 0 {
					t.Fatalf("body = %q, want none, so the command runs", response.Body.String())
				}
				return
			}
			var answer struct {
				Output struct {
					Event    string `json:"hookEventName"`
					Decision string `json:"permissionDecision"`
					Reason   string `json:"permissionDecisionReason"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || answer.Output.Event != "PreToolUse" || answer.Output.Decision != "deny" || !strings.Contains(answer.Output.Reason, test.wantRefusal) {
				t.Fatalf("body = %q (%v), want the command denied with %q", response.Body.String(), err, test.wantRefusal)
			}
		})
	}
}

// The board reports a goblin's tool call to the supervisor as the native hook
// did, naming its task, generation and terminal, and raises the key alarm
// for a key its output holds, without the key.
func TestTheBoardReportsAGoblinsToolCallAndRaisesItsKeyAlarm(t *testing.T) {
	// Arrange
	board, h, _ := goblinHookBoard(t)
	key := keyShaped("sk" + "_test_")

	// Act
	response := postHook(board, http.MethodPost, install.GoblinToolPath, toolCallBody(t, "toolu_1", "STRIPE_TEST_SECRET_KEY="+key), goblinVariables())

	// Assert
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "{}" {
		t.Fatalf("status = %d, body = %q, want 200 and {}", response.Code, response.Body.String())
	}
	entries, err := os.ReadDir(nativehook.SpoolDir(h.State))
	if err != nil || len(entries) != 1 {
		t.Fatalf("spool = %v, %v, want the one event", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(nativehook.SpoolDir(h.State), entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var event nativehook.Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Harness != "claude" || event.TaskID != hookGoblin || event.Generation != "3" || event.HostID != hookGoblin || event.Role != harness.RoleGoblin {
		t.Errorf("event = %+v, want the goblin's tool call with its task, generation and terminal", event)
	}
	wakes, err := wake.Pending(h.State)
	if err != nil || len(wakes) != 1 || !strings.Contains(wakes[0].Detail, hookGoblin+"'s Read output held") || strings.Contains(wakes[0].Detail, key) {
		t.Fatalf("wakes = %+v, %v, want one key alarm that names no key", wakes, err)
	}
}

// Only a goblin's own terminal speaks for it: a hook its terminal's proof
// does not prove, one that names another goblin's terminal, one of no
// goblin, and one the board serves no hook at is refused, and none is
// reported or sealed.
func TestTheBoardRefusesAGoblinHookItsTerminalDoesNotProve(t *testing.T) {
	without := func(variable string) map[string]string {
		variables := goblinVariables()
		delete(variables, variable)
		return variables
	}
	with := func(variable, value string) map[string]string {
		variables := goblinVariables()
		variables[variable] = value
		return variables
	}
	anotherTerminal := with(host.IDVariable, "pd-other")
	anotherTerminal[host.ProofVariable] = "proof-of-pd-other"
	for _, test := range []struct {
		name       string
		method     string
		path       string
		variables  map[string]string
		wantStatus int
	}{
		{"no proof", http.MethodPost, install.GoblinToolPath, without(host.ProofVariable), http.StatusForbidden},
		{"a proof its terminal was never given", http.MethodPost, install.GoblinToolPath, with(host.ProofVariable, "guessed"), http.StatusForbidden},
		{"another goblin's terminal and proof", http.MethodPost, install.GoblinToolPath, anotherTerminal, http.StatusForbidden},
		{"no goblin", http.MethodPost, install.GoblinToolPath, with(harness.RoleVariable, "ceo"), http.StatusForbidden},
		{"a task that is none", http.MethodPost, install.GoblinToolPath, with("GOBLINS_TASK_ID", `..\pd-keys`), http.StatusForbidden},
		{"a read", http.MethodGet, install.GoblinToolPath, goblinVariables(), http.StatusMethodNotAllowed},
		{"a hook the board serves none at", http.MethodPost, install.GoblinHookPrefix + "other", goblinVariables(), http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			board, h, _ := goblinHookBoard(t)

			// Act
			response := postHook(board, test.method, test.path, toolCallBody(t, "toolu_1", "STRIPE_TEST_SECRET_KEY="+keyShaped("sk"+"_test_")), test.variables)

			// Assert
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %q, want %d", response.Code, response.Body.String(), test.wantStatus)
			}
			if entries, _ := os.ReadDir(nativehook.SpoolDir(h.State)); len(entries) != 0 {
				t.Errorf("spool holds %d events, want none", len(entries))
			}
			if wakes, err := wake.Pending(h.State); err != nil || len(wakes) != 0 {
				t.Errorf("wakes = %+v, %v, want none", wakes, err)
			}
		})
	}
}

// A seal whose goblin's task record cannot be read cannot name the worktree
// it reads, and so refuses the command, as Claude Code refuses one whose seal
// does not answer.
func TestTheBoardRefusesASealItCannotReadTheTaskOf(t *testing.T) {
	// Arrange
	board, h, _ := goblinHookBoard(t)
	if err := os.Remove(state.TaskMetaPath(h.State, hookGoblin)); err != nil {
		t.Fatal(err)
	}

	// Act
	response := postHook(board, http.MethodPost, install.GoblinSealPath, shellCallBody(t, "git status"), goblinVariables())

	// Assert
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %q, want 503", response.Code, response.Body.String())
	}
}

// The board holds a goblin's file tools to its task's path boundary: a read
// of the ssh folder or the credential store and a write outside its worktree
// are denied before they run, naming the path, and a write in its worktree
// runs.
func TestTheBoardHoldsAGoblinsFileToolsToItsPathBoundary(t *testing.T) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	fileStore := filepath.Join(t.TempDir(), "credentials")
	t.Setenv("GOBLINS_CREDENTIAL_DIR", fileStore)
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	for _, test := range []struct {
		name, tool, path string
		isRefused        bool
	}{
		{"a read of an ssh key", "Read", filepath.Join(userHome, ".ssh", "id_ed25519"), true},
		{"a read of the credential store", "Read", filepath.Join(fileStore, "project", "TOKEN"), true},
		{"a write outside the worktree", "Write", outside, true},
		{"a write in the worktree", "Write", "", false},
		{"a read outside the worktree", "Read", outside, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			board, _, worktree := goblinHookBoard(t)
			path := test.path
			if path == "" {
				path = filepath.Join(worktree, "main.go")
			}
			body, err := json.Marshal(map[string]any{"session_id": "s1", "hook_event_name": "PreToolUse", "tool_name": test.tool, "tool_input": map[string]string{"file_path": path, "content": "x"}})
			if err != nil {
				t.Fatal(err)
			}

			// Act
			response := postHook(board, http.MethodPost, install.GoblinSealPath, string(body), goblinVariables())

			// Assert
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %q, want 200", response.Code, response.Body.String())
			}
			if !test.isRefused {
				if response.Body.Len() != 0 {
					t.Fatalf("body = %q, want none, so the call runs", response.Body.String())
				}
				return
			}
			var answer struct {
				Output struct {
					Decision string `json:"permissionDecision"`
					Reason   string `json:"permissionDecisionReason"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || answer.Output.Decision != "deny" || !strings.Contains(strings.ToLower(answer.Output.Reason), strings.ToLower(path)) {
				t.Fatalf("body = %q (%v), want the call denied naming %s", response.Body.String(), err, path)
			}
			if _, err := os.Stat(path); test.tool == "Write" && !os.IsNotExist(err) {
				t.Fatalf("the refused call left %s behind (%v)", path, err)
			}
		})
	}
}
