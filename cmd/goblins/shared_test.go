package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatehome"
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
	return supervisor.SharedServices{Schema: "ceo-shared-services.v1", CheckedAt: checkedAt, Services: []supervisor.SharedService{
		{Name: supervisor.SharedGateDaemon, State: "busy", Detail: "pid 276 is alive and serves 3 runs, and missed its short health reply, so it is busy, not stopped", Next: supervisor.SharedWait, NextDetail: "report the wait and do not poll", RetryAt: checkedAt.Add(time.Minute), CheckedAt: checkedAt, Holders: []string{}, Queued: []string{}, Waiting: []string{"fern", "huey"}, PID: 276, Runs: 3},
		{Name: supervisor.SharedHeavyLine, State: "full", Detail: "all 1 turns are held, by wanda, and 1 runs wait", Next: supervisor.SharedWait, NextDetail: "goblins gate turn waits its turn by itself", RetryAt: checkedAt.Add(time.Minute), CheckedAt: checkedAt, Holders: []string{"wanda"}, Queued: []string{"fern"}, Waiting: []string{}},
		{Name: supervisor.SharedDocker, State: "running", IsUsable: true, Detail: "the engine answers", Next: supervisor.SharedGo, NextDetail: "Use it through goblins services up", CheckedAt: checkedAt, Holders: []string{"fern"}, Queued: []string{}, Waiting: []string{}},
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

// Issue 2365: goblins shared prints each shared service's state, what a goblin
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
		t.Fatalf("goblins shared = %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"checked by the supervisor 20s ago, at 14:00:00Z",
		"gate-daemon: busy\n  pid 276 is alive and serves 3 runs, and missed its short health reply, so it is busy, not stopped\n  next: wait - report the wait and do not poll\n  checked again at 14:01:00Z\n  goblins waiting on it: fern, huey",
		"heavy-line: full",
		"held by: wanda\n  in its line: fern",
		"docker: running",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("goblins shared printed %q, want %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "out of date") {
		t.Errorf("goblins shared printed %q, a fresh record called out of date", stdout)
	}
}

// The record is the same under every harness: goblins shared reads one file and
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
			t.Setenv("GOBLINS_ROLE", "goblin")
			t.Setenv("GOBLINS_TASK_ID", "fern")
			t.Setenv("GOBLINS_PARENT_HARNESS", harness)

			// Act
			code, stdout, stderr := runSharedAt(t, h, checkedAt.Add(20*time.Second), "--json")

			// Assert
			if code != 0 || stderr != "" {
				t.Fatalf("goblins shared --json = %d, stderr %q", code, stderr)
			}
			var fields map[string]any
			if err := json.Unmarshal([]byte(stdout), &fields); err != nil {
				t.Fatalf("goblins shared --json printed %q: %v", stdout, err)
			}
			for _, key := range []string{"schema", "checked_at", "services", "age_seconds", "is_stale"} {
				if _, ok := fields[key]; !ok {
					t.Errorf("goblins shared --json has no %s: %s", key, stdout)
				}
			}
			services, _ := fields["services"].([]any)
			if len(services) != 3 {
				t.Fatalf("goblins shared --json services = %v, want 3", fields["services"])
			}
			daemon, _ := services[0].(map[string]any)
			for _, key := range []string{"name", "state", "is_usable", "detail", "next", "next_detail", "retry_at", "checked_at", "holders", "queued", "waiting", "pid", "runs"} {
				if _, ok := daemon[key]; !ok {
					t.Errorf("gate daemon in goblins shared --json has no %s: %v", key, daemon)
				}
			}
			outputs[harness] = stdout
		})
	}
	if outputs["claude"] != outputs["codex"] || outputs["claude"] != outputs["pi"] {
		t.Fatalf("goblins shared --json differs by harness: %v", outputs)
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
		{"an argument", true, 0, []string{"gate-daemon"}, 2, "", "usage: goblins shared [--json]"},
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
				t.Fatalf("goblins shared %v = %d, stdout %q, stderr %q; want %d, %q, %q", c.args, code, stdout, stderr, c.wantCode, c.wantStdout, c.wantStderr)
			}
		})
	}
}

// daemonStatusRunner answers no-mistakes daemon status as the native build
// does, with stdout and exitCode, and records each request.
type daemonStatusRunner struct {
	stdout   string
	exitCode int
	requests []execx.Request
}

func (r *daemonStatusRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	return execx.Result{Stdout: []byte(r.stdout), ExitCode: r.exitCode}, nil
}

// no-mistakes daemon status exits 0 even when it says the daemon is not
// running, so the supervisor's health probe counts an answer only when the
// command both exits 0 and says "daemon running", and it only reads.
func TestTheDaemonHealthProbeCountsOnlyARunningDaemonsAnswer(t *testing.T) {
	for _, c := range []struct {
		name        string
		stdout      string
		exitCode    int
		isAnswering bool
	}{
		{"a running daemon", "daemon running (pid 276)\n", 0, true},
		{"a daemon that gave no answer", "daemon not running\n", 0, false},
		{"a status that failed", "daemon running (pid 276)\n", 1, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			t.Setenv(gatehome.Variable, t.TempDir())
			commands := &daemonStatusRunner{stdout: c.stdout, exitCode: c.exitCode}

			// Act
			isAnswering := sharedProbes(false, commands).DaemonAnswers(t.Context())

			// Assert
			if isAnswering != c.isAnswering {
				t.Fatalf("the health probe read %q, exit %d, as answering %t, want %t", c.stdout, c.exitCode, isAnswering, c.isAnswering)
			}
			if len(commands.requests) != 1 || commands.requests[0].Name != "no-mistakes" || strings.Join(commands.requests[0].Args, " ") != "daemon status" {
				t.Fatalf("the health probe ran %+v, want one no-mistakes daemon status", commands.requests)
			}
		})
	}
}

// goblins serve reads the machine's shared services, and goblins serve
// --example, whose home is made up, reads none and asks the machine nothing.
func TestOnlyARealServeReadsTheMachinesSharedServices(t *testing.T) {
	// Arrange
	t.Setenv(gatehome.Variable, t.TempDir())
	commands := &daemonStatusRunner{stdout: "daemon running (pid 276)\n"}

	// Act
	example := sharedProbes(true, commands)
	machine := sharedProbes(false, commands)

	// Assert
	if example != nil || len(commands.requests) != 0 {
		t.Fatalf("an example serve's shared probes = %+v after %d commands, want none and nothing asked", example, len(commands.requests))
	}
	if machine == nil || !machine.DaemonAnswers(t.Context()) || len(commands.requests) != 1 || strings.Join(commands.requests[0].Args, " ") != "daemon status" {
		t.Fatalf("a real serve's shared probes ran %+v, want them to ask no-mistakes daemon status", commands.requests)
	}
	if machine.Scrawl == nil {
		t.Fatal("a real serve's shared probes read no Scrawl server")
	}
	if _, _, err := machine.Scrawl(); err != nil {
		t.Fatalf("a real serve's Scrawl probe failed on this machine: %v", err)
	}
}
