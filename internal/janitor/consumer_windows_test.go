package janitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

func TestDetachedConsumerCustodySurvivesSupplierCleanup(t *testing.T) {
	buildDir := t.TempDir()
	standin.RemoveAtCleanup(t, buildDir)
	program := filepath.Join(buildDir, "consumer.test.exe")
	build := execx.CommandContext(t.Context(), "go", "build", "-trimpath", "-buildvcs=false", "-o", program, "./testdata/consumer")
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
			// The standalone fixture uses real custody with controlled headroom.
			// Its test suffix keeps admission in this fixture's store.
			borrowed := filepath.Join(meta.Scratch, "borrowed.test.exe")
			if err := os.WriteFile(borrowed, binary, 0700); err != nil {
				t.Fatal(err)
			}
			store := t.TempDir()
			standin.RemoveAtCleanup(t, store)
			standin.Put(t, filepath.Join(store, "held.exe"))
			writeConsumerHeadroom(t, store, verify.Headroom{PhysicalAvailable: 32 << 30, CommitAvailable: 32 << 30})
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
			identity, handle, err := startDetachedConsumer(t, store, filepath.Join(store, "identity.json"))
			if err != nil {
				t.Fatal(err)
			}
			var custody verify.Custody
			t.Cleanup(func() {
				if custody.Job != "" {
					if usage, _, err := execx.EndNamedJob(context.Background(), custody.Job); err != nil || usage.ActiveProcesses != 0 {
						t.Errorf("fixture worker cleanup: %+v %v", usage, err)
					}
				}
			})
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
	for _, failure := range []string{"missing", "malformed"} {
		t.Run("failed-handoff/"+failure, func(t *testing.T) {
			// Arrange: the launcher really starts and hands off an orphan.
			store := t.TempDir()
			t.Setenv("CFO_VERIFY_DIR", store)
			t.Setenv(verify.TurnVariable, "")
			t.Setenv("CFO_CONSUMER_FIXTURE", "launcher")
			t.Setenv("CFO_CONSUMER_PHASE", "orphan")
			t.Setenv("CFO_CONSUMER_BINARY", program)
			readback := filepath.Join(store, "failed-readback.json")
			if failure == "malformed" {
				if err := os.WriteFile(readback, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}

			// Act: fail before the caller has any controller identity or handle.
			_, _, err := startDetachedConsumer(t, store, readback)

			// Assert: the pre-registered job ended the actual handed-off child.
			if err == nil || !strings.Contains(err.Error(), "controller identity") {
				t.Fatalf("readback failure = %v", err)
			}
			data, err := os.ReadFile(filepath.Join(store, "identity.json"))
			var identity lifecycle.Identity
			if err != nil || json.Unmarshal(data, &identity) != nil || identity.PID <= 0 || identity.Started.IsZero() {
				t.Fatalf("real handoff identity: %s %v", data, err)
			}
			handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(identity.PID))
			if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(handle)
			var creation, exit, kernel, user windows.Filetime
			if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
				t.Fatal(err)
			}
			if time.Unix(0, creation.Nanoseconds()).UTC().Equal(identity.Started) {
				if wait, err := windows.WaitForSingleObject(handle, uint32((30 * time.Second).Milliseconds())); err != nil || wait != windows.WAIT_OBJECT_0 {
					t.Fatalf("handed-off controller survived failed readback: wait=%d err=%v", wait, err)
				}
			}
		})
	}
	for _, constrained := range []string{"physical", "commit"} {
		t.Run("constrained-resources/"+constrained, func(t *testing.T) {
			// Arrange: only this fixture's readings determine its admission.
			store := t.TempDir()
			standin.RemoveAtCleanup(t, store)
			standin.Put(t, filepath.Join(store, "held.exe"))
			t.Setenv("CFO_VERIFY_DIR", store)
			t.Setenv(verify.TurnVariable, "")
			t.Setenv("CFO_CONSUMER_FIXTURE", "launcher")
			t.Setenv("CFO_CONSUMER_PHASE", "admitted")
			t.Setenv("CFO_CONSUMER_BINARY", program)
			memory := verify.Headroom{PhysicalAvailable: 32 << 30, CommitAvailable: 32 << 30}
			if constrained == "physical" {
				memory.PhysicalAvailable = 3 << 30
			} else {
				memory.CommitAvailable = 3 << 30
			}
			writeConsumerHeadroom(t, store, memory)
			identity, _, err := startDetachedConsumer(t, store, filepath.Join(store, "identity.json"))
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(30 * time.Second)
			for {
				data, err := os.ReadFile(filepath.Join(store, "waiting"))
				if err == nil && strings.Contains(string(data), "3.0 GB of memory is available") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("fixture ignored constrained %s: %s %v", constrained, data, err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			holding, waiting, err := verify.Line(filepath.Join(store, "slots"))
			if err != nil || len(holding) != 0 || !slices.ContainsFunc(waiting, func(run verify.Standing) bool { return run.PID == identity.PID }) {
				t.Fatalf("constrained fixture took a turn: holding=%v waiting=%v err=%v", holding, waiting, err)
			}
			if _, err := os.Stat(filepath.Join(store, "ready")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("constrained fixture started its payload: %v", err)
			}

			// Act: restore this fixture's inputs without changing the machine.
			writeConsumerHeadroom(t, store, verify.Headroom{PhysicalAvailable: 32 << 30, CommitAvailable: 32 << 30})

			// Assert: real admission and execution now start the payload.
			deadline = time.Now().Add(30 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(store, "ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("fixture did not admit after its resource input recovered")
				}
				time.Sleep(20 * time.Millisecond)
			}
			holding, waiting, err = verify.Line(filepath.Join(store, "slots"))
			if err != nil || len(waiting) != 0 || !slices.ContainsFunc(holding, func(run verify.Standing) bool { return run.PID == identity.PID }) {
				t.Fatalf("recovered fixture has no turn: holding=%v waiting=%v err=%v", holding, waiting, err)
			}
		})
	}
}

func writeConsumerHeadroom(t *testing.T, store string, memory verify.Headroom) {
	t.Helper()
	data, err := json.Marshal(memory)
	if err == nil {
		err = fsx.AtomicWriteFile(filepath.Join(store, "headroom.json"), data)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func startDetachedConsumer(t *testing.T, store, readback string) (identity lifecycle.Identity, handle windows.Handle, failure error) {
	t.Helper()
	launcher := execx.Command(os.Args[0], "-test.run=^TestDetachedConsumerFixture$")
	launcher.Dir = store
	launcher.Env = append(os.Environ(), standin.Env(filepath.Join(store, "ready"), standin.Rule{Any: true, Hold: true})...)
	var output bytes.Buffer
	launcher.Stdout, launcher.Stderr = &output, &output
	tree, err := execx.StartTree(launcher)
	if err != nil {
		return identity, 0, err
	}
	end := sync.OnceValues(func() (execx.TreeUsage, error) { return tree.TerminateAndWait(context.Background()) })
	watchdog := time.AfterFunc(3*time.Minute, func() { _, _ = end() })
	t.Cleanup(func() {
		watchdog.Stop()
		if usage, err := end(); err != nil || usage.ActiveProcesses != 0 {
			t.Errorf("fixture tree cleanup: %+v %v", usage, err)
		}
		if handle != 0 {
			if wait, err := windows.WaitForSingleObject(handle, uint32((30 * time.Second).Milliseconds())); err != nil || wait != windows.WAIT_OBJECT_0 {
				t.Errorf("fixture cleanup pid %d: wait=%d err=%v", identity.PID, wait, err)
			}
			windows.CloseHandle(handle)
		}
	})
	defer func() {
		if failure != nil {
			usage, err := end()
			t.Logf("failed handoff owned-job cleanup: active=%d total=%d err=%v", usage.ActiveProcesses, usage.TotalProcesses, err)
			failure = errors.Join(failure, err)
		}
	}()
	if err := launcher.Wait(); err != nil {
		return identity, 0, fmt.Errorf("launcher: %w\n%s", err, output.Bytes())
	}
	if launcher.ProcessState == nil || !launcher.ProcessState.Exited() {
		return identity, 0, errors.New("launcher still runs")
	}
	data, err := os.ReadFile(readback)
	if err == nil {
		err = json.Unmarshal(data, &identity)
	}
	if err != nil {
		return identity, 0, fmt.Errorf("controller identity: %w", err)
	}
	handle, err = windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(identity.PID))
	if err != nil {
		return identity, 0, err
	}
	if started, exists := proc.StartTime(identity.PID); !exists || !started.Equal(identity.Started) {
		return identity, handle, errors.New("controller identity changed before its handle was held")
	}
	return identity, handle, nil
}

func TestDetachedConsumerFixture(t *testing.T) {
	if os.Getenv("CFO_CONSUMER_FIXTURE") != "launcher" {
		return
	}
	store := os.Getenv("CFO_VERIFY_DIR")
	child := exec.Command(os.Getenv("CFO_CONSUMER_BINARY"))
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
