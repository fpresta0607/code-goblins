package claudehook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDenyPreToolEnvelope(t *testing.T) {
	var buf bytes.Buffer
	code := DenyPreTool(&buf, "blocked: reason")
	if code != 2 {
		t.Errorf("DenyPreTool return = %d, want 2", code)
	}
	trimmed := strings.TrimRight(buf.String(), "\n")
	if strings.Count(trimmed, "\n") != 0 {
		t.Fatalf("stderr is not exactly one line: %q", buf.String())
	}
	var envelope struct {
		HookSpecificOutput struct {
			HookEventName      string `json:"hookEventName"`
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		t.Fatalf("stderr line is not valid JSON: %v", err)
	}
	if envelope.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q, want %q", envelope.HookSpecificOutput.HookEventName, "PreToolUse")
	}
	if envelope.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("permissionDecision = %q, want %q", envelope.HookSpecificOutput.PermissionDecision, "deny")
	}
	if envelope.SystemMessage != "blocked: reason" {
		t.Errorf("systemMessage = %q, want %q", envelope.SystemMessage, "blocked: reason")
	}
}

func TestDenyPreToolEscapesMessage(t *testing.T) {
	var buf bytes.Buffer
	message := "quote \" and newline \n end"
	code := DenyPreTool(&buf, message)
	if code != 2 {
		t.Errorf("DenyPreTool return = %d, want 2", code)
	}
	trimmed := strings.TrimRight(buf.String(), "\n")
	if strings.Count(trimmed, "\n") != 0 {
		t.Fatalf("stderr is not exactly one line after escaping: %q", buf.String())
	}
	var envelope struct {
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		t.Fatalf("stderr line is not valid JSON: %v", err)
	}
	if envelope.SystemMessage != message {
		t.Errorf("systemMessage round-trip = %q, want %q", envelope.SystemMessage, message)
	}
}

// Codex reads a hook's refusal as JSON on stdout from a hook that exits 0,
// since the session's PowerShell it runs the hook in ends with 1 for every
// other exit code; the reason comes back whole, quotes and line breaks too.
func TestDenyPreToolOnStdoutEnvelope(t *testing.T) {
	for _, message := range []string{"blocked: reason", "quote \" and newline \n end"} {
		t.Run(message, func(t *testing.T) {
			// Arrange
			var buf bytes.Buffer

			// Act
			code := DenyPreToolOnStdout(&buf, message)

			// Assert
			if code != 0 {
				t.Errorf("DenyPreToolOnStdout return = %d, want 0", code)
			}
			trimmed := strings.TrimRight(buf.String(), "\n")
			if strings.Count(trimmed, "\n") != 0 {
				t.Fatalf("stdout is not exactly one line: %q", buf.String())
			}
			var envelope struct {
				HookSpecificOutput struct {
					HookEventName            string `json:"hookEventName"`
					PermissionDecision       string `json:"permissionDecision"`
					PermissionDecisionReason string `json:"permissionDecisionReason"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
				t.Fatalf("stdout line is not valid JSON: %v", err)
			}
			if got := envelope.HookSpecificOutput; got.HookEventName != "PreToolUse" || got.PermissionDecision != "deny" || got.PermissionDecisionReason != message {
				t.Errorf("hookSpecificOutput = %+v, want a PreToolUse deny with reason %q", got, message)
			}
		})
	}
}

func TestBlockStopWritesBannerVerbatim(t *testing.T) {
	var buf bytes.Buffer
	banner := "Stop blocked: the CFO must review before ending the turn."
	code := BlockStop(&buf, banner)
	if code != 2 {
		t.Errorf("BlockStop return = %d, want 2", code)
	}
	if got := strings.TrimRight(buf.String(), "\n"); got != banner {
		t.Errorf("stderr = %q, want %q", got, banner)
	}
}

func TestInfoAllowWritesSystemMessage(t *testing.T) {
	var buf bytes.Buffer
	code := InfoAllow(&buf, "session ready")
	if code != 0 {
		t.Errorf("InfoAllow return = %d, want 0", code)
	}
	var envelope struct {
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if envelope.SystemMessage != "session ready" {
		t.Errorf("systemMessage = %q, want %q", envelope.SystemMessage, "session ready")
	}
}
