package axi

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type fakeRunner struct {
	result execx.Result
	err    error
	calls  []execx.Request
}

func (r *fakeRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.calls = append(r.calls, req)
	return r.result, r.err
}

func TestTasksShowFullPreservesRawBodyAndExactRequest(t *testing.T) {
	raw := "Task: task-42\r\n\r\n  Preserve trailing spaces  \r\n"
	runner := &fakeRunner{result: execx.Result{Stdout: []byte(raw)}}

	got, err := (Tasks{Commands: runner}).ShowFull(context.Background(), "task-42")
	if err != nil {
		t.Fatalf("ShowFull: %v", err)
	}
	if got != raw {
		t.Errorf("ShowFull output = %q, want byte-for-byte %q", got, raw)
	}
	assertRequest(t, runner, execx.Request{Name: "tasks-axi", Args: []string{"show", "task-42", "--full"}})
}

func TestQuotaJSONPreservesRawBytesAndExactRequest(t *testing.T) {
	raw := []byte("{\r\n  \"quota\": [not valid JSON]\r\n}\r\n")
	runner := &fakeRunner{result: execx.Result{Stdout: raw}}

	got, err := (Quota{Commands: runner}).JSON(context.Background())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Errorf("JSON output = %q, want raw bytes %q", got, raw)
	}
	assertRequest(t, runner, execx.Request{Name: "quota-axi", Args: []string{"--json"}})
}

func TestCommandFailuresIncludeOperationAndStderr(t *testing.T) {
	runnerErr := errors.New("executable lookup failed")
	tests := []struct {
		name          string
		operation     string
		result        execx.Result
		runnerErr     error
		invoke        func(execx.Runner) error
		wantRunnerErr bool
	}{
		{
			name:      "tasks runner error",
			operation: "tasks-axi show task-42 --full",
			result:    execx.Result{Stderr: []byte("access denied")},
			runnerErr: runnerErr,
			invoke: func(runner execx.Runner) error {
				_, err := (Tasks{Commands: runner}).ShowFull(context.Background(), "task-42")
				return err
			},
			wantRunnerErr: true,
		},
		{
			name:      "tasks nonzero exit",
			operation: "tasks-axi show task-42 --full",
			result:    execx.Result{ExitCode: 17, Stderr: []byte("task not found")},
			invoke: func(runner execx.Runner) error {
				_, err := (Tasks{Commands: runner}).ShowFull(context.Background(), "task-42")
				return err
			},
		},
		{
			name:      "quota runner error",
			operation: "quota-axi --json",
			result:    execx.Result{Stderr: []byte("access denied")},
			runnerErr: runnerErr,
			invoke: func(runner execx.Runner) error {
				_, err := (Quota{Commands: runner}).JSON(context.Background())
				return err
			},
			wantRunnerErr: true,
		},
		{
			name:      "quota nonzero exit",
			operation: "quota-axi --json",
			result:    execx.Result{ExitCode: 17, Stderr: []byte("service unavailable")},
			invoke: func(runner execx.Runner) error {
				_, err := (Quota{Commands: runner}).JSON(context.Background())
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{result: tt.result, err: tt.runnerErr}
			err := tt.invoke(runner)
			if err == nil {
				t.Fatal("command returned nil error")
			}
			if !strings.Contains(err.Error(), tt.operation) {
				t.Errorf("error = %q, want operation %q", err, tt.operation)
			}
			if !strings.Contains(err.Error(), string(tt.result.Stderr)) {
				t.Errorf("error = %q, want stderr %q", err, tt.result.Stderr)
			}
			if tt.wantRunnerErr && !errors.Is(err, runnerErr) {
				t.Errorf("error = %v, want to preserve %v", err, runnerErr)
			}
			if !tt.wantRunnerErr && !strings.Contains(err.Error(), "exited with code 17") {
				t.Errorf("error = %q, want exit code", err)
			}
		})
	}
}

func assertRequest(t *testing.T, runner *fakeRunner, want execx.Request) {
	t.Helper()
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(runner.calls))
	}
	if got := runner.calls[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("request = %#v, want %#v", got, want)
	}
}
