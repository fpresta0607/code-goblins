package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestPauseReportsDetachedWindowsTeardownWithoutWaitingForTheHandle(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		stopError  error
		exitCode   uint32
		shouldFail bool
	}{
		{name: "successful termination pending teardown", exitCode: 1},
		{name: "access denied already terminating", stopError: windows.ERROR_ACCESS_DENIED, exitCode: 1},
		{name: "access denied still active", stopError: windows.ERROR_ACCESS_DENIED, exitCode: 259, shouldFail: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service, meta := lifecycleFixture(t)
			child := exec.Command(os.Args[0], "-test.run=^TestLifecycleProcessFixture$")
			child.Dir = meta.TaskTmp
			child.Env = append(os.Environ(), "CFO_LIFECYCLE_FIXTURE=1")
			child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB | windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
			started, exists := proc.StartTime(child.Process.Pid)
			if !exists {
				t.Fatal("detached fixture did not start")
			}
			identity := Identity{PID: child.Process.Pid, Started: started}
			var wasTerminated atomic.Bool
			service.Operations.Stop = func(ctx context.Context, _ state.TaskMeta, record *state.Lifecycle) ([]string, error) {
				stopped, teardown, err := stopResources(ctx, Resources{Directories: []string{meta.TaskTmp}}, func(ctx context.Context, found Identity) (bool, error) {
					if found != identity {
						return false, errors.New("unexpected owned identity")
					}
					return terminate(ctx, found, func(handle windows.Handle, _ uint32) error {
						if wait, err := windows.WaitForSingleObject(handle, 0); err != nil || wait != uint32(windows.WAIT_TIMEOUT) {
							return errors.New("fixture must retain an unsignaled handle")
						}
						wasTerminated.Store(true)
						return testCase.stopError
					}, func(_ windows.Handle, code *uint32) error {
						*code = 259
						if wasTerminated.Load() {
							*code = testCase.exitCode
						}
						return nil
					})
				})
				record.Teardown = teardown
				return stopped, err
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			begin := time.Now()
			record, err := service.Run(ctx, Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-teardown", Action: "pause"})
			if time.Since(begin) >= 10*time.Second {
				t.Fatal("Pause exceeded its ten-second deadline")
			}
			if testCase.shouldFail {
				if err == nil || record.Phase != "failed" {
					t.Fatalf("still-active process incorrectly paused: %+v %v", record, err)
				}
				return
			}
			if err != nil || record.Phase != "paused" {
				t.Fatalf("exit status must complete Pause while teardown remains: %+v %v", record, err)
			}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			var persisted struct {
				Teardown []struct {
					PID     int
					Started time.Time
				}
			}
			if err := json.Unmarshal(data, &persisted); err != nil {
				t.Fatal(err)
			}
			if len(persisted.Teardown) != 1 || persisted.Teardown[0].PID != identity.PID || !persisted.Teardown[0].Started.Equal(identity.Started) {
				t.Fatalf("Pause did not retain the birth-checked teardown identity: %s", data)
			}
			lines, err := state.TailStatus(service.StateDir, meta.ID, 10)
			if err != nil || !strings.Contains(strings.Join(lines, "\n"), "finishing Windows teardown") {
				t.Fatalf("status hid teardown: %v %v", lines, err)
			}
			isResumed := false
			service.Operations.Resume = func(_ context.Context, _ state.TaskMeta, prior state.Lifecycle) error {
				isResumed = len(prior.Teardown) == 1
				return nil
			}
			resumed, err := service.Run(ctx, Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-teardown", Action: "resume"})
			if err != nil || !isResumed || resumed.Phase != "running" || len(resumed.Teardown) != 1 {
				t.Fatalf("Resume waited for or lost teardown: %+v %v", resumed, err)
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = child.Wait()
			fresh, err := state.ReadLifecycle(service.StateDir, meta.ID)
			if err != nil || len(fresh.Teardown) != 0 {
				t.Fatalf("fresh status retained vanished teardown: %+v %v", fresh, err)
			}
		})
	}
}

func TestStopResourcesIssuesEveryTerminationBeforeWaiting(t *testing.T) {
	directory := t.TempDir()
	for range 3 {
		child := exec.Command(os.Args[0], "-test.run=^TestLifecycleProcessFixture$")
		child.Dir = directory
		child.Env = append(os.Environ(), "CFO_LIFECYCLE_FIXTURE=1")
		child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB | windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	}
	var requested atomic.Int32
	allRequested := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stopped, _, err := stopResources(ctx, Resources{Directories: []string{directory}}, func(ctx context.Context, identity Identity) (bool, error) {
		if requested.Add(1) == 3 {
			close(allRequested)
		}
		select {
		case <-allRequested:
			return Terminate(ctx, identity)
		case <-time.After(time.Second):
			return false, errors.New("cleanup waited before issuing the other terminations")
		}
	})
	if err != nil || len(stopped) != 3 {
		t.Fatalf("requests were serialized: stopped=%v error=%v", stopped, err)
	}
}
