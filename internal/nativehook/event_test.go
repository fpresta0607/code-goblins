package nativehook

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNativeContracts(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ harness, event, want string }{
		{"claude", "SessionStart", "started"}, {"claude", "Stop", "settled"},
		{"codex", "Stop", "settled"}, {"codex", "SessionEnd", "ended"},
		{"codex", "UserPromptSubmit", "active"}, {"pi", "agent_settled", "settled"},
		{"pi", "session_shutdown", "ended"},
	} {
		t.Run(tc.harness+tc.event, func(t *testing.T) {
			input, _ := json.Marshal(map[string]string{"session_id": "session-1", "cwd": t.TempDir(), "hook_event_name": tc.event, "turn_id": "turn-1"})
			e, err := Normalize(bytes.NewReader(input), Context{Harness: tc.harness, Role: "goblin", TaskID: "task-1", Generation: "spawn-1", Now: now})
			if err != nil {
				t.Fatal(err)
			}
			if e.Kind != tc.want || e.Schema != 1 || e.SessionID != "session-1" || e.TaskID != "task-1" || e.TurnID != "turn-1" || e.Generation != "spawn-1" {
				t.Fatalf("event = %+v", e)
			}
			stateDir := t.TempDir()
			if err := Spool(stateDir, e); err != nil {
				t.Fatal(err)
			}
			if err := Spool(stateDir, e); err != nil {
				t.Fatal(err)
			}
			files, _ := os.ReadDir(SpoolDir(stateDir))
			if len(files) != 1 {
				t.Fatalf("replayed event created %d files", len(files))
			}
			data, _ := os.ReadFile(filepath.Join(SpoolDir(stateDir), files[0].Name()))
			var roundTrip Event
			if err := json.Unmarshal(data, &roundTrip); err != nil || roundTrip != e {
				t.Fatalf("persisted event: %s, %v", data, err)
			}
		})
	}
}

func TestHookRejectsMalformedAndPrematureEvents(t *testing.T) {
	for _, input := range []string{
		`{`, `{ "session_id": "s", "cwd": "relative", "hook_event_name": "Stop" }`,
		`{"session_id":"s","cwd":"C:\\project","hook_event_name":"agent_end"}`,
		`{"session_id":"s","cwd":"C:\\project","hook_event_name":"agent_settled","pending_messages":true}`,
		strings.Repeat("x", 1<<17),
	} {
		if _, err := Normalize(strings.NewReader(input), Context{Harness: "pi", Role: "cfo", Now: time.Now()}); err == nil {
			t.Fatalf("accepted %q", input[:min(len(input), 100)])
		}
	}
}

// A value the hook skips is still checked, so malformed JSON anywhere in the
// input fails as json.Unmarshal failed it.
func TestHookRejectsMalformedValuesItSkips(t *testing.T) {
	cwd, _ := json.Marshal(t.TempDir())
	input := func(response string) string {
		return `{"session_id":"s","cwd":` + string(cwd) + `,"tool_response":` + response + `,"hook_event_name":"PostToolUse"}`
	}
	for _, response := range []string{
		`{"a":[1,-2.5e+3,0,true,false,null,"\u00e9\n\""]}`,
		strings.Repeat("[", maxInputDepth-1) + "1" + strings.Repeat("]", maxInputDepth-1),
	} {
		if _, err := Normalize(strings.NewReader(input(response)), Context{Harness: "claude", Now: time.Now()}); err != nil {
			t.Fatalf("well-formed tool response %.40q refused: %v", response, err)
		}
	}
	for _, response := range []string{
		`{"a":tru}`, `"\x"`, `"\u00g0"`, "\"a\tb\"", `01`, `1.`, `-`, `1e`, `[1,]`, `{"a"}`, `{"a":1,}`, `[1 2]`, `{"a":1`,
		strings.Repeat("[", maxInputDepth+1) + strings.Repeat("]", maxInputDepth+1),
	} {
		if _, err := Normalize(strings.NewReader(input(response)), Context{Harness: "claude", Now: time.Now()}); err == nil {
			t.Errorf("accepted tool response %.40q", response)
		}
	}
	if _, err := Normalize(strings.NewReader(input(`1`)+` {}`), Context{Harness: "claude", Now: time.Now()}); err == nil {
		t.Error("accepted data after the object")
	}
}

// Claude Code's PostToolUse input carries the tool's whole response: a Bash
// call's output, an Edit's original file, a Read's content. A response past
// 64 KiB, with protocol fields both before and after it, still spools them.
func TestALargeToolResponseKeepsItsProtocolFields(t *testing.T) {
	large := strings.Repeat(`line \"quoted\" \\ \u00e9 \t tab\n`, 1<<12)
	for _, tc := range []struct{ tool, input, response string }{
		{"Bash", `{"command":"cat big.log"}`, `{"stdout":"` + large + `","stderr":"","interrupted":false,"isImage":false}`},
		{"Edit", `{"file_path":"C:\\big.md","old_string":"a","new_string":"b"}`, `{"filePath":"C:\\big.md","oldString":"a","newString":"b","originalFile":"` + large + `","structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":["-a","+b"]}],"userModified":false,"replaceAll":false}`},
		{"Read", `{"file_path":"C:\\big.md"}`, `{"type":"text","file":{"filePath":"C:\\big.md","content":"` + large + `","numLines":4096,"startLine":1,"totalLines":4096}}`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			dir := t.TempDir()
			cwd, _ := json.Marshal(dir)
			input := `{"session_id":"session-1","transcript_path":"C:\\t.jsonl","cwd":` + string(cwd) + `,"tool_name":"` + tc.tool + `","tool_input":` + tc.input + `,"tool_response":` + tc.response + `,"hook_event_name":"PostToolUse","model":"model-1"}`
			if len(input) <= 64<<10 {
				t.Fatalf("input is only %d bytes", len(input))
			}

			e, err := Normalize(strings.NewReader(input), Context{Harness: "claude", Role: "goblin", TaskID: "task-1", Now: time.Now()})

			if err != nil {
				t.Fatal(err)
			}
			if e.Kind != "active" || e.SessionID != "session-1" || e.CWD != dir || e.Model != "model-1" {
				t.Fatalf("event = %+v", e)
			}
			stateDir := t.TempDir()
			if err := Spool(stateDir, e); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(SpoolDir(stateDir), e.ID+".event.json"))
			if err != nil || strings.Contains(string(data), "quoted") {
				t.Fatalf("spooled %.200s, %v", data, err)
			}
		})
	}
}

// The hook streams a tool response past rather than holding it.
func TestAToolResponseIsNeverHeldWhole(t *testing.T) {
	const size = 16 << 20
	cwd, _ := json.Marshal(t.TempDir())
	input := io.MultiReader(
		strings.NewReader(`{"session_id":"s","cwd":`+string(cwd)+`,"tool_response":{"stdout":"`),
		io.LimitReader(repeatedByte('x'), size),
		strings.NewReader(`"},"hook_event_name":"PostToolUse"}`),
	)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	_, err := Normalize(input, Context{Harness: "claude", Now: time.Now()})

	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > size/16 {
		t.Fatalf("reading a %d-byte response allocated %d bytes", size, allocated)
	}
}

type repeatedByte byte

func (b repeatedByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

func TestHookDoesNotPersistPromptOrToolCredentials(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"session_id": "s", "cwd": t.TempDir(), "hook_event_name": "PostToolUse", "tool_response": "secret", "last_assistant_message": "secret"})
	e, err := Normalize(bytes.NewReader(input), Context{Harness: "codex", Role: "cfo", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(e)
	if strings.Contains(string(data), "secret") {
		t.Fatalf("payload leaked: %s", data)
	}
}

func TestStopInvocationsHaveIndependentIdentityWithinOneTurn(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"session_id": "s", "cwd": t.TempDir(), "hook_event_name": "Stop", "turn_id": "turn-1"})
	c := Context{Harness: "codex", Role: "cfo", Now: time.Now()}
	a, err := Normalize(bytes.NewReader(input), c)
	if err != nil {
		t.Fatal(err)
	}
	c.Now = c.Now.Add(time.Second)
	b, err := Normalize(bytes.NewReader(input), c)
	if err != nil || a.ID == b.ID {
		t.Fatalf("separate invocations collapsed: %s %s %v", a.ID, b.ID, err)
	}
}

// A harness taking a prompt for a turn marks its active event, the proof a
// delivery was accepted: Claude and Codex as they submit it, Pi as its agent
// starts. Tool events are activity without a prompt.
func TestAPromptTakenForATurnIsMarked(t *testing.T) {
	for _, tc := range []struct {
		harness, event string
		prompt         bool
	}{
		{"claude", "UserPromptSubmit", true},
		{"codex", "UserPromptSubmit", true},
		{"pi", "agent_start", true},
		{"claude", "PreToolUse", false},
		{"claude", "PostToolUse", false},
		{"pi", "tool_execution_end", false},
		{"claude", "Stop", false},
	} {
		t.Run(tc.harness+tc.event, func(t *testing.T) {
			input, _ := json.Marshal(map[string]string{"session_id": "session-1", "cwd": t.TempDir(), "hook_event_name": tc.event})

			e, err := Normalize(bytes.NewReader(input), Context{Harness: tc.harness, Role: "goblin", TaskID: "task-1", Generation: "spawn-1", Now: time.Now()})

			if err != nil {
				t.Fatal(err)
			}
			if e.Prompt != tc.prompt {
				t.Errorf("prompt = %v, want %v", e.Prompt, tc.prompt)
			}
		})
	}
}

// An event records the native terminal its harness runs in, so a delivery
// into that terminal can be proven by the harness's own hook; a harness in no
// native terminal records none.
func TestAnEventRecordsTheNativeTerminalItRunsIn(t *testing.T) {
	for _, hostID := range []string{"cfo", ""} {
		input, _ := json.Marshal(map[string]string{"session_id": "session-1", "cwd": t.TempDir(), "hook_event_name": "UserPromptSubmit"})

		e, err := Normalize(bytes.NewReader(input), Context{Harness: "claude", HostID: hostID, Now: time.Now()})

		if err != nil {
			t.Fatal(err)
		}
		if e.HostID != hostID {
			t.Errorf("host = %q, want %q", e.HostID, hostID)
		}
	}
}

// Only an active event can carry a prompt.
func TestAPromptOnAnyOtherKindIsRefused(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"session_id": "session-1", "cwd": t.TempDir(), "hook_event_name": "Stop"})
	e, err := Normalize(bytes.NewReader(input), Context{Harness: "claude", Role: "goblin", TaskID: "task-1", Generation: "spawn-1", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	e.Prompt = true

	if err := e.Validate(); err == nil || !strings.Contains(err.Error(), "only an active event") {
		t.Errorf("Validate = %v, want a settled event with a prompt refused", err)
	}
}
