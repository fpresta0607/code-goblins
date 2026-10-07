package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// TestASteerReachesAWorkingGoblinAtItsNextToolCall is the end-to-end proof
// that text for a goblin in a long turn of many tool calls reaches it at its
// next tool call, not when its turn ends: a Claude Code and a Codex goblin,
// each spawned in a scratch home under `cfo serve --example`, ask a board
// question and then run twenty six-second steps, one tool call each. While
// they run, the Overlord answers on the board, and then `cfo send` tells the
// goblin to stop. Each must be taken within one tool call of its submit, as
// the goblin's own record of the conversation shows; the board must count the
// answer delivered only once that record holds it; and the goblin must stop
// and reply as told.
//
// It runs real harnesses on the Overlord's subscriptions, so it runs only
// when asked:
//
//	CFO_STEER_REAL=1 CFO_STEER_BINARY=<cfo.exe built from this tree>
//	CFO_STEER_RESULTS=<a directory for the record> [CFO_STEER_ONLY=claude|codex]
func TestASteerReachesAWorkingGoblinAtItsNextToolCall(t *testing.T) {
	if os.Getenv("CFO_STEER_REAL") != "1" {
		t.Skip("set CFO_STEER_REAL=1 with CFO_STEER_BINARY and CFO_STEER_RESULTS to prove steers reach real goblins mid-turn")
	}
	binary, results := os.Getenv("CFO_STEER_BINARY"), os.Getenv("CFO_STEER_RESULTS")
	if binary == "" || results == "" {
		t.Fatal("CFO_STEER_BINARY and CFO_STEER_RESULTS are both required")
	}
	if root, state := home.Inherited(); root != "" || state != "" {
		t.Fatalf("this process inherited the fleet home %s (state %s); unset CFO_HOME and CFO_STATE_OVERRIDE first", root, state)
	}
	for i, goblin := range []string{"claude", "codex"} {
		if only := os.Getenv("CFO_STEER_ONLY"); only != "" && only != goblin {
			continue
		}
		t.Run(goblin+"-goblin", func(t *testing.T) {
			root := filepath.Join(results, goblin+"-goblin")
			proveSteer(t, &wakeProof{binary: binary, root: root, project: filepath.Join(root, "project"), goblin: goblin, port: 4412 + i})
		})
	}
}

func proveSteer(t *testing.T, p *wakeProof) {
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
	writeProofFile(t, filepath.Join(p.home.Root, "AGENTS.md"), "# Scratch CFO home for the steer proof\n")
	writeProofFile(t, filepath.Join(p.home.Root, home.InstalledMarker), "")
	p.say("scratch home %s; goblin %s", p.home.Root, p.goblin)
	p.setUpProject(t)
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

	goblin := "steer-" + p.goblin
	defer p.endGoblin(goblin)
	// The goblin's terminal names this home's state but the user's own
	// home, so it runs this tree's cfo by its path.
	self := filepath.ToSlash(p.binary)
	task := "This is a delivery fixture in an automated test. First run this command exactly, once: " + self + " notify " + goblin + " --blocked \"Pick a color for the fixture. options: Red | Blue\" . " +
		fmt.Sprintf("Do not wait for an answer: right after it, run the shell command  sleep 6; echo step N done  %d times, for N from 1 to %d, strictly one at a time:", steerSteps, steerSteps) + " one shell tool call per step, wait for each to finish before the next, never two calls in one message, and write no text between steps. " +
		"Lines starting with CFO: may reach you while you work. One that answers your color question needs no reply: keep running the steps. One that tells you to stop you follow at once. Change no file and run no other command."
	p.cfoCommand(t, "spawn", goblin, "--project", p.project, "--brief", p.brief(t, goblin, task), "--mode", "local-only", "--harness", p.goblin, "--model", proofModel(p.goblin))
	meta, err := state.ReadTaskMeta(p.home.State, goblin)
	if err != nil {
		t.Fatal(err)
	}
	if record, err := host.ReadRecord(p.home.State, goblin); err == nil {
		p.cfoTerminal = &record
	}

	var question supervisor.Question
	p.await(t, "the goblin's question on the board", 5*time.Minute, func() bool {
		for _, asked := range p.snapshot(t).Questions {
			if asked.Task == goblin && asked.Status == "pending" {
				question = asked
				return true
			}
		}
		return false
	})
	p.say("the board shows %s's question %s", goblin, question.ID)
	p.await(t, "two steps done", 5*time.Minute, func() bool { return len(stepsDone(p.conversation(meta))) >= 2 })

	// Act: the Overlord answers on the board while the goblin runs its steps,
	// at length and beyond ASCII, as a pasted answer can be.
	answerMarker := fmt.Sprintf("board-answer-%d", time.Now().UnixNano())
	answer := "Blue, " + answerMarker + ". " + strings.Repeat("Keep the ünïcødé café ✓ notes. ", 30)
	answered := time.Now().UTC()
	p.post(t, map[string]string{"id": "steer-answer", "kind": "goblin_answer", "generation": question.Identity, "question_id": question.ID, "text": answer, "answer_kind": "other"})
	p.say("answered %s's question on the board with %s, %d characters", goblin, answerMarker, len([]rune(answer)))

	// Assert: the board counts the answer delivered only once the goblin's
	// record holds it, and never before the goblin took it.
	var seen []string
	var delivered supervisor.Action
	p.awaitEvery(t, "the board to count the answer delivered", 8*time.Minute, 250*time.Millisecond, func() bool {
		action, found := boardAction(p.snapshot(t), "steer-answer")
		taken := handedAt(p.conversation(meta), answerMarker) >= 0
		if found {
			seen = append(seen, fmt.Sprintf("%s taken=%t: %s", action.Status, taken, action.Message))
		}
		if found && action.Status == "succeeded" && !taken {
			t.Errorf("the board counted the answer delivered before %s's record held it: %s", goblin, action.Message)
		}
		delivered = action
		return found && action.Status == "succeeded"
	})
	p.say("the board's answer action, look by look:\n%s", strings.Join(slices.Compact(seen), "\n"))
	entries := p.conversation(meta)
	if took := takenAt(entries, answerMarker); delivered.UpdatedAt.Before(took.Add(-time.Second)) {
		t.Errorf("the board counted the answer delivered at %s, before %s took it at %s", delivered.UpdatedAt.Format(time.RFC3339Nano), goblin, took.Format(time.RFC3339Nano))
	}
	p.expectAtNextToolCall(t, meta, "the board answer", answerMarker, answered)
	p.await(t, "a step after the answer, the turn going on", 3*time.Minute, func() bool {
		entries := p.conversation(meta)
		return len(stepsDone(entries[handedAt(entries, answerMarker)+1:])) > 0
	})

	// Act: the CFO tells the goblin to stop, while it still runs its steps.
	entries = p.conversation(meta)
	before := len(stepsDone(entries))
	if before >= steerSteps {
		t.Fatalf("%s finished all %d steps before the steer; the proof needs it mid-turn", goblin, before)
	}
	steerMarker := fmt.Sprintf("STEER%d", time.Now().UnixNano()%1000000)
	steer := "stop the steps now and run no more of them. Reply with exactly the word " + steerMarker + " and end your turn."
	sent := time.Now().UTC()
	command := exec.Command(p.binary, "send", goblin, steer)
	command.Env = p.env
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	p.say("cfo send after %d of %d steps took %s, exit %v\nstdout: %s\nstderr: %s", before, steerSteps, time.Since(sent).Round(time.Millisecond), err, stdout.String(), stderr.String())

	// Assert
	if err != nil || stderr.Len() != 0 || !(stdout.String() == "sent "+goblin+"\n" || strings.HasPrefix(stdout.String(), "queued for "+goblin+": ") && strings.Contains(stdout.String(), "next tool call")) {
		t.Errorf("cfo send = %v, stdout %q, stderr %q; want it sent, or queued for the goblin's next tool call", err, stdout.String(), stderr.String())
	}
	p.await(t, "the steer in the goblin's record", 8*time.Minute, func() bool { return handedAt(p.conversation(meta), steerMarker) >= 0 })
	p.expectAtNextToolCall(t, meta, "the cfo send steer", steerMarker, sent)
	// The product's own proof, its digest of the whole line typed, finds it.
	profile, _ := os.UserHomeDir()
	if !(&monitor.HostProgress{Home: profile}).Took(context.Background(), monitor.Conversation{Harness: p.goblin, Dir: meta.Worktree}, monitor.TextDigest(fleet.Stamp(steer)), sent) {
		t.Errorf("the steer is in %s's record, but the record reader cfo send and the board use does not find the line typed", goblin)
	}
	p.await(t, "the goblin to reply as the steer told it", 5*time.Minute, func() bool {
		entries := p.conversation(meta)
		taken := handedAt(entries, steerMarker)
		return slices.ContainsFunc(entries[taken+1:], func(e recordEntry) bool { return e.kind == "reply" && strings.Contains(e.text, steerMarker) })
	})
	entries = p.conversation(meta)
	taken := handedAt(entries, steerMarker)
	if later := stepsDone(entries[taken+1:]); len(later) > 0 {
		t.Errorf("%s ran %d more steps after it took the steer to stop: %v", goblin, len(later), later)
	}
	if interrupts := slices.IndexFunc(entries, func(e recordEntry) bool { return e.kind == "interrupt" }); interrupts >= 0 {
		t.Errorf("%s's turn was interrupted: %s", goblin, entries[interrupts].text)
	}
	p.say("%s's record of the turn:\n%s", goblin, describeRecord(entries))
}

// steerSteps is how many tool calls the proof's goblin is told to make in
// its turn, more than the deliveries take to reach it.
const steerSteps = 40

// awaitEvery is await, looking every interval.
func (p *wakeProof) awaitEvery(t *testing.T, what string, within, interval time.Duration, done func() bool) {
	deadline := time.Now().Add(within)
	for !done() {
		if time.Now().After(deadline) {
			p.say("gave up waiting for %s after %s", what, within)
			t.Fatalf("gave up waiting for %s after %s; see %s", what, within, filepath.Join(p.root, "proof.log"))
		}
		time.Sleep(interval)
	}
}

// takenAt is when the goblin took the text marker names: when Claude Code
// absorbed it from its queue into the running turn, or when Codex handed it
// to the model.
func takenAt(entries []recordEntry, marker string) time.Time {
	if i := slices.IndexFunc(entries, func(e recordEntry) bool { return e.kind == "absorbed" && strings.Contains(e.text, marker) }); i >= 0 {
		return entries[i].at
	}
	if i := handedAt(entries, marker); i >= 0 {
		return entries[i].at
	}
	return time.Time{}
}

// expectAtNextToolCall checks the goblin took the text marker names at its
// next tool call after it was submitted at since: at most the one tool call
// that was running, or already chosen, then finished before the goblin's
// record shows the text handed to its model.
func (p *wakeProof) expectAtNextToolCall(t *testing.T, meta state.TaskMeta, what, marker string, since time.Time) {
	t.Helper()
	entries := p.conversation(meta)
	taken := handedAt(entries, marker)
	if taken < 0 {
		t.Fatalf("%s is not in %s's record", what, meta.ID)
	}
	var between []recordEntry
	for _, entry := range entries[:taken] {
		if entry.kind == "result" && entry.at.After(since) {
			between = append(between, entry)
		}
	}
	p.say("%s: submitted %s, taken in the record at %s after %d tool results finished in between", what, since.Format("15:04:05.000Z"), entries[taken].at.Format("15:04:05.000Z"), len(between))
	if len(between) > 1 {
		t.Errorf("%s reached %s after %d tool calls finished, want at its next tool call: %v", what, meta.ID, len(between), between)
	}
}

// endGoblin cleans up the proof's goblin, and stops its terminal's host by
// its pid when cleanup refuses it, as it refuses a goblin in a turn.
func (p *wakeProof) endGoblin(goblin string) {
	command := exec.Command(p.binary, "cleanup", goblin)
	command.Env = p.env
	output, err := command.CombinedOutput()
	p.say("cfo cleanup %s (error %v):\n%s", goblin, err, output)
	record, readErr := host.ReadRecord(p.home.State, goblin)
	if err == nil || readErr != nil {
		return
	}
	arguments, argsErr := proc.Arguments(record.HostPID)
	if argsErr != nil || len(arguments) == 0 || !strings.EqualFold(arguments[0], p.binary) || !slices.Contains(arguments, "host") || !slices.Contains(arguments, goblin) {
		p.say("leaving pid %d alone: it is not this proof's host for %s (%v)", record.HostPID, goblin, argsErr)
		return
	}
	if process, err := os.FindProcess(record.HostPID); err == nil {
		p.say("stopping %s's host pid %d: %v", goblin, record.HostPID, process.Kill())
	}
}

func boardAction(snapshot supervisor.Snapshot, id string) (supervisor.Action, bool) {
	for _, action := range snapshot.Actions {
		if action.ID == id {
			return action, true
		}
	}
	return supervisor.Action{}, false
}

// recordEntry is one entry of a harness's record of a conversation, as this
// proof reads it apart from the reader under test: text handed to the model,
// text Claude Code absorbed from its queue into a running turn, a tool's call
// or result, the agent's reply, or an interrupted turn.
type recordEntry struct {
	kind string
	at   time.Time
	text string
}

var stepDone = regexp.MustCompile(`step \d+ done`)

// stepsDone is the steps whose results entries hold.
func stepsDone(entries []recordEntry) []string {
	var steps []string
	for _, entry := range entries {
		if entry.kind == "result" {
			steps = append(steps, stepDone.FindAllString(entry.text, -1)...)
		}
	}
	return steps
}

// handedAt is the index of the first entry handing the model text that holds
// marker, or -1.
func handedAt(entries []recordEntry, marker string) int {
	return slices.IndexFunc(entries, func(e recordEntry) bool { return e.kind == "handed" && strings.Contains(e.text, marker) })
}

func describeRecord(entries []recordEntry) string {
	var lines []string
	for _, entry := range entries {
		text := strings.Join(strings.Fields(entry.text), " ")
		if len(text) > 160 {
			text = text[:160] + "..."
		}
		lines = append(lines, fmt.Sprintf("  %s %-7s %s", entry.at.Format("15:04:05.000Z"), entry.kind, text))
	}
	return strings.Join(lines, "\n")
}

// conversation reads, in file order, the record the goblin's harness keeps
// of its conversation: the newest Claude Code transcript for its worktree,
// or the Codex rollout whose opening names the worktree.
func (p *wakeProof) conversation(meta state.TaskMeta) []recordEntry {
	profile, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var files []string
	switch p.goblin {
	case "claude":
		// Claude Code cuts a folder name past 200 characters and adds a hash
		// of the path, which this proof does not reproduce.
		folder := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(meta.Worktree, "-")
		if len(folder) > 200 {
			folder = folder[:200] + "-*"
		}
		files, _ = filepath.Glob(filepath.Join(profile, ".claude", "projects", folder, "*.jsonl"))
	case "codex":
		if p.rollout != "" {
			files = []string{p.rollout}
			break
		}
		all, _ := filepath.Glob(filepath.Join(profile, ".codex", "sessions", time.Now().UTC().Format("2006"), "*", "*", "rollout-*.jsonl"))
		for _, file := range all {
			var opening struct {
				Payload struct {
					Cwd string `json:"cwd"`
				} `json:"payload"`
			}
			handle, err := os.Open(file)
			if err != nil {
				continue
			}
			decodeErr := json.NewDecoder(handle).Decode(&opening)
			handle.Close()
			if decodeErr == nil && strings.EqualFold(filepath.Clean(opening.Payload.Cwd), filepath.Clean(meta.Worktree)) {
				files = append(files, file)
			}
		}
		// A goblin keeps one rollout, which is found once.
		if len(files) == 1 {
			p.rollout = files[0]
		}
	}
	newest, written := "", time.Time{}
	for _, file := range files {
		if info, err := os.Stat(file); err == nil && info.ModTime().After(written) {
			newest, written = file, info.ModTime()
		}
	}
	if newest == "" {
		return nil
	}
	data, err := os.ReadFile(newest)
	if err != nil {
		return nil
	}
	var entries []recordEntry
	lines := bufio.NewScanner(bytes.NewReader(data))
	lines.Buffer(make([]byte, 1<<20), 64<<20)
	for lines.Scan() {
		entries = append(entries, readRecordEntry(p.goblin, lines.Bytes())...)
	}
	return entries
}

func readRecordEntry(harness string, line []byte) []recordEntry {
	var entry struct {
		Type        string    `json:"type"`
		Timestamp   time.Time `json:"timestamp"`
		IsSidechain bool      `json:"isSidechain"`
		Operation   string    `json:"operation"`
		Content     string    `json:"content"`
		Attachment  struct {
			Type   string `json:"type"`
			Prompt string `json:"prompt"`
		} `json:"attachment"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		Payload struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			Output    json.RawMessage `json:"output"`
			Arguments string          `json:"arguments"`
			Input     string          `json:"input"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.IsSidechain {
		return nil
	}
	at := entry.Timestamp
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Input   json.RawMessage `json:"input"`
		Content json.RawMessage `json:"content"`
	}
	switch harness {
	case "claude":
		switch {
		case entry.Type == "attachment" && entry.Attachment.Type == "queued_command":
			return []recordEntry{{"handed", at, entry.Attachment.Prompt}}
		case entry.Type == "queue-operation" && entry.Operation == "remove":
			return []recordEntry{{"absorbed", at, entry.Content}}
		case entry.Type == "user":
			var text string
			if json.Unmarshal(entry.Message.Content, &text) == nil {
				if strings.HasPrefix(text, "[Request interrupted") {
					return []recordEntry{{"interrupt", at, text}}
				}
				return []recordEntry{{"handed", at, text}}
			}
			var found []recordEntry
			if json.Unmarshal(entry.Message.Content, &blocks) == nil {
				for _, block := range blocks {
					if block.Type == "tool_result" {
						found = append(found, recordEntry{"result", at, string(block.Content)})
					}
				}
			}
			return found
		case entry.Type == "assistant":
			var found []recordEntry
			if json.Unmarshal(entry.Message.Content, &blocks) == nil {
				for _, block := range blocks {
					switch block.Type {
					case "tool_use":
						found = append(found, recordEntry{"call", at, string(block.Input)})
					case "text":
						found = append(found, recordEntry{"reply", at, block.Text})
					}
				}
			}
			return found
		}
	case "codex":
		if entry.Type == "event_msg" && entry.Payload.Type == "turn_aborted" {
			return []recordEntry{{"interrupt", at, "turn_aborted"}}
		}
		if entry.Type != "response_item" {
			return nil
		}
		switch entry.Payload.Type {
		case "message":
			kind := "reply"
			if entry.Payload.Role == "user" {
				kind = "handed"
			} else if entry.Payload.Role != "assistant" {
				return nil
			}
			var texts []string
			if json.Unmarshal(entry.Payload.Content, &blocks) == nil {
				for _, block := range blocks {
					texts = append(texts, block.Text)
				}
			}
			return []recordEntry{{kind, at, strings.Join(texts, "\n")}}
		case "function_call", "custom_tool_call":
			return []recordEntry{{"call", at, entry.Payload.Arguments + entry.Payload.Input}}
		case "function_call_output", "custom_tool_call_output":
			return []recordEntry{{"result", at, string(entry.Payload.Output)}}
		}
	}
	return nil
}
