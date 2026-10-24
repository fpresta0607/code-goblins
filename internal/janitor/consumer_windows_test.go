package janitor

import (
	"context"
	"encoding/json"
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
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

func TestDetachedConsumerCustodySurvivesSupplierCleanup(t *testing.T) {
	buildDir := t.TempDir()
	program := filepath.Join(buildDir, "cfo.test.exe")
	build := execx.CommandContext(t.Context(), "go", "build", "-o", program, "./cmd/cfo")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build borrowed controller: %v\n%s", err, output)
	}
	binary, err := os.ReadFile(program)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"queued", "admitted", "queued-unmarked", "admitted-unmarked", "orphan"} {
		phase := strings.TrimSuffix(scenario, "-unmarked")
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			standin.RemoveAtCleanup(t, root)
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
			// The real CLI borrows a supplier's executable. Its test suffix keeps
			// admission in this fixture's store, as for other test programs.
			borrowed := filepath.Join(meta.Scratch, "borrowed.test.exe")
			if phase == "orphan" {
				standin.Put(t, borrowed)
			} else if err := os.WriteFile(borrowed, binary, 0700); err != nil {
				t.Fatal(err)
			}
			store := t.TempDir()
			standin.RemoveAtCleanup(t, store)
			standin.Put(t, filepath.Join(store, "held.exe"))
			t.Setenv("CFO_VERIFY_DIR", store)
			t.Setenv(verify.TurnVariable, "")
			t.Setenv("CFO_HOME", h.Root)
			t.Setenv("CFO_STATE_OVERRIDE", h.State)
			t.Setenv("CFO_CONSUMER_FIXTURE", "launcher")
			t.Setenv("CFO_CONSUMER_PHASE", phase)
			t.Setenv("CFO_CONSUMER_BINARY", borrowed)
			t.Setenv(host.IDVariable, "consumer")
			t.Setenv(host.ProofVariable, "consumer-proof")
			t.Setenv("NO_MISTAKES_GATE", "")
			if phase == "orphan" || strings.HasSuffix(scenario, "-unmarked") {
				t.Setenv(host.ProofVariable, "unproven-proof")
			}
			admissionDir, err := verify.AdmissionDir()
			if err != nil {
				t.Fatal(err)
			}
			if phase == "queued" {
				turn, err := (verify.Admission{Dir: admissionDir, Available: func() (uint64, error) { return 32 << 30, nil }, Who: "fixture slot holder", Limit: time.Minute, Poll: 20 * time.Millisecond}).Wait(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := turn.Release(); err != nil {
						t.Error(err)
					}
				})
			}
			launcher := exec.Command(os.Args[0], "-test.run=^TestDetachedConsumerFixture$")
			launcher.Dir = store
			launcher.Env = append(os.Environ(), standin.Env(filepath.Join(store, "ready"), standin.Rule{Any: true, Hold: true})...)
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
			watchdog := time.AfterFunc(3*time.Minute, func() { _, _ = lifecycle.Terminate(context.Background(), identity) })
			var custody verify.Custody
			t.Cleanup(func() {
				watchdog.Stop()
				_, _ = lifecycle.Terminate(context.Background(), identity)
				if wait, err := windows.WaitForSingleObject(handle, uint32((30 * time.Second).Milliseconds())); err != nil || wait != windows.WAIT_OBJECT_0 {
					t.Errorf("fixture cleanup pid %d: wait=%d err=%v", identity.PID, wait, err)
				}
				windows.CloseHandle(handle)
				if custody.Job != "" {
					if usage, _, err := execx.EndNamedJob(context.Background(), custody.Job); err != nil || usage.ActiveProcesses != 0 {
						t.Errorf("fixture worker cleanup: %+v %v", usage, err)
					}
				}
			})
			if launcher.ProcessState == nil || !launcher.ProcessState.Exited() {
				t.Fatal("launcher still runs")
			}
			var holding, waiting []verify.Standing
			deadline := time.Now().Add(30 * time.Second)
			for {
				holding, waiting, err = verify.Line(admissionDir)
				if err != nil {
					t.Fatal(err)
				}
				_, readyError := os.Stat(filepath.Join(store, "ready"))
				if phase == "queued" && slices.ContainsFunc(waiting, func(run verify.Standing) bool { return run.PID == identity.PID }) || phase != "queued" && readyError == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("controller never reached its admission state")
				}
				time.Sleep(20 * time.Millisecond)
			}
			arguments, err := proc.Arguments(identity.PID)
			if err != nil || len(arguments) == 0 || !strings.EqualFold(arguments[0], borrowed) {
				t.Fatalf("controller did not borrow the supplier executable: %v %v", arguments, err)
			}
			if phase == "queued" && !slices.ContainsFunc(waiting, func(run verify.Standing) bool { return run.PID == identity.PID }) {
				t.Fatalf("controller did not actually queue: %+v", waiting)
			}
			if phase == "admitted" {
				if !slices.ContainsFunc(holding, func(run verify.Standing) bool { return run.PID == identity.PID }) {
					t.Fatalf("controller did not actually take a turn: %+v", holding)
				}
				files, err := filepath.Glob(filepath.Join(admissionDir, "custody", "*.json"))
				if err != nil || len(files) != 1 {
					t.Fatalf("controller execution custody files: %v %v", files, err)
				}
				data, err := os.ReadFile(files[0])
				if err != nil || json.Unmarshal(data, &custody) != nil || custody.Controller.PID != identity.PID || !custody.Controller.Start.Equal(identity.Started) || custody.Job == "" || custody.Evidence == "" {
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
	if os.Getenv("CFO_CONSUMER_FIXTURE") != "launcher" {
		return
	}
	store := os.Getenv("CFO_VERIFY_DIR")
	var arguments []string
	if os.Getenv("CFO_CONSUMER_PHASE") != "orphan" {
		arguments = []string{"gate", "turn", "--as", "isolated borrowed controller fixture", "--wait", "1m", "--execution-limit", "3m", "--", filepath.Join(store, "held.exe")}
	}
	child := exec.Command(os.Getenv("CFO_CONSUMER_BINARY"), arguments...)
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	isHandedOff := false
	defer func() {
		if !isHandedOff {
			_ = child.Process.Kill()
			_ = child.Wait()
		} else {
			_ = child.Process.Release()
		}
	}()
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
	isHandedOff = true
}
