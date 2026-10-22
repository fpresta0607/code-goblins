package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func restartThroughHTTP(t *testing.T, h home.Home, harness string) *httptest.ResponseRecorder {
	t.Helper()
	store, err := supervisor.Open(h)
	if err != nil {
		t.Fatal(err)
	}
	service := &supervisor.Service{Store: store, Instance: "switch-test", Options: supervisor.Options{FirstRun: firstRunOn(h, t.TempDir(), true, func(string) error { return nil })}}
	request := httptest.NewRequest(http.MethodPost, "http://board.local/api/cfo/restart", strings.NewReader(fmt.Sprintf(`{"harness":%q}`, harness)))
	request.Header.Set("Origin", "http://board.local")
	request.Header.Set("X-CFO-Token", service.Instance)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	supervisor.NewHTTP(service, "board.local", nil).ServeHTTP(response, request)
	return response
}

func switchStandIns(t *testing.T, h home.Home) {
	t.Helper()
	t.Setenv("CFO_HOME", h.Root)
	t.Setenv("CFO_STATE_OVERRIDE", h.State)
	t.Setenv("CFO_PROJECTS_ROOT", filepath.Join(h.Root, "projects"))
	t.Setenv("PATH", h.Root)
	rules, err := json.Marshal([]standin.Rule{{Args: "--help", Stdout: "Options:\n  --tui-mode <mode>              TUI mode: regular or fullscreen\n"}, {Any: true, Hold: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"codex", "pi"} {
		program := filepath.Join(h.Root, harness+"-standin.exe")
		standin.Put(t, program)
		script := "@echo off\r\nset \"CFO_STANDIN_RULES=" + string(rules) + "\"\r\n" +
			"set \"CFO_STANDIN_RECORD=" + filepath.Join(h.Root, "switch-arguments.txt") + "\"\r\n" +
			"\"" + program + "\" %*\r\n"
		if err := os.WriteFile(filepath.Join(h.Root, harness+".cmd"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCFOSwitchRoundTripStartsFreshAndDeliversWakes(t *testing.T) {
	// Arrange
	h := home.Home{Root: t.TempDir()}
	h.State, h.Data = filepath.Join(h.Root, "state"), filepath.Join(h.Root, "data")
	switchStandIns(t, h)
	if err := startNativeCFO(h, h.Root, "codex", nil, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeNativeTerminal(t, h.State, supervisor.NativeCFOTerminal) })
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(20 * time.Millisecond) {
		arguments, err := os.ReadFile(filepath.Join(h.Root, "switch-arguments.txt"))
		if err == nil && len(arguments) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the initial CFO recorded no arguments within a minute")
		}
	}
	before, err := host.ReadRecord(h.State, supervisor.NativeCFOTerminal)
	if err != nil {
		t.Fatal(err)
	}
	registerRestartCFO(t, h, before, "codex", "codex-before")
	delivered := make(chan string, 2)
	connection := &supervisor.CFOConnection{
		State: h.State,
		ReadScreen: func(host.Record) ([]string, error) {
			agent, err := cfoHarness(h.State)
			if err != nil {
				return nil, err
			}
			if agent == "pi" {
				rule := strings.Repeat("─", 60)
				return []string{"Done.", rule, "  ", rule, "0.8%/1.0M (auto)"}, nil
			}
			return []string{"• Done.", "› Ask Codex to do anything", "  gpt-6-astra high · work"}, nil
		},
		Deliver: func(_ context.Context, terminal state.TaskMeta, text string) error {
			record, err := host.ReadRecord(h.State, terminal.ID)
			if err != nil {
				return err
			}
			client, err := host.Dial(record)
			if err != nil {
				return err
			}
			defer client.Close()
			if err := client.Input([]byte(text + "\r")); err != nil {
				return err
			}
			delivered <- terminal.Harness + "|" + text
			return nil
		},
	}
	service, err := supervisor.Start(context.Background(), h, supervisor.Options{Example: true, CFO: connection})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)

	for _, harness := range []string{"pi", "codex"} {
		// Act
		response := restartThroughHTTP(t, h, harness)

		// Assert
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"resumed":false`) {
			t.Fatalf("switch to %s: %d %s", harness, response.Code, response.Body.String())
		}
		if _, isAlive := proc.StartTime(before.ChildPID); isAlive {
			t.Fatalf("the previous CFO pid %d still runs", before.ChildPID)
		}
		after, err := host.ReadRecord(h.State, supervisor.NativeCFOTerminal)
		if err != nil || !host.Running(after) {
			t.Fatalf("replacement terminal = %+v, %v", after, err)
		}
		if saved, err := cfoHarness(h.State); err != nil || saved != harness {
			t.Fatalf("remembered harness = %q, %v; want %s", saved, err, harness)
		}
		registerRestartCFO(t, h, after, harness, harness+"-after")
		if _, err := wake.Append(h.State, "notify", harness, "switch proof"); err != nil {
			t.Fatal(err)
		}
		select {
		case text := <-delivered:
			if !strings.HasPrefix(text, harness+"|cfo watcher wake:") || !strings.Contains(text, "notify "+harness) {
				t.Fatalf("wake delivered = %q", text)
			}
		case <-time.After(45 * time.Second):
			t.Fatal("no wake reached the replacement CFO within 45s")
		}
		before = after
	}
	arguments, err := os.ReadFile(filepath.Join(h.Root, "switch-arguments.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(arguments)), "\r\n") {
		if !strings.HasSuffix(line, "-standin --help") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("harness starts = %q, want three", lines)
	}
	codexHooks, err := cfoHookArguments(h, "codex")
	if err != nil {
		t.Fatal(err)
	}
	for index, harness := range []string{"codex", "pi", "codex"} {
		want := harness + "-standin "
		if harness == "codex" {
			want += strings.Join(slices.Concat(codexHooks, cfoCompactArguments("codex"), []string{"-c", "tui.copy_on_select=always", "--no-daemon"}), " ") + " "
		}
		if harness == "pi" {
			want += "--tui-mode fullscreen "
		}
		// The first start is the home's first CFO; each switch restarts it,
		// told why.
		prompt := cfoFirstPrompt
		if index > 0 {
			prompt = cfoBackPrompt(harness, restartedWhy, "The CFO starts a new conversation in "+harness+" because its harness changed.")
		}
		if lines[index] != want+prompt {
			t.Errorf("start %d = %q, want fresh arguments %q", index, lines[index], want+prompt)
		}
	}
}

func TestCFOSwitchRefusalsLeaveTheRunningCFOUntouched(t *testing.T) {
	for _, testCase := range []struct {
		name, harness, problem string
		status                 int
	}{
		{"unsupported", "unknown", "not claude, codex or pi", http.StatusBadRequest},
		{"unavailable", "codex", "program cannot be found", http.StatusInternalServerError},
		{"save failure", "codex", "could not be remembered", http.StatusInternalServerError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			h := fakeClaudeHome(t)
			h.Data = filepath.Join(h.Root, "data")
			before := nativeCFORunning(t, h, "current-session")
			if testCase.name == "save failure" {
				switchStandIns(t, h)
				if err := os.Mkdir(cfoHarnessPath(h.State), 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(cfoHarnessPath(h.State), []byte("claude\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			response := restartThroughHTTP(t, h, testCase.harness)

			// Assert
			if response.Code != testCase.status || !strings.Contains(response.Body.String(), testCase.problem) {
				t.Fatalf("restart = %d %s", response.Code, response.Body.String())
			}
			if !host.Running(before) {
				t.Fatal("the old CFO stopped after a refused choice")
			}
			conversation, err := supervisor.ReadCFOConversation(h.State)
			if err != nil || conversation.PID != before.ChildPID || conversation.Session != "current-session" {
				t.Fatalf("conversation changed after refusal: %+v, %v", conversation, err)
			}
			if testCase.name != "save failure" {
				if saved, err := cfoHarness(h.State); err != nil || saved != "claude" {
					t.Fatalf("remembered choice changed after refusal: %q, %v", saved, err)
				}
			}
		})
	}
}

func TestGoblinsResumeSwitchesHarnessAndTheBoardSwitchesBack(t *testing.T) {
	// Arrange
	h := fakeClaudeHome(t)
	h.Data = filepath.Join(h.Root, "data")
	claudePath := os.Getenv("PATH")
	before := nativeCFORunning(t, h, "claude-before")
	switchStandIns(t, h)
	t.Setenv("PATH", h.Root+string(os.PathListSeparator)+claudePath)
	f := newSessionFixture(t)
	f.home = h
	f.record(fakeBoard(t, busySnapshot))
	f.runtime.resolveHome = func() (home.Home, error) { return h, nil }
	f.runtime.restartCFO = restartCFO
	f.runtime.nativeTerminalRuns = supervisor.NativeTerminalRuns

	// Act
	exit, stdout, stderr := f.launch("resume", "--harness", "codex")

	// Assert
	if exit != 0 || !strings.Contains(stdout, "restarted as Codex on a new conversation") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	if host.Running(before) {
		t.Fatal("the earlier Claude CFO still runs")
	}
	if saved, err := cfoHarness(h.State); err != nil || saved != "codex" {
		t.Fatalf("remembered harness = %q, %v", saved, err)
	}
	record, err := host.ReadRecord(h.State, supervisor.NativeCFOTerminal)
	if err != nil {
		t.Fatal(err)
	}
	registerRestartCFO(t, h, record, "codex", "codex-after")
	if err := os.Remove(filepath.Join(h.Root, fakeClaudeArguments)); err != nil {
		t.Fatal(err)
	}
	response := restartThroughHTTP(t, h, "claude")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"resumed":false`) {
		t.Fatalf("switch back = %d %s", response.Code, response.Body.String())
	}
	if host.Running(record) {
		t.Fatal("the earlier Codex CFO still runs")
	}
	if arguments, want := waitForFakeClaudeArguments(t, h.Root, nil), cfoBackPrompt("claude", restartedWhy, "The CFO starts a new conversation in claude because its harness changed."); !slices.Equal(arguments, []string{want}) {
		t.Fatalf("Claude arguments = %q, want fresh startup told why", arguments)
	}
	if saved, err := cfoHarness(h.State); err != nil || saved != "claude" {
		t.Fatalf("remembered harness = %q, %v", saved, err)
	}
}

func TestCFOSwitchReportsLaunchFailureAndKeepsTheChoiceForRecovery(t *testing.T) {
	// Arrange
	h := fakeClaudeHome(t)
	h.Data = filepath.Join(h.Root, "data")
	before := nativeCFORunning(t, h, "current-session")
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "codex.exe"), []byte("not an executable"), 0o700); err != nil {
		t.Fatal(err)
	}

	// Act
	response := restartThroughHTTP(t, h, "codex")

	// Assert
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "stopped but did not start again") {
		t.Fatalf("restart = %d %s, want the failed launch reported", response.Code, response.Body.String())
	}
	if host.Running(before) || supervisor.NativeTerminalRuns(h.State, supervisor.NativeCFOTerminal) {
		t.Fatal("the stopped CFO or failed replacement still runs")
	}
	if saved, err := cfoHarness(h.State); err != nil || saved != "codex" {
		t.Fatalf("recovery harness = %q, %v; want codex", saved, err)
	}
}

func TestCFOSwitchReportsAReplacementThatEndsDuringStartup(t *testing.T) {
	// Arrange
	h := fakeClaudeHome(t)
	h.Data = filepath.Join(h.Root, "data")
	nativeCFORunning(t, h, "current-session")
	switchStandIns(t, h)
	wait := cfoResumeWait
	cfoResumeWait = func(time.Duration) { closeNativeTerminal(t, h.State, supervisor.NativeCFOTerminal) }
	t.Cleanup(func() { cfoResumeWait = wait })

	// Act
	response := restartThroughHTTP(t, h, "codex")

	// Assert
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "ended during startup") {
		t.Fatalf("restart = %d %s, want the ended replacement reported", response.Code, response.Body.String())
	}
}

func TestCFORestartWithTheSameHarnessResumesItsConversationThroughHTTP(t *testing.T) {
	for _, harness := range []string{"", "claude"} {
		t.Run("harness="+harness, func(t *testing.T) {
			// Arrange
			h := fakeClaudeHome(t)
			h.Data = filepath.Join(h.Root, "data")
			nativeCFORunning(t, h, "current-session")
			if err := os.Remove(filepath.Join(h.Root, fakeClaudeArguments)); err != nil {
				t.Fatal(err)
			}

			// Act
			response := restartThroughHTTP(t, h, harness)

			// Assert
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"resumed":true,"session":"current-session"`) {
				t.Fatalf("restart = %d %s", response.Code, response.Body.String())
			}
			arguments := waitForFakeClaudeArguments(t, h.Root, nil)
			if !slices.Equal(arguments, []string{"--resume", "current-session", cfoBackPrompt("claude", restartedWhy, "")}) {
				t.Fatalf("arguments = %q, want the running conversation, told why it started again", arguments)
			}
		})
	}
}
