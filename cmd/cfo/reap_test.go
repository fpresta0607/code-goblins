package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/reap"
)

func reapTestRuntime(h home.Home, sweep func(context.Context, home.Home, reap.Options) (reap.Result, error)) commandRuntime {
	return commandRuntime{
		resolveHome: func() (home.Home, error) { return h, nil },
		reap:        sweep,
	}
}

func TestReapHelpPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exit := runReap([]string{"--help"}, &stdout, &stderr, commandRuntime{}); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if !strings.Contains(stdout.String(), "usage: cfo reap") {
		t.Fatalf("stdout = %q, want the usage line", stdout.String())
	}
}

func TestReapArgumentValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown argument", []string{"--nope"}, `unknown argument "--nope"`},
		{"force needs a value", []string{"--force"}, "--force needs a pid or a task id"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if exit := runReap(testCase.args, &stdout, &stderr, commandRuntime{}); exit != 2 {
				t.Fatalf("exit = %d, want 2", exit)
			}
			if !strings.Contains(stderr.String(), testCase.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), testCase.want)
			}
		})
	}
}

// TestReapDefaultsToDryRun is the posture the whole command depends on:
// killing a process is irreversible, so acting must be typed out in full.
func TestReapDefaultsToDryRun(t *testing.T) {
	h := primaryHomeFixture(t)
	var seen reap.Options
	runtime := reapTestRuntime(h, func(_ context.Context, _ home.Home, options reap.Options) (reap.Result, error) {
		seen = options
		return reap.Result{Findings: []reap.Finding{{
			Class: reap.OrphanProcess, PID: 31032, Detail: "claude.exe has no pane", Action: "kill the process tree",
		}}}, nil
	})

	var stdout, stderr bytes.Buffer
	if exit := runReap(nil, &stdout, &stderr, runtime); exit != 0 {
		t.Fatalf("exit = %d stderr=%q, want 0", exit, stderr.String())
	}
	if seen.Apply {
		t.Fatal("cfo reap with no flags asked to apply")
	}
	out := stdout.String()
	if !strings.Contains(out, "pid=31032") || !strings.Contains(out, "Run cfo reap --apply") {
		t.Fatalf("stdout = %q, want the finding and the opt-in hint", out)
	}
}

func TestReapApplyAndForceReachTheSweep(t *testing.T) {
	h := primaryHomeFixture(t)
	var seen reap.Options
	runtime := reapTestRuntime(h, func(_ context.Context, _ home.Home, options reap.Options) (reap.Result, error) {
		seen = options
		return reap.Result{}, nil
	})

	var stdout, stderr bytes.Buffer
	if exit := runReap([]string{"--apply", "--force", "31032", "--force", "utah"}, &stdout, &stderr, runtime); exit != 0 {
		t.Fatalf("exit = %d stderr=%q, want 0", exit, stderr.String())
	}
	if !seen.Apply || !seen.ProbeIdle {
		t.Fatalf("options = %+v, want apply with a fresh idleness probe", seen)
	}
	if !seen.Force["31032"] || !seen.Force["utah"] {
		t.Fatalf("force = %+v, want both named targets", seen.Force)
	}
}

// TestReapRecordsAFailedSweep: the session-start digest reads this record, so a
// sweep that could not see the fleet must leave a failure behind rather than
// the previous run's clean result.
func TestReapRecordsAFailedSweep(t *testing.T) {
	h := primaryHomeFixture(t)
	runtime := reapTestRuntime(h, func(context.Context, home.Home, reap.Options) (reap.Result, error) {
		return reap.Result{}, errors.New("herdr is down")
	})

	var stdout, stderr bytes.Buffer
	if exit := runReap(nil, &stdout, &stderr, runtime); exit != 1 {
		t.Fatalf("exit = %d, want 1", exit)
	}
	record, err := reap.ReadRecord(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if record.Error != "herdr is down" {
		t.Fatalf("record = %+v, want the failure recorded", record)
	}
}

func TestReapJSON(t *testing.T) {
	h := primaryHomeFixture(t)
	runtime := reapTestRuntime(h, func(context.Context, home.Home, reap.Options) (reap.Result, error) {
		return reap.Result{Findings: []reap.Finding{{Class: reap.StaleServer, PID: 555, Detail: "d", Action: "kill the process tree"}}}, nil
	})

	var stdout, stderr bytes.Buffer
	if exit := runReap([]string{"--json"}, &stdout, &stderr, runtime); exit != 0 {
		t.Fatalf("exit = %d stderr=%q, want 0", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"class":"stale_server"`) {
		t.Fatalf("stdout = %q, want typed JSON", stdout.String())
	}
}
