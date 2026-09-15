package reap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/taskcontext"
)

type nativeQueryRunner struct {
	run pipeline.NativeRun
}

func (r nativeQueryRunner) Run(context.Context, execx.Request) (execx.Result, error) {
	body := `[{"run_id":"` + r.run.RunID + `","project":"` + strings.ReplaceAll(r.run.Project, `\`, `\\`) + `","branch":"` + r.run.Branch + `","head":"` + r.run.Head + `","nonce":"` + r.run.Nonce + `","generation":"` + r.run.Generation + `","intent_digest":"` + r.run.IntentDigest + `","status":"` + r.run.Status + `","custody_returned":0,"pushed":""}]`
	return execx.Result{Stdout: []byte(body)}, nil
}

type nativeGateReader struct {
	sample monitor.GateSample
	err    error
}

func (r nativeGateReader) InspectGate(context.Context, state.TaskMeta) (monitor.GateSample, error) {
	return r.sample, r.err
}

func TestPipelineValidatorRequiresExactBoundRunAndActiveProcess(t *testing.T) {
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	task := Task{ID: "task", Meta: state.TaskMeta{ID: "task", Mode: "no-mistakes", Project: filepath.Join(root, "project"), Worktree: filepath.Join(root, "worktree")}}
	for _, dir := range []string{task.Meta.Project, task.Meta.Worktree, filepath.Dir(taskcontext.PathsFor(h, task.ID).Manifest)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	launch := pipeline.Launch{Project: task.Meta.Project, Branch: "feature", Head: strings.Repeat("a", 40), Nonce: "nonce", Generation: "generation", IntentDigest: "intent", PolicyHash: "policy", RunID: "run"}
	if err := launch.Save(filepath.Join(filepath.Dir(taskcontext.PathsFor(h, task.ID).Manifest), "pipeline-launch.json")); err != nil {
		t.Fatal(err)
	}
	run := pipeline.NativeRun{RunID: "run", Project: task.Meta.Project, Branch: launch.Branch, Head: launch.Head, Nonce: launch.Nonce, Generation: launch.Generation, IntentDigest: launch.IntentDigest, Status: "running"}
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	reader := PipelineValidators{Home: h, Reader: pipeline.Reader{Root: root, Commands: nativeQueryRunner{run: run}}, Gate: nativeGateReader{sample: monitor.GateSample{Active: true, RunID: "run", Step: "review", ActivePID: 120, ActiveFor: time.Minute, ObservedAt: now}}}
	validator, active, err := reader.InspectNative(context.Background(), task)
	if err != nil || !active || validator.RootPID != 120 || validator.RunID != "run" || validator.Step != "review" {
		t.Fatalf("validator=%+v active=%t error=%v", validator, active, err)
	}

	reader.Gate = nativeGateReader{sample: monitor.GateSample{Active: true, RunID: "rebound", Step: "review", ActivePID: 120, ActiveFor: time.Minute, ObservedAt: now}}
	if _, _, err := reader.InspectNative(context.Background(), task); err == nil || !strings.Contains(err.Error(), "different run") {
		t.Fatalf("rebound run error = %v", err)
	}
	reader.Gate = nativeGateReader{err: errors.New("status unavailable")}
	if _, _, err := reader.InspectNative(context.Background(), task); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unreadable status error = %v", err)
	}
}

func TestManagedNativeRootAndDescendantsStayVisibleAndUnactionable(t *testing.T) {
	start := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	inv := Inventory{
		NativeValidators: []NativeValidator{{TaskID: "task", RunID: "run", Step: "review", RootPID: 120, RootStart: start}},
		Processes: []Process{
			process(120, 1, "codex.exe", "synthetic validator", start),
			process(121, 120, "node.exe", "synthetic child", start.Add(time.Second)),
			process(122, 121, "codex.exe", "synthetic descendant", start.Add(2*time.Second)),
			process(220, 1, "codex.exe", "unrelated process", start),
		},
	}
	findings := Classify(inv)
	managed := classOf(findings, ManagedNative)
	if len(managed) != 3 || len(Actionable(managed)) != 0 {
		t.Fatalf("managed findings = %+v", managed)
	}
	orphans := classOf(findings, OrphanProcess)
	if len(orphans) != 1 || orphans[0].PID != 220 || orphans[0].Hold == "" {
		t.Fatalf("unrelated process classification = %+v", orphans)
	}
}

func TestNativeValidatorRejectsReusedPIDCreationTime(t *testing.T) {
	observed := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	validator := NativeValidator{TaskID: "task", RunID: "run", Step: "review", RootPID: 120, StepStartedAt: observed.Add(-time.Minute), ObservedAt: observed}
	processes := []Process{process(120, 1, "codex.exe", "unrelated reused pid", observed.Add(-time.Hour))}
	if _, err := verifyNativeValidatorAt(validator, processes, observed.Add(10*time.Second)); err == nil || !strings.Contains(err.Error(), "creation time") {
		t.Fatalf("reused pid error = %v", err)
	}
}

func TestNativeValidatorRejectsReplacementCreatedAfterStepBegan(t *testing.T) {
	observed := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	validator := NativeValidator{TaskID: "task", RunID: "run", Step: "review", RootPID: 120, StepStartedAt: observed.Add(-time.Minute), ObservedAt: observed}
	processes := []Process{process(120, 1, "codex.exe", "replacement", observed.Add(-30*time.Second))}
	if _, err := verifyNativeValidatorAt(validator, processes, observed.Add(10*time.Second)); err == nil || !strings.Contains(err.Error(), "creation time") {
		t.Fatalf("replacement pid error = %v", err)
	}
}

func TestNativeValidatorRejectsStaleObservation(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	observed := now.Add(-nativeObservationMaxAge - time.Second)
	validator := NativeValidator{TaskID: "task", RunID: "run", Step: "review", RootPID: 120, StepStartedAt: observed.Add(-time.Minute), ObservedAt: observed}
	processes := []Process{process(120, 1, "codex.exe", "stale status", observed)}
	if _, err := verifyNativeValidatorAt(validator, processes, now); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale observation error = %v", err)
	}
}
