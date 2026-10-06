package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// TestACommandCenterAnswerReachesAPrintingCFOOnce is the end-to-end proof
// that input typed into a native terminal reaches a real CFO at once and once
// while it prints: a Claude Code and a Codex CFO, each in a scratch home under
// `cfo serve --example`, asks a board question, then runs a command that
// prints for most of a minute, and while it prints the Command Center answers
// the question. The answer must reach the CFO's conversation exactly once,
// its composer must end empty, and the board must count the delivery done.
//
// It runs real harnesses on the Overlord's subscriptions, so it runs only
// when asked:
//
//	CFO_INPUT_REAL=1 CFO_INPUT_BINARY=<cfo.exe built from this tree>
//	CFO_INPUT_RESULTS=<a directory for the record> [CFO_INPUT_ONLY=claude|codex]
func TestACommandCenterAnswerReachesAPrintingCFOOnce(t *testing.T) {
	if os.Getenv("CFO_INPUT_REAL") != "1" {
		t.Skip("set CFO_INPUT_REAL=1 with CFO_INPUT_BINARY and CFO_INPUT_RESULTS to prove input reaches real CFOs")
	}
	binary, results := os.Getenv("CFO_INPUT_BINARY"), os.Getenv("CFO_INPUT_RESULTS")
	if binary == "" || results == "" {
		t.Fatal("CFO_INPUT_BINARY and CFO_INPUT_RESULTS are both required")
	}
	if root, state := home.Inherited(); root != "" || state != "" {
		t.Fatalf("this process inherited the fleet home %s (state %s); unset CFO_HOME and CFO_STATE_OVERRIDE first", root, state)
	}
	for i, cfo := range []string{"claude", "codex"} {
		if only := os.Getenv("CFO_INPUT_ONLY"); only != "" && only != cfo {
			continue
		}
		t.Run(cfo+"-cfo", func(t *testing.T) {
			proveAnswerOnce(t, &wakeProof{binary: binary, root: filepath.Join(results, cfo+"-cfo"), cfo: cfo, port: 4402 + i})
		})
	}
}

func proveAnswerOnce(t *testing.T, p *wakeProof) {
	if err := os.MkdirAll(p.root, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(p.root, "proof.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	p.log = logFile
	p.home = home.Home{Root: filepath.Join(p.root, "home"), State: filepath.Join(p.root, "home", "state"), Data: filepath.Join(p.root, "home", "data")}
	for _, dir := range []string{p.home.State, p.home.Data} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeProofFile(t, filepath.Join(p.home.Root, "AGENTS.md"), "# Scratch CFO home for the input proof\n")
	writeProofFile(t, filepath.Join(p.home.Root, home.InstalledMarker), "")
	started := filepath.Join(p.root, "printing")
	printer := filepath.Join(p.root, "print.ps1")
	// About 45 seconds of lines, after the file that says it started.
	writeProofFile(t, printer, "New-Item -ItemType File -Force '"+started+"' | Out-Null\r\n1..3000 | ForEach-Object { \"proof line $_ of 3000\"; Start-Sleep -Milliseconds 15 }\r\n")
	p.say("scratch home %s; CFO %s", p.home.Root, p.cfo)
	p.env = p.environment()

	serve := exec.Command(p.binary, "serve", "--example", "--listen", fmt.Sprintf("127.0.0.1:%d", p.port))
	serve.Env = p.env
	serve.Stdout, serve.Stderr = logFile, logFile
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serve.Process.Kill(); _, _ = serve.Process.Wait() })
	p.say("cfo serve --example pid %d", serve.Process.Pid)
	p.await(t, "the board record", time.Minute, func() bool { return exists(filepath.Join(p.home.State, "board.json")) })

	prompt := "You stand in for the CFO of a scratch Code Goblins home in an automated test. Use the shell only as told. "
	if p.cfo != "claude" {
		prompt += "First run the command cfo register. "
	}
	// A Codex CFO starts through cmd, which reads quotes as more than text.
	prompt += "Run this command exactly: cfo question --id proof-question --text Continue? --option Continue --option Stop . " +
		"Then run this command exactly and wait for it to finish: powershell -NoProfile -ExecutionPolicy Bypass -File " + filepath.ToSlash(printer) + " . " +
		"Then reply printed and end your turn. " +
		"When a line starting with Overlord: reaches you, reply with the single word answered and end your turn. " +
		"Run no other command and change no file."
	var args []string
	switch p.cfo {
	case "claude":
		args, err = nativeCFOProgram("claude", append(p.claudeInputArguments(t), prompt)...)
	case "codex":
		args, err = spawn.NativeProgram("codex", "--dangerously-bypass-approvals-and-sandbox", "-c", "check_for_update_on_startup=false", "-m", proofModel("codex"), prompt)
	}
	if err != nil {
		t.Fatal(err)
	}
	cfo, err := host.Launch(p.home.State, []string{p.binary, "host"}, p.env, host.Spec{ID: supervisor.NativeCFOTerminal, Args: args, Dir: p.home.Root, Cols: 120, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if process, err := os.FindProcess(cfo.HostPID); err == nil {
			_ = process.Kill()
		}
	})
	p.cfoTerminal = &cfo
	p.say("CFO %s runs in native terminal cfo, host pid %d, harness pid %d", p.cfo, cfo.HostPID, cfo.ChildPID)
	p.settle(t, cfo)
	p.await(t, "the CFO to register", 5*time.Minute, func() bool {
		agent, live := livePrimaryFile(p.home.State)
		return live && agent == p.cfo
	})
	var question supervisor.Question
	p.await(t, "the CFO's question on the board", 5*time.Minute, func() bool {
		for _, asked := range p.snapshot(t).Questions {
			if asked.ID == "proof-question" && asked.Status == "pending" {
				question = asked
				return true
			}
		}
		return false
	})
	p.await(t, "the CFO to print", 5*time.Minute, func() bool { return exists(started) })
	screens, _ := harness.NativeScreens(harness.Kind(p.cfo))
	if screen, err := host.ReadScreen(cfo); err != nil || !screens.IsWorking(screen) {
		p.say("the CFO's screen as it prints (read error %v):\n%s", err, host.ScreenTail(screen, 40))
		t.Fatalf("the %s CFO does not show a turn while it prints; see %s", p.cfo, filepath.Join(p.root, "proof.log"))
	}

	// Act: the Overlord answers on the board while the CFO prints.
	marker := fmt.Sprintf("input-proof-%d", time.Now().UnixNano())
	p.post(t, map[string]string{"id": "proof-answer", "kind": "cfo_answer", "generation": question.Identity, "question_id": question.ID, "text": "Continue " + marker, "answer_kind": "other"})
	p.say("answered the question with %s while the CFO printed", marker)

	// Assert
	p.await(t, "the answer in the CFO's conversation", 8*time.Minute, func() bool {
		submitted, err := p.promptsHolding(marker)
		return err == nil && submitted > 0
	})
	p.await(t, "the CFO to answer and end its turn", 5*time.Minute, func() bool {
		screen, err := host.ReadScreen(cfo)
		if err != nil {
			return false
		}
		// Codex can offer optional setup once a turn ends, which the board
		// dismisses as well before it types.
		if dialog, shows := screens.Dialog(screen); shows && dialog.EscapeHint != "" {
			p.say("dismissing %s, as a delivery would: %v", dialog.Name, spawn.AnswerDialog(context.Background(), cfo, dialog, screen))
			return false
		}
		return screens.IsReady(screen) && !screens.IsWorking(screen) && screens.ComposerEmpty(screen)
	})
	screen, _ := host.ReadScreen(cfo)
	p.say("the CFO's screen after it answered:\n%s", host.ScreenTail(screen, 40))
	submitted, err := p.promptsHolding(marker)
	if err != nil {
		t.Fatal(err)
	}
	p.say("the CFO's conversation holds the answer %d times", submitted)
	if submitted != 1 {
		t.Fatalf("the %s CFO's conversation holds the answer %d times, want once; see %s", p.cfo, submitted, filepath.Join(p.root, "proof.log"))
	}
	// Only the Claude Code CFO runs this tree's prompt hook: a Codex session
	// takes its hooks from this machine's Codex settings, which the proof
	// leaves alone, and a delivery made behind a turn is confirmed only by
	// the hook.
	delivered := func() (supervisor.Action, bool) {
		for _, action := range p.snapshot(t).Actions {
			if action.ID == "proof-answer" {
				return action, action.Status == "succeeded"
			}
		}
		return supervisor.Action{}, false
	}
	if p.cfo == "claude" {
		p.await(t, "the board to count the answer delivered", 2*time.Minute, func() bool { _, done := delivered(); return done })
	}
	action, _ := delivered()
	p.say("the board's answer action: %s (%s)", action.Status, action.Message)
	if log, err := os.ReadFile(filepath.Join(p.home.State, "hosts", supervisor.NativeCFOTerminal+".log")); err == nil {
		p.say("the CFO terminal's host log:\n%s", log)
	}
}

// claudeInputArguments are what the proof's Claude Code CFO starts with:
// this tree's registration hook and its native prompt hook, reporting to the
// scratch home, from settings of its own and none of the user's.
func (p *wakeProof) claudeInputArguments(t *testing.T) []string {
	settings := filepath.Join(p.root, "claude-settings.json")
	native := map[string]any{"type": "command", "command": p.binary, "args": []string{"native-hook", "claude", "--home", p.home.Root, "--state", p.home.State}, "timeout": 30}
	register := map[string]any{"type": "command", "command": p.binary, "args": []string{"hook", "session-start"}, "timeout": 120}
	data, err := json.Marshal(map[string]any{"hooks": map[string]any{
		"SessionStart":     []any{map[string]any{"hooks": []any{register, native}}},
		"UserPromptSubmit": []any{map[string]any{"hooks": []any{native}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeProofFile(t, settings, string(data))
	return []string{"--setting-sources", "project,local", "--settings", settings, "--dangerously-skip-permissions"}
}

// snapshot reads the scratch board's snapshot.
func (p *wakeProof) snapshot(t *testing.T) supervisor.Snapshot {
	response, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/snapshot", p.port))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var snapshot supervisor.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// post submits an action to the scratch board as its page does.
func (p *wakeProof) post(t *testing.T, action map[string]string) {
	data, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	origin := fmt.Sprintf("http://127.0.0.1:%d", p.port)
	request, err := http.NewRequest(http.MethodPost, origin+"/api/actions", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	request.Header.Set("X-CFO-Token", p.snapshot(t).Instance)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("the board answered the action with %d: %s", response.StatusCode, body)
	}
}

// promptsHolding counts the prompts holding marker, which no earlier
// conversation can hold, in the CFO's own record of its conversation: Claude
// Code's transcript of the scratch home, or the Codex rollout of the thread
// the CFO registered with. Neither is filtered by when it was last written,
// which Windows keeps late for a file its writer holds open.
func (p *wakeProof) promptsHolding(marker string) (int, error) {
	profile, err := os.UserHomeDir()
	if err != nil {
		return 0, err
	}
	var files []string
	switch p.cfo {
	case "claude":
		// Claude Code names a project's folder after its path, every
		// character but a letter or digit made a dash.
		folder := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(p.home.Root, "-")
		files, err = filepath.Glob(filepath.Join(profile, ".claude", "projects", folder+"*", "*.jsonl"))
	case "codex":
		data, readErr := os.ReadFile(filepath.Join(p.home.State, "primary.json"))
		if readErr != nil {
			return 0, readErr
		}
		var primary struct {
			Process struct {
				Session string `json:"session"`
			} `json:"process"`
		}
		if err := json.Unmarshal(data, &primary); err != nil || primary.Process.Session == "" {
			return 0, fmt.Errorf("the Codex CFO registered no thread: %v", err)
		}
		files, err = filepath.Glob(filepath.Join(profile, ".codex", "sessions", "*", "*", "*", "rollout-*"+primary.Process.Session+".jsonl"))
	}
	if err != nil {
		return 0, err
	}
	if len(files) == 0 {
		return 0, fmt.Errorf("no record of the %s CFO's conversation was found", p.cfo)
	}
	prompts := 0
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return 0, err
		}
		lines := bufio.NewScanner(bytes.NewReader(data))
		lines.Buffer(make([]byte, 1<<20), 64<<20)
		for lines.Scan() {
			line := lines.Text()
			if !strings.Contains(line, marker) {
				continue
			}
			var record struct {
				Type    string `json:"type"`
				Payload struct {
					Type string `json:"type"`
					Role string `json:"role"`
				} `json:"payload"`
				Message struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				} `json:"message"`
				Attachment struct {
					Type string `json:"type"`
				} `json:"attachment"`
			}
			if json.Unmarshal([]byte(line), &record) != nil {
				continue
			}
			// A prompt the user submitted: Claude Code records one submitted
			// at its composer as a user message that is not a tool's result,
			// and one typed during a turn as the queued command it took into
			// that turn; Codex records either as a user message it hands the
			// model, besides events that quote it.
			isClaudePrompt := record.Type == "user" && record.Message.Role == "user" && !strings.Contains(string(record.Message.Content), `"tool_result"`)
			isClaudeQueuedPrompt := record.Type == "attachment" && record.Attachment.Type == "queued_command"
			isCodexPrompt := record.Type == "response_item" && record.Payload.Type == "message" && record.Payload.Role == "user"
			if isClaudePrompt || isClaudeQueuedPrompt || isCodexPrompt {
				prompts++
			}
		}
	}
	return prompts, nil
}
