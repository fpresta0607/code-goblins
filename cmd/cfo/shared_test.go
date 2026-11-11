package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// writeSharedRecord keeps record where the supervisor keeps it.
func writeSharedRecord(t *testing.T, h home.Home, record supervisor.SharedServices) {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "shared-services.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// busyDaemonRecord is the shared services as the supervisor read them on
// 2026-11-11: a live daemon serving three runs that missed its health reply,
// a full heavy line, and a running engine.
func busyDaemonRecord(checkedAt time.Time) supervisor.SharedServices {
	return supervisor.SharedServices{Schema: "cfo-shared-services.v1", CheckedAt: checkedAt, Services: []supervisor.SharedService{
		{Name: supervisor.SharedGateDaemon, State: "busy", Detail: "pid 276 is alive and serves 3 runs, and missed its short health reply, so it is busy, not stopped", Next: supervisor.SharedWait, NextDetail: "report the wait and do not poll", RetryAt: checkedAt.Add(time.Minute), CheckedAt: checkedAt, Holders: []string{}, Queued: []string{}, Waiting: []string{"fern", "huey"}, PID: 276, Runs: 3},
		{Name: supervisor.SharedHeavyLine, State: "full", Detail: "all 1 turns are held, by wanda, and 1 runs wait", Next: supervisor.SharedWait, NextDetail: "cfo gate turn waits its turn by itself", RetryAt: checkedAt.Add(time.Minute), CheckedAt: checkedAt, Holders: []string{"wanda"}, Queued: []string{"fern"}, Waiting: []string{}},
		{Name: supervisor.SharedDocker, State: "running", IsUsable: true, Detail: "the engine answers", Next: supervisor.SharedGo, NextDetail: "Use it through cfo services up", CheckedAt: checkedAt, Holders: []string{"fern"}, Queued: []string{}, Waiting: []string{}},
	}}
}

func runSharedAt(t *testing.T, h home.Home, now time.Time, args ...string) (int, string, string) {
	t.Helper()
	runtime := testCommandRuntimeForHome(h)
	runtime.now = func() time.Time { return now }
	var stdout, stderr bytes.Buffer
	code := runShared(args, &stdout, &stderr, runtime)
	return code, stdout.String(), stderr.String()
}

// Issue 2365: cfo shared prints each shared service's state, what a goblin
// does now, when it is checked again, who holds it and who waits on it.
func TestSharedPrintsEachServicesStateAndWhatToDo(t *testing.T) {
	// Arrange
	h := testHome(t)
	checkedAt := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	writeSharedRecord(t, h, busyDaemonRecord(checkedAt))

	// Act
	code, stdout, stderr := runSharedAt(t, h, checkedAt.Add(20*time.Second))

	// Assert
	if code != 0 || stderr != "" {
		t.Fatalf("cfo shared = %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"checked by the supervisor 20s ago, at 14:00:00Z",
		"gate-daemon: busy\n  pid 276 is alive and serves 3 runs, and missed its short health reply, so it is busy, not stopped\n  next: wait - report the wait and do not poll\n  checked again at 14:01:00Z\n  goblins waiting on it: fern, huey",
		"heavy-line: full",
		"held by: wanda\n  in its line: fern",
		"docker: running",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("cfo shared printed %q, want %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "out of date") {
		t.Errorf("cfo shared printed %q, a fresh record called out of date", stdout)
	}
}

// The record is the same under every harness: cfo shared reads one file and
// nothing of the pane it runs in, so a goblin on Claude, Codex or Pi reads
// the same fields.
func TestSharedPrintsTheSameFieldsUnderEveryHarness(t *testing.T) {
	// Arrange
	h := testHome(t)
	checkedAt := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	writeSharedRecord(t, h, busyDaemonRecord(checkedAt))
	outputs := map[string]string{}

	for _, harness := range []string{"claude", "codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			t.Setenv("CFO_ROLE", "goblin")
			t.Setenv("CFO_TASK_ID", "fern")
			t.Setenv("CFO_PARENT_HARNESS", harness)

			// Act
			code, stdout, stderr := runSharedAt(t, h, checkedAt.Add(20*time.Second), "--json")

			// Assert
			if code != 0 || stderr != "" {
				t.Fatalf("cfo shared --json = %d, stderr %q", code, stderr)
			}
			var fields map[string]any
			if err := json.Unmarshal([]byte(stdout), &fields); err != nil {
				t.Fatalf("cfo shared --json printed %q: %v", stdout, err)
			}
			for _, key := range []string{"schema", "checked_at", "services", "age_seconds", "is_stale"} {
				if _, ok := fields[key]; !ok {
					t.Errorf("cfo shared --json has no %s: %s", key, stdout)
				}
			}
			services, _ := fields["services"].([]any)
			if len(services) != 3 {
				t.Fatalf("cfo shared --json services = %v, want 3", fields["services"])
			}
			daemon, _ := services[0].(map[string]any)
			for _, key := range []string{"name", "state", "is_usable", "detail", "next", "next_detail", "retry_at", "checked_at", "holders", "queued", "waiting", "pid", "runs"} {
				if _, ok := daemon[key]; !ok {
					t.Errorf("gate daemon in cfo shared --json has no %s: %v", key, daemon)
				}
			}
			outputs[harness] = stdout
		})
	}
	if outputs["claude"] != outputs["codex"] || outputs["claude"] != outputs["pi"] {
		t.Fatalf("cfo shared --json differs by harness: %v", outputs)
	}
}

// A record older than three of the supervisor's checks is marked stale, and
// no record yet exits 1: either way the goblin asks the CEO whether the
// supervisor runs.
func TestSharedSaysWhenTheSupervisorIsNotChecking(t *testing.T) {
	checkedAt := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name       string
		isRecorded bool
		age        time.Duration
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"a stale record", true, 4 * time.Minute, nil, 0, "may be out of date: ask the CEO whether the supervisor runs", ""},
		{"a stale record as JSON", true, 4 * time.Minute, []string{"--json"}, 0, `"is_stale": true`, ""},
		{"a record within three checks as JSON", true, 3 * time.Minute, []string{"--json"}, 0, `"is_stale": false`, ""},
		{"no record yet", false, 0, nil, 1, "", "has not checked the shared services yet; ask the CEO whether it runs"},
		{"an argument", true, 0, []string{"gate-daemon"}, 2, "", "usage: cfo shared [--json]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			if c.isRecorded {
				writeSharedRecord(t, h, busyDaemonRecord(checkedAt))
			}

			// Act
			code, stdout, stderr := runSharedAt(t, h, checkedAt.Add(c.age), c.args...)

			// Assert
			if code != c.wantCode || !strings.Contains(stdout, c.wantStdout) || !strings.Contains(stderr, c.wantStderr) {
				t.Fatalf("cfo shared %v = %d, stdout %q, stderr %q; want %d, %q, %q", c.args, code, stdout, stderr, c.wantCode, c.wantStdout, c.wantStderr)
			}
		})
	}
}
