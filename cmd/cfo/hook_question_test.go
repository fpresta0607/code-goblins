package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// registerCFO writes the primary.json cfo register would write for pid, a
// CFO harness in a Herdr pane.
func registerCFO(t *testing.T, homeDir string, pid int) {
	t.Helper()
	entries, err := proc.Ancestry(pid, 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("start time of pid %d is unreadable: %v", pid, err)
	}
	start := entries[0].Start
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	record := map[string]any{
		"target":    map[string]string{"Session": "default", "Pane": "w1:p0"},
		"workspace": "w1",
		"tab":       "w1:t0",
		"agent":     "claude",
		"terminal":  "term_1",
		"process":   lock.Info{PID: pid, OwnerPID: pid, Start: start, Hostname: hostname, Acquired: time.Now().UTC()},
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", "primary.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The 2026-09-25 incident: the CFO waited on an AskUserQuestion selector the
// Overlord could only see in its terminal, and the Command Center showed
// nothing while supervision stood still.
func TestRunHookPretoolSubagentRefusesTheRegisteredCFOsNativeQuestion(t *testing.T) {
	homeDir := newPrimaryHome(t)
	registerCFO(t, homeDir, os.Getpid())
	t.Setenv("CFO_ALLOW_SUBAGENT", "1")

	var stdout, stderr bytes.Buffer
	exit := runHook("pretool-subagent", strings.NewReader(`{"session_id":"s","tool_name":"AskUserQuestion"}`), &stdout, &stderr)

	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", exit, stderr.String())
	}
	var envelope struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal(bytes.TrimRight(stderr.Bytes(), "\n"), &envelope); err != nil {
		t.Fatalf("stderr is not the deny envelope: %v\nstderr=%s", err, stderr.String())
	}
	if envelope.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("decision = %q, want deny", envelope.HookSpecificOutput.PermissionDecision)
	}
	for _, want := range []string{"AskUserQuestion", "cfo question", "cfo run-request"} {
		if !strings.Contains(envelope.SystemMessage, want) {
			t.Errorf("systemMessage %q lacks %q", envelope.SystemMessage, want)
		}
	}
}

// Every other session keeps its native prompt: it has no board to publish a
// question through, so refusing it would leave it no way to ask at all.
func TestRunHookPretoolSubagentLetsOtherSessionsAskNatively(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, homeDir string)
	}{
		{"no CFO is registered", func(*testing.T, string) {}},
		{"another session is the registered CFO", func(t *testing.T, homeDir string) {
			other := exec.Command("ping", "-n", "30", "127.0.0.1")
			if err := other.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = other.Process.Kill()
				_, _ = other.Process.Wait()
			})
			registerCFO(t, homeDir, other.Process.Pid)
		}},
		{"a goblin under the registered CFO's process", func(t *testing.T, homeDir string) {
			registerCFO(t, homeDir, os.Getpid())
			t.Setenv(harness.RoleVariable, harness.RoleGoblin)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := newPrimaryHome(t)
			tc.setup(t, homeDir)

			var stdout, stderr bytes.Buffer
			exit := runHook("pretool-subagent", strings.NewReader(`{"session_id":"s","tool_name":"AskUserQuestion"}`), &stdout, &stderr)

			if exit != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want a silent pass", exit, stdout.String(), stderr.String())
			}
		})
	}
}
