package claudestream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// fixtureLine is one line of testdata/spike.jsonl: a line Claude Code 2.1.312
// wrote ("out") or the spike wrote to it ("in"), recorded on 2026-10-28 with
// the account and its paths taken out.
type fixtureLine struct {
	Direction string          `json:"dir"`
	Line      json.RawMessage `json:"line"`
}

func readFixture(t *testing.T) []fixtureLine {
	t.Helper()
	file, err := os.Open("testdata/spike.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var lines []fixtureLine
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// describe is what the chat shows of a session: whether it works, the kinds
// of its live blocks, the tools that wait, and how its last turn ended.
func describe(session Session) string {
	var live, asks []string
	for _, block := range session.Live {
		live = append(live, block.Kind)
	}
	for _, ask := range session.Asks {
		asks = append(asks, ask.Tool)
	}
	result := "none"
	if session.Result != nil {
		result = session.Result.Subtype + "/" + session.Result.StopReason
	}
	return fmt.Sprintf("working %v, live %v, asks %v, result %s", session.IsWorking, live, asks, result)
}

// The spike's session read line by line: a plain turn, a Bash call allowed
// with a line of his taken mid-turn, an AskUserQuestion answered, /effort as
// a line, and a Bash call stopped by interrupt.
func TestASessionFollowsTheSpikesTurns(t *testing.T) {
	// Arrange
	var session Session
	var trace []string
	shown := describe(session)
	isReadyShownLive := false

	// Act
	for _, line := range readFixture(t) {
		if err := session.Apply(line.Line, line.Direction == "in"); err != nil {
			t.Fatalf("Apply %s: %v", line.Line, err)
		}
		if now := describe(session); now != shown {
			trace, shown = append(trace, now), now
		}
		isReadyShownLive = isReadyShownLive || len(session.Live) == 1 && session.Live[0].Text == "ready"
	}

	// Assert
	want := []string{
		"working true, live [], asks [], result none",
		"working true, live [text], asks [], result none",
		"working true, live [], asks [], result none",
		"working false, live [], asks [], result success/end_turn",
		"working true, live [], asks [], result success/end_turn",
		"working true, live [], asks [Bash], result success/end_turn",
		"working true, live [], asks [], result success/end_turn",
		"working true, live [text], asks [], result success/end_turn",
		"working true, live [], asks [], result success/end_turn",
		"working false, live [], asks [], result success/end_turn",
		"working true, live [], asks [], result success/end_turn",
		"working true, live [], asks [AskUserQuestion], result success/end_turn",
		"working true, live [], asks [], result success/end_turn",
		"working true, live [text], asks [], result success/end_turn",
		"working true, live [], asks [], result success/end_turn",
		"working false, live [], asks [], result success/end_turn",
		"working false, live [], asks [], result success/",
		"working true, live [], asks [], result success/",
		"working true, live [], asks [Bash], result success/",
		"working true, live [], asks [], result success/",
		"working false, live [], asks [], result error_during_execution/tool_use",
	}
	if !slices.Equal(trace, want) {
		t.Errorf("the session went\n%s\nwant\n%s", strings.Join(trace, "\n"), strings.Join(want, "\n"))
	}
	if !isReadyShownLive {
		t.Error("the first reply's words never showed live before its whole block")
	}
	if session.ID != "9968aeee-6a2f-4a72-8625-51eccea25c44" || session.Model != "claude-haiku-5-5" || session.Version != "2.1.312" || !slices.Contains(session.Capabilities, "interrupt_send_now_v1") {
		t.Errorf("init read as %q %q %q %v", session.ID, session.Model, session.Version, session.Capabilities)
	}
	if !session.Result.IsError || session.Result.Usage.OutputTokens != 90 || session.Result.CostUSD != 0.00414923 {
		t.Errorf("the interrupted turn's result read as %+v", *session.Result)
	}
	if len(session.Unknown) != 0 {
		t.Errorf("the reader did not know %v", session.Unknown)
	}
}

// A Bash call's ask carries its tool, call and command, as the board's card
// shows them.
func TestAnAskCarriesItsToolCallAndInput(t *testing.T) {
	// Arrange
	var session Session

	// Act
	for _, line := range readFixture(t) {
		if line.Direction == "out" && strings.Contains(string(line.Line), `"can_use_tool"`) {
			if err := session.Apply(line.Line, false); err != nil {
				t.Fatal(err)
			}
			break
		}
	}

	// Assert
	if len(session.Asks) != 1 {
		t.Fatalf("asks = %+v, want the first Bash call", session.Asks)
	}
	ask := session.Asks[0]
	if ask.RequestID == "" || ask.Tool != "Bash" || !strings.HasPrefix(ask.ToolUseID, "toolu_") || ask.Description != "Ping localhost 8 times" || !strings.Contains(string(ask.Input), `"command":"ping -n 8 127.0.0.1"`) {
		t.Errorf("ask = %+v", ask)
	}
}

func TestASessionReadsWhatTheSpikeDidNotShow(t *testing.T) {
	const message = `"api_message_id":"msg_1","parent_tool_use_id":null`
	for _, testCase := range []struct {
		name  string
		lines []string
		want  func(session Session) bool
	}{
		{"thinking shows live as thinking", []string{
			`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}},` + message + `}`,
			`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Check the "}},` + message + `}`,
			`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"brief."}},` + message + `}`,
		}, func(session Session) bool {
			return len(session.Live) == 1 && session.Live[0] == LiveBlock{MessageID: "msg_1", Index: 0, Kind: "thinking", Text: "Check the brief."}
		}},
		{"words whose block start was not read still show", []string{
			`{"type":"stream_event","event":{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"late"}},` + message + `}`,
		}, func(session Session) bool {
			return len(session.Live) == 1 && session.Live[0] == LiveBlock{MessageID: "msg_1", Index: 2, Kind: "text", Text: "late"}
		}},
		{"a whole block drops its live block of that kind alone", []string{
			`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}},` + message + `}`,
			`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"text"}},` + message + `}`,
			`{"type":"assistant","message":{"id":"msg_1","content":[{"type":"thinking","thinking":"Done."}]},"parent_tool_use_id":null}`,
		}, func(session Session) bool {
			return len(session.Live) == 1 && session.Live[0].Kind == "text"
		}},
		{"a sub-agent's words stay out of the reply", []string{
			`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"inner"}},"api_message_id":"msg_2","parent_tool_use_id":"toolu_1"}`,
		}, func(session Session) bool { return len(session.Live) == 0 }},
		{"a withdrawn ask is gone", []string{
			`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{}}}`,
			`{"type":"control_request","request_id":"r2","request":{"subtype":"can_use_tool","tool_name":"Edit","input":{}}}`,
			`{"type":"control_cancel_request","request_id":"r1"}`,
		}, func(session Session) bool { return len(session.Asks) == 1 && session.Asks[0].RequestID == "r2" }},
		{"a turn's end drops the asks it left", []string{
			`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{}}}`,
			`{"type":"result","subtype":"error_during_execution","is_error":true}`,
		}, func(session Session) bool {
			return len(session.Asks) == 0 && session.Result.Subtype == "error_during_execution"
		}},
		{"unknown lines and requests are named once each", []string{
			`{"type":"brand_new"}`,
			`{"type":"control_request","request_id":"r1","request":{"subtype":"elicitation"}}`,
			`{"type":"brand_new"}`,
			`{"type":"system","subtype":"compact_boundary"}`,
		}, func(session Session) bool {
			return slices.Equal(session.Unknown, []string{"brand_new", "control_request elicitation"}) && len(session.Asks) == 0
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			var session Session

			// Act
			for _, line := range testCase.lines {
				if err := session.Apply(json.RawMessage(line), false); err != nil {
					t.Fatalf("Apply %s: %v", line, err)
				}
			}

			// Assert
			if !testCase.want(session) {
				t.Errorf("session = %+v", session)
			}
		})
	}
}

// A line the board wrote changes nothing but the ask it answered: his line
// works only once Claude Code echoes it.
func TestALineTheBoardWroteAnswersItsAskAlone(t *testing.T) {
	// Arrange
	var session Session
	if err := session.Apply(json.RawMessage(`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{}}}`), false); err != nil {
		t.Fatal(err)
	}

	// Act
	userErr := session.Apply(UserLine("go on"), true)
	isWorkingAfterHisLine := session.IsWorking
	answerErr := session.Apply(DenyLine("r1", "Not now."), true)

	// Assert
	if userErr != nil || answerErr != nil {
		t.Fatalf("Apply = %v, %v", userErr, answerErr)
	}
	if isWorkingAfterHisLine {
		t.Error("his line written worked before Claude Code took it")
	}
	if len(session.Asks) != 0 {
		t.Errorf("asks = %+v after the board answered", session.Asks)
	}
}

// A Claude Code that dies mid-turn, an ask waiting and words half written,
// leaves no turn working.
func TestASessionWhoseClaudeCodeDiesMidTurnStopsWorking(t *testing.T) {
	// Arrange
	var session Session
	for _, line := range []string{
		`{"type":"user","message":{"role":"user","content":"go"},"parent_tool_use_id":null,"isReplay":true}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Half"}},"api_message_id":"msg_1","parent_tool_use_id":null}`,
		`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{}}}`,
	} {
		if err := session.Apply(json.RawMessage(line), false); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	session.Exit(1)

	// Assert
	if !session.IsExited || session.ExitCode != 1 || session.IsWorking || len(session.Live) != 0 || len(session.Asks) != 0 {
		t.Errorf("session = %+v, want it ended with code 1 and nothing left working", session)
	}
}

func TestApplyRefusesALineThatIsNotJSON(t *testing.T) {
	// Arrange
	var session Session

	// Act
	err := session.Apply(json.RawMessage(`not json`), false)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "claudestream: read a 8-byte line") {
		t.Errorf("Apply = %v, want an error naming the line's size", err)
	}
}
