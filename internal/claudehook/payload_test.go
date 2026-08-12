package claudehook

import (
	"strings"
	"testing"
)

func TestReadPayloadPreToolUse(t *testing.T) {
	p, ok := ReadPayload(strings.NewReader(`{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"echo hi"}}`))
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := Payload{SessionID: "s1", ToolName: "Bash", Command: "echo hi"}
	if p != want {
		t.Errorf("ReadPayload = %+v, want %+v", p, want)
	}
}

func TestReadPayloadSessionStart(t *testing.T) {
	p, ok := ReadPayload(strings.NewReader(`{"session_id":"s2","source":"startup"}`))
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := Payload{SessionID: "s2", Source: "startup"}
	if p != want {
		t.Errorf("ReadPayload = %+v, want %+v", p, want)
	}
}

func TestReadPayloadStopCamelCaseWins(t *testing.T) {
	p, ok := ReadPayload(strings.NewReader(`{"session_id":"s3","stopHookActive":true,"stop_hook_active":false}`))
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := Payload{SessionID: "s3", StopHookActive: true}
	if p != want {
		t.Errorf("ReadPayload = %+v, want %+v", p, want)
	}
}

func TestReadPayloadStopSnakeFallback(t *testing.T) {
	p, ok := ReadPayload(strings.NewReader(`{"stop_hook_active":true}`))
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := Payload{StopHookActive: true}
	if p != want {
		t.Errorf("ReadPayload = %+v, want %+v", p, want)
	}
}

func TestReadPayloadEmptyInput(t *testing.T) {
	_, ok := ReadPayload(strings.NewReader(""))
	if ok {
		t.Error("ok = true, want false on empty input")
	}
}

func TestReadPayloadGarbageInput(t *testing.T) {
	_, ok := ReadPayload(strings.NewReader("not json"))
	if ok {
		t.Error("ok = true, want false on non-JSON input")
	}
}

func TestReadPayloadJSONArray(t *testing.T) {
	_, ok := ReadPayload(strings.NewReader("[1]"))
	if ok {
		t.Error("ok = true, want false on a JSON array (not an object)")
	}
}

func TestReadPayloadMissingToolInput(t *testing.T) {
	p, ok := ReadPayload(strings.NewReader(`{"session_id":"s4","tool_name":"Bash"}`))
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := Payload{SessionID: "s4", ToolName: "Bash"}
	if p != want {
		t.Errorf("ReadPayload = %+v, want %+v", p, want)
	}
}
