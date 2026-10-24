package janitor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

func TestDetachedConsumerCustodySurvivesSupplierCleanup(t *testing.T) {
	for _, scenario := range []string{"queued", "admitted", "queued-unmarked", "admitted-unmarked", "orphan"} {
		phase := strings.TrimSuffix(scenario, "-unmarked")
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state")}
			meta := state.TaskMeta{ID: "supplier", Backend: "native", Project: filepath.Join(root, "project"), Worktree: filepath.Join(h.Worktrees(), "project", "supplier"), TaskTmp: filepath.Join(h.State, "tasktmp", "supplier"), Scratch: filepath.Join(h.Scratch(), "supplier")}
			for _, directory := range []string{meta.Scratch, meta.TaskTmp, filepath.Join(h.State, "hosts")} {
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"supplier", "consumer"} {
				proof := host.ProofSum(id+"-proof") + " " + time.Now().UTC().Format(time.RFC3339Nano) + "\n"
				if err := os.WriteFile(filepath.Join(h.State, "hosts", id+".proofs"), []byte(proof), 0600); err != nil {
					t.Fatal(err)
				}
			}
			started, exists := proc.StartTime(os.Getpid())
			if !exists {
				t.Fatal("test process identity unavailable")
			}
			record, err := json.Marshal(host.Record{ID: "consumer", Pipe: "fixture", Token: "fixture", HostPID: os.Getpid(), Started: started})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.State, "hosts", "consumer.json"), record, 0600); err != nil {
				t.Fatal(err)
			}
			binary, err := os.ReadFile(os.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			borrowed := filepath.Join(meta.Scratch, "borrowed.test.exe")
			if err := os.WriteFile(borrowed, binary, 0700); err != nil {
				t.Fatal(err)
			}
			store := t.TempDir()
			t.Setenv("CFO_VERIFY_DIR", store)
			t.Setenv(verify.TurnVariable, "")
			t.Setenv("CFO_CONSUMER_FIXTURE", "launcher")
			t.Setenv("CFO_CONSUMER_PHASE", phase)
			t.Setenv("CFO_CONSUMER_BINARY", borrowed)
			t.Setenv(host.IDVariable, "consumer")
			t.Setenv(host.ProofVariable, "consumer-proof")
			t.Setenv("NO_MISTAKES_GATE", "")
			if phase == "orphan" || strings.HasSuffix(scenario, "-unmarked") {
				t.Setenv(host.ProofVariable, "unproven-proof")
			}
			launcher := exec.Command(os.Args[0], "-test.run=^TestDetachedConsumerFixture$")
			launcher.Dir = store
			launcher.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			if output, err := launcher.CombinedOutput(); err != nil {
				t.Fatalf("launcher: %v\n%s", err, output)
			}
			var identity lifecycle.Identity
			data, err := os.ReadFile(filepath.Join(store, "identity.json"))
			if err != nil || json.Unmarshal(data, &identity) != nil {
				t.Fatalf("controller identity: %s %v", data, err)
			}
			handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(identity.PID))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = lifecycle.Terminate(context.Background(), identity)
				if wait, err := windows.WaitForSingleObject(handle, uint32((30 * time.Second).Milliseconds())); err != nil || wait != windows.WAIT_OBJECT_0 {
					t.Errorf("fixture cleanup pid %d: wait=%d err=%v", identity.PID, wait, err)
				}
				windows.CloseHandle(handle)
			})
			if launcher.ProcessState == nil || !launcher.ProcessState.Exited() {
				t.Fatal("launcher still runs")
			}
			admissionDir, err := verify.AdmissionDir()
			if err != nil {
				t.Fatal(err)
			}
			holding, waiting, err := verify.Line(admissionDir)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "queued" && !slices.ContainsFunc(waiting, func(run verify.Standing) bool { return run.PID == identity.PID }) {
				t.Fatalf("controller did not actually queue: %+v", waiting)
			}
			if phase == "admitted" {
				if !slices.ContainsFunc(holding, func(run verify.Standing) bool { return run.PID == identity.PID }) {
					t.Fatalf("controller did not actually take a turn: %+v", holding)
				}
				data, err := os.ReadFile(filepath.Join(admissionDir, "custody", "borrowed-controller-fixture.json"))
				var custody verify.Custody
				if err != nil || json.Unmarshal(data, &custody) != nil || custody.Controller.PID != identity.PID || !custody.Controller.Start.Equal(identity.Started) {
					t.Fatalf("controller has no matching execution custody: %s %v", data, err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()

			// Act: the production readers, planner, leftover sweep and teardown.
			cfg := Config{Home: h, Now: time.Now(), Processes: ReadAllProcesses, Controllers: lifecycle.VerificationControllers, Owners: func() ([]Owner, []string) { return Owners(ctx, h, nil) }}
			plan, err := cfg.PlanProcesses(ctx)
			if err != nil {
				t.Fatal(err)
			}
			left, err := lifecycle.Left(ctx, h, meta)
			if err != nil {
				t.Fatal(err)
			}
			isEnding := slices.ContainsFunc(plan.Ending, func(item ProcessItem) bool { return item.PID == identity.PID })
			isLeft := slices.ContainsFunc(left, func(item lifecycle.LeftProcess) bool { return item.PID == identity.PID })
			if phase == "orphan" {
				if !isEnding || !isLeft {
					t.Fatalf("true orphan not selected: ending=%t left=%t notes=%v", isEnding, isLeft, plan.Notes)
				}
			} else if isEnding || isLeft {
				t.Fatalf("%s consumer selected by supplier cleanup: ending=%t left=%t", phase, isEnding, isLeft)
			}
			resources, err := lifecycle.TaskResources(ctx, h, meta, pipeline.Reader{Root: filepath.Join(root, "gate"), Commands: execx.OSRunner{}})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := lifecycle.StopResources(ctx, resources); err != nil {
				t.Fatal(err)
			}
			if phase == "orphan" {
				if wait, err := windows.WaitForSingleObject(handle, uint32((30 * time.Second).Milliseconds())); err != nil || wait != windows.WAIT_OBJECT_0 {
					t.Fatalf("orphan did not end: wait=%d err=%v", wait, err)
				}
			}
			var code uint32
			if err := windows.GetExitCodeProcess(handle, &code); err != nil {
				t.Fatal(err)
			}
			isRunning := code == 259
			if isRunning != (phase != "orphan") {
				t.Fatalf("%s controller running after supplier teardown: %t", phase, isRunning)
			}
		})
	}
}

func TestDetachedConsumerFixture(t *testing.T) {
	mode := os.Getenv("CFO_CONSUMER_FIXTURE")
	if mode == "" {
		return
	}
	store := os.Getenv("CFO_VERIFY_DIR")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	if mode == "launcher" {
		child := exec.Command(os.Getenv("CFO_CONSUMER_BINARY"), "-test.run=^TestDetachedConsumerFixture$")
		child.Env = append(os.Environ(), "CFO_CONSUMER_FIXTURE=controller")
		child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		started, exists := proc.StartTime(child.Process.Pid)
		if !exists {
			t.Fatal("controller failed to start")
		}
		data, err := json.Marshal(lifecycle.Identity{PID: child.Process.Pid, Started: started})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store, "identity.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.NewTimer(30 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(filepath.Join(store, "ready")); err == nil {
				return
			}
			select {
			case <-ticker.C:
			case <-deadline.C:
				_ = child.Process.Kill()
				_ = child.Wait()
				t.Fatal("controller never reached its admission state")
			}
		}
	}
	phase := os.Getenv("CFO_CONSUMER_PHASE")
	ready := func() {
		if err := fsx.AtomicWriteFile(filepath.Join(store, "ready"), []byte(phase)); err != nil {
			t.Fatal(err)
		}
	}
	if phase == "orphan" {
		ready()
		<-ctx.Done()
		return
	}
	dir, err := verify.AdmissionDir()
	if err != nil {
		t.Fatal(err)
	}
	admission := verify.Admission{Dir: dir, Available: func() (uint64, error) {
		if phase == "queued" {
			return 0, nil
		}
		return 32 << 30, nil
	}, Who: "isolated borrowed controller fixture", Limit: time.Minute, Poll: 20 * time.Millisecond}
	if phase == "queued" {
		admission.Waiting = func(time.Duration, string) { ready() }
	}
	turn, err := admission.Wait(ctx)
	if err != nil {
		if phase == "queued" && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
			return
		}
		t.Fatal(err)
	}
	defer func() {
		if err := turn.Release(); err != nil {
			t.Error(err)
		}
	}()
	guard, err := turn.Protect("borrowed-controller-fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := guard.Complete(); err != nil {
			t.Error(err)
		}
	}()
	ready()
	<-ctx.Done()
}
