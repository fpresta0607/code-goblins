package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestBacklogAcceptTakesADeliveryOnlyFromTheRegisteredPrimaryCEO(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CEO registration is verified on Windows only")
	}
	const unauthorized = "registered primary CEO"
	for _, test := range []struct {
		name, registered, variable, value, refusal string
		isLive, isNotPrimary                       bool
	}{
		{name: "the registered CEO after cleanup", registered: "ceo"},
		{name: "the registered CEO before cleanup", registered: "ceo", isLive: true},
		{name: "an unregistered caller", refusal: unauthorized},
		{name: "another registration", registered: "other", refusal: unauthorized},
		{name: "a goblin", registered: "ceo", variable: harness.RoleVariable, value: harness.RoleGoblin, refusal: unauthorized},
		{name: "a gate agent", registered: "ceo", variable: gateAgentVariable, value: "1", refusal: unauthorized},
		{name: "a home that is not primary", registered: "ceo", isNotPrimary: true, refusal: "not a primary home"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := primaryHomeFixture(t)
			if test.isNotPrimary {
				if err := os.Remove(filepath.Join(h.Root, home.InstalledMarker)); err != nil {
					t.Fatal(err)
				}
			}
			outsideGoblinTerminal(t)
			if test.registered != "" {
				registeredPrimary(t, h, test.registered)
			}
			if test.variable != "" {
				t.Setenv(test.variable, test.value)
			}
			installed := filepath.Join(h.Root, "bin", "tool.exe")
			digest := strings.Repeat("e", 64)
			receipt, err := json.Marshal(state.InstallationReceipt{Task: "install", Generation: "s1", Image: state.InstalledImage{Path: installed, SHA256: digest}, Daemon: state.InstalledDaemon{PID: 7, Started: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)}})
			if err != nil {
				t.Fatal(err)
			}
			report := filepath.Join(h.Data, "install", "report.md")
			saved := filepath.Join(h.Data, "install", "receipt.json")
			backlog := filepath.Join(h.Data, "backlog.md")
			source := "## Queued\n- [ ] install - Install the tool\n\n## Done\n"
			if err := os.MkdirAll(filepath.Dir(report), 0o700); err != nil {
				t.Fatal(err)
			}
			for path, content := range map[string]string{report: "Installed and verified.\n", saved: string(receipt), backlog: source} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			retired := state.Outcome{ID: "install", Generation: "s1", Phase: "stopped", Reason: "Worktree returned by cleanup", At: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
			if test.isLive {
				if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "install", SpawnGen: "s1"}); err != nil {
					t.Fatal(err)
				}
				retired = state.Outcome{}
			} else if err := state.WriteOutcome(h.State, retired); err != nil {
				t.Fatal(err)
			}
			commands := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }, installation: fleet.Installation{
				Hash:        func(string) (string, error) { return digest, nil },
				DaemonImage: func(int, time.Time) (string, error) { return installed, nil },
			}}
			arguments := []string{"backlog", "accept", "install", "--generation", "s1", "--report", report, "--receipt", saved}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime(arguments, &stdout, &stderr, commands)

			// Assert
			_, isDelivered, err := state.DeliveredAt(h.State, "install")
			if err != nil || isDelivered != (test.refusal == "") {
				t.Fatalf("delivered=%v %v; exit=%d stderr=%s", isDelivered, err, exit, &stderr)
			}
			if outcome, err := state.ReadOutcome(h.State, "install"); outcome != retired || test.isLive != errors.Is(err, os.ErrNotExist) {
				t.Fatalf("outcome=%+v %v", outcome, err)
			}
			data, err := os.ReadFile(backlog)
			if err != nil {
				t.Fatal(err)
			}
			if test.refusal != "" {
				if exit != 1 || !strings.Contains(stderr.String(), test.refusal) || string(data) != source {
					t.Fatalf("exit=%d stderr=%s backlog=%s", exit, &stderr, data)
				}
				if _, err := state.ReadAcceptedDelivery(h.State, "install"); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("a refused caller left a record: %v", err)
				}
				return
			}
			if test.isLive {
				if exit != 0 || !strings.Contains(stdout.String(), "install is not cleaned up yet") || string(data) != source {
					t.Fatalf("exit=%d stdout=%s stderr=%s backlog=%s", exit, &stdout, &stderr, data)
				}
				return
			}
			if exit != 0 || !strings.Contains(string(data), "## Done\n- [x] install - Install the tool (done ") {
				t.Fatalf("exit=%d stderr=%s backlog=%s", exit, &stderr, data)
			}
			stdout.Reset()
			if exit := runWithRuntime(arguments, &stdout, &stderr, commands); exit != 0 || !strings.Contains(stdout.String(), "already accepted") {
				t.Fatalf("replay exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
			}
			if after, err := os.ReadFile(backlog); err != nil || !bytes.Equal(after, data) {
				t.Fatalf("replay changed the backlog: %s %v", after, err)
			}
		})
	}
}
