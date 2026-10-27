package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestRunSpawnMovesNewWorkBeforeSessionOrWeekIsSpent(t *testing.T) {
	for _, window := range []string{"session", "weekly"} {
		t.Run(window, func(t *testing.T) {
			// Arrange
			h, runtime := routedRuntime(t, shippedRoutingJSON(t))
			got := captureSpawn(&runtime, "spawned routed")
			now := time.Now().UTC()
			runtime.now = func() time.Time { return now }
			runtime.quota = func(context.Context) (quota.Report, string) {
				return quota.Report{GeneratedAt: now, Providers: map[string]quota.Provider{
					"codex": {Known: true, RefreshedAt: now, Windows: []quota.Window{{ID: window, Kind: window, PercentUsed: 91, ResetsAt: now.Add(time.Hour)}}, Scopes: map[string]quota.Scope{
						"all_models": {Name: "all_models", Known: true, PercentRemaining: 9, BoundedBy: []string{window}},
					}},
				}}, ""
			}
			var stdout, stderr bytes.Buffer
			// Act
			exit := runWithRuntime([]string{"spawn", "routed", "--project", `C:\project`, "--brief", briefWith(t, "Fix a security vulnerability.")}, &stdout, &stderr, runtime)
			// Assert
			if exit != 0 || string(got.Harness) != "claude" || got.Model != "claude-opus-5-5" {
				t.Fatalf("exit=%d request=%+v stdout=%s stderr=%s", exit, got, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), "wanted=deep") || !strings.Contains(stdout.String(), "start floor") {
				t.Fatalf("the CFO was not told why routing moved: %s", stdout.String())
			}
			notices, err := wake.Pending(h.State)
			if err != nil || len(notices) != 1 || !strings.Contains(notices[0].Detail, "start floor") {
				t.Fatalf("the CFO has no durable fallback notice: %+v, %v", notices, err)
			}
		})
	}
}

func TestRunSpawnKeepsVerificationHelpersIndependent(t *testing.T) {
	for _, test := range []struct {
		name, task, parentModel string
		flags                   []string
		wantModel               string
		wantExit                int
	}{
		{"route verifier", "Verify the completed implementation.", "gpt-6-sol", nil, "gpt-6-astra", 0},
		{"accept same explicit model", "Verify the completed implementation.", "gpt-6-sol", []string{"--harness", "codex", "--model", "gpt-6-sol"}, "gpt-6-sol", 0},
		{"refuse unknown explicit model", "Verify the completed implementation.", "gpt-6-sol", []string{"--harness", "codex"}, "", 1},
		{"refuse unknown parent", "Verify the completed implementation.", "default", nil, "", 1},
		{"implementation checklist", "Implement a settings page.\n\n## Acceptance criteria\nVerify the page works.", "default", nil, "gpt-6-astra", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, runtime := routedRuntime(t, shippedRoutingJSON(t))
			if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "parent", Harness: "codex", Model: test.parentModel}); err != nil {
				t.Fatal(err)
			}
			got := captureSpawn(&runtime, "spawned child")
			args := append([]string{"spawn", "child", "--project", `C:\project`, "--brief", briefWith(t, test.task), "--parent", "parent"}, test.flags...)
			var stdout, stderr bytes.Buffer
			exit := runWithRuntime(args, &stdout, &stderr, runtime)
			if exit != test.wantExit || got.Model != test.wantModel {
				t.Fatalf("exit=%d request=%+v stderr=%s", exit, got, stderr.String())
			}
		})
	}
}
