package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/gatetest"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

func TestGateTestRecordsQualifiedHostedReuseWithoutTakingATurn(t *testing.T) {
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	runtime := standIn()
	var executions, memoryReads int
	runtime.availableMemory = func() (uint64, error) { memoryReads++; return plenty() }
	runtime.gateRun = func(_ []string, _ string, _ []string, _, _ io.Writer) (int, error) { executions++; return 0, nil }
	runtime.gateHosted = func(_ context.Context, plan gatetest.Plan, _ time.Duration) (*verify.HostedReceipt, error) {
		return &verify.HostedReceipt{URL: "https://github.com/fixture/project/actions/runs/123", RunID: 123, Head: plan.Commit, Main: plan.Base, Tree: "fixture-tree", Packages: plan.Tests}, nil
	}
	var stdout, stderr bytes.Buffer
	if code := gateTestWith(runtime, &stdout, &stderr); code != 0 || executions != 0 || memoryReads != 0 {
		t.Fatalf("code=%d, executions=%d, memory reads=%d\n%s\n%s", code, executions, memoryReads, &stdout, &stderr)
	}
	report := hostedGateReport(t)
	if report.Reused == nil || report.Reused.RunID != 123 || report.Status != "passed" || len(report.Checks) != 2 {
		t.Fatalf("reuse receipt lost: %+v", report)
	}
	for _, check := range report.Checks {
		if check.Status != "reused" || check.ExitCode != 0 || !check.Start.IsZero() || check.DurationSeconds != 0 {
			t.Fatalf("external proof presented as execution: %+v", check)
		}
	}
}

func TestGateTestReusesProofThatArrivesWhileWaitingAndReleasesTheTurn(t *testing.T) {
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	turn := holdTheTurn(t)
	runtime := standIn()
	var reads atomic.Int32
	var isProofReady, isTested atomic.Bool
	runtime.gateHosted = func(_ context.Context, plan gatetest.Plan, _ time.Duration) (*verify.HostedReceipt, error) {
		reads.Add(1)
		if !isProofReady.Load() {
			return nil, errors.New("hosted CI is pending")
		}
		return &verify.HostedReceipt{URL: "https://github.com/fixture/project/actions/runs/123", RunID: 123, Head: plan.Commit, Main: plan.Base}, nil
	}
	runtime.gateRun = func(command []string, _ string, _ []string, _, _ io.Writer) (int, error) {
		if command[1] == "test" {
			isTested.Store(true)
		}
		return 0, nil
	}
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	stopped := make(chan struct{})
	t.Cleanup(func() { turn.Release(); <-stopped })
	go func() {
		defer close(stopped)
		exited <- gateTestWith(runtime, &stdout, &stderr)
	}()
	for deadline := time.Now().Add(2 * time.Minute); !strings.Contains(stdout.String(), "waiting for its turn"); time.Sleep(20 * time.Millisecond) {
		select {
		case code := <-exited:
			t.Fatalf("code=%d before the holder released its turn\n%s\n%s", code, stdout.String(), stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the gate did not join the line")
		}
	}
	if reads.Load() != 1 || isTested.Load() {
		t.Fatal("the waiting gate executed tests or read evidence again before taking its turn")
	}
	isProofReady.Store(true)
	turn.Release()
	select {
	case code := <-exited:
		if code != 0 || isTested.Load() || reads.Load() != 2 {
			t.Fatalf("code=%d, tested=%v, reads=%d\n%s\n%s", code, isTested.Load(), reads.Load(), stdout.String(), stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatal("the gate did not release its turn after finding proof")
	}
	report := hostedGateReport(t)
	if report.Reused == nil || report.QueueSeconds <= 0 || len(report.ReuseDeclined) != 1 || report.Checks[0].Status != "passed" || report.Checks[1].Status != "reused" {
		t.Fatalf("wait-time proof lost: %+v", report)
	}
	holding, waiting, err := verify.Line(filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots"))
	if err != nil || len(holding) != 0 || len(waiting) != 0 {
		t.Fatalf("reused proof retained a turn: %+v, %+v, %v", holding, waiting, err)
	}
}

func TestGateTestFallsBackOnUnreadableHostedProofAndStillBlocksAFailure(t *testing.T) {
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	runtime := standIn()
	runtime.gateHosted = func(context.Context, gatetest.Plan, time.Duration) (*verify.HostedReceipt, error) {
		return nil, errors.New("proof digest differs")
	}
	runtime.gateRun = func(command []string, _ string, _ []string, _, _ io.Writer) (int, error) {
		if command[1] == "test" {
			return 1, errors.New("a relevant test failed")
		}
		return 0, nil
	}
	var stdout, stderr bytes.Buffer
	if code := gateTestWith(runtime, &stdout, &stderr); code != 1 {
		t.Fatalf("failure bypassed: %d\n%s\n%s", code, &stdout, &stderr)
	}
	report := hostedGateReport(t)
	if report.Reused != nil || report.Status != "failed" || len(report.ReuseDeclined) == 0 || report.Checks[1].Status != "failed" {
		t.Fatalf("declined proof hid a failure: %+v", report)
	}
}

func TestGateTestRunsLocallyWhenTheReuseReceiptCannotBeSaved(t *testing.T) {
	for _, scenario := range []string{"begin", "save"} {
		t.Run(scenario, func(t *testing.T) {
			dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\nfunc A() int { return 2 }\n"})
			t.Chdir(dir)
			store := os.Getenv("CFO_VERIFY_DIR")
			if scenario == "begin" {
				file := filepath.Join(t.TempDir(), "blocked-store")
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("CFO_VERIFY_DIR", file)
			}
			runtime := standIn()
			var executions int
			runtime.gateRun = func(_ []string, _ string, _ []string, _, _ io.Writer) (int, error) {
				executions++
				return 0, nil
			}
			runtime.gateHosted = func(_ context.Context, plan gatetest.Plan, _ time.Duration) (*verify.HostedReceipt, error) {
				if scenario != "begin" {
					logs, err := filepath.Glob(filepath.Join(store, "reports", "*", "*.log"))
					if err != nil || len(logs) != 1 {
						return nil, errors.New("fixture did not find the report path")
					}
					blocked := strings.TrimSuffix(logs[0], ".log") + ".json"
					if err := os.Mkdir(blocked, 0o700); err != nil && !os.IsExist(err) {
						return nil, err
					}
				}
				return &verify.HostedReceipt{RunID: 123, Head: plan.Commit, Main: plan.Base}, nil
			}
			var stdout, stderr bytes.Buffer
			if code := gateTestWith(runtime, &stdout, &stderr); code != 0 || executions != 2 {
				t.Fatalf("code=%d, local executions=%d\n%s\n%s", code, executions, &stdout, &stderr)
			}
		})
	}
}

func TestGateTestDoesNotChargeDeclinedProofTimeToLocalTests(t *testing.T) {
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	runtime := standIn()
	var lookupCompleted time.Time
	runtime.gateHosted = func(context.Context, gatetest.Plan, time.Duration) (*verify.HostedReceipt, error) {
		time.Sleep(20 * time.Millisecond)
		lookupCompleted = time.Now()
		return nil, errors.New("no compatible hosted proof")
	}
	var stdout, stderr bytes.Buffer
	if code := gateTestWith(runtime, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d\n%s\n%s", code, &stdout, &stderr)
	}
	if report := hostedGateReport(t); report.Checks[1].Start.Before(lookupCompleted) {
		t.Fatal("declined proof time was included in local test execution")
	}
}

type hostedReceiptBlocker struct {
	store  string
	buffer bytes.Buffer
}

func (writer *hostedReceiptBlocker) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte("reusing hosted Go evidence")) {
		files, err := filepath.Glob(filepath.Join(writer.store, "reports", "*", "*.log"))
		if err != nil || len(files) != 1 {
			return 0, errors.New("fixture did not find the receipt path")
		}
		path := strings.TrimSuffix(files[0], ".log") + ".json"
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			return 0, err
		}
	}
	return writer.buffer.Write(data)
}

func TestGateTestCannotPassReuseWithoutTheFinishedReceipt(t *testing.T) {
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	runtime := standIn()
	runtime.gateHosted = func(_ context.Context, plan gatetest.Plan, _ time.Duration) (*verify.HostedReceipt, error) {
		return &verify.HostedReceipt{RunID: 123, Head: plan.Commit, Main: plan.Base}, nil
	}
	stdout := hostedReceiptBlocker{store: os.Getenv("CFO_VERIFY_DIR")}
	var stderr bytes.Buffer
	if code := gateTestWith(runtime, &stdout, &stderr); code != 1 || !strings.Contains(stdout.buffer.String(), "failed at level") {
		t.Fatalf("reuse passed without a saved receipt: code=%d\n%s\n%s", code, stdout.buffer.String(), &stderr)
	}
}

func hostedGateReport(t *testing.T) verify.Report {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "reports", "*", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("report files=%v, %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var report verify.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report
}
