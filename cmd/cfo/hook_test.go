package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newPrimaryHome creates AGENTS.md, state/, and a plain git checkout in a
// temp dir, sets CFO_HOME to it, and returns the dir.
func newPrimaryHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# home"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Setenv("CFO_HOME", dir)
	return dir
}

func TestRunHookPretoolSubagentDeniesInPrimaryHome(t *testing.T) {
	newPrimaryHome(t)
	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-subagent", strings.NewReader(`{"session_id":"s","tool_name":"Agent"}`), &stdout, &stderr)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", exit, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	var envelope struct {
		HookSpecificOutput struct {
			HookEventName      string `json:"hookEventName"`
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
		SystemMessage string `json:"systemMessage"`
	}
	trimmed := strings.TrimRight(stderr.String(), "\n")
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		t.Fatalf("stderr is not the deny envelope: %v\nstderr=%s", err, stderr.String())
	}
	if envelope.HookSpecificOutput.HookEventName != "PreToolUse" || envelope.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("envelope = %+v, want PreToolUse/deny", envelope)
	}
	if !strings.Contains(envelope.SystemMessage, `delegation-shaped on "agent"`) {
		t.Errorf("systemMessage = %q, want it to contain %q", envelope.SystemMessage, `delegation-shaped on "agent"`)
	}
}

func TestRunHookPretoolSubagentInertWithoutState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# home"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Setenv("CFO_HOME", dir)

	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-subagent", strings.NewReader(`{"session_id":"s","tool_name":"Agent"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookPretoolSubagentEscapeHatch(t *testing.T) {
	newPrimaryHome(t)
	t.Setenv("CFO_ALLOW_SUBAGENT", "1")
	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-subagent", strings.NewReader(`{"session_id":"s","tool_name":"Agent"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookPretoolSubagentMCPPassthrough(t *testing.T) {
	newPrimaryHome(t)
	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-subagent", strings.NewReader(`{"session_id":"s","tool_name":"mcp__x__spawn"}`), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookPretoolSubagentTransportFailure(t *testing.T) {
	newPrimaryHome(t)
	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-subagent", strings.NewReader("garbage"), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
}

func TestRunHookUnknownName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runHook("no-such", strings.NewReader(""), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown hook") {
		t.Errorf("stderr = %q, want it to mention unknown hook", stderr.String())
	}
}
