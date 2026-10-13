package verify

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

// recoveryRole names what this test binary is when a recovery test starts
// it: the controller of an execution, its worker, the worker's child, or a
// controller that leaves a worker outside its job.
const recoveryRole = "VERIFY_RECOVERY_ROLE"

// controlAnExecution runs one execution whose worker leaves a descendant
// running, in the admission store the test names, until it is killed.
func controlAnExecution() int {
	executor := Executor{
		Admission: Admission{Dir: os.Getenv("VERIFY_RECOVERY_ADMISSION"), Who: "a recovery fixture", Limit: time.Minute, Poll: 5 * time.Millisecond},
		Monitor: Monitor{Poll: 10 * time.Millisecond, Headroom: func() (Headroom, error) {
			return Headroom{PhysicalAvailable: 64 << 30, CommitAvailable: 64 << 30}, nil
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	executor.Run(ctx, execx.Request{Dir: os.Getenv("VERIFY_RECOVERY_ROOT"), Name: os.Args[0], Args: os.Args[1:], Env: append(os.Environ(), recoveryRole+"=worker")})
	return 0
}

// strandAWorker records custody as an execution's controller, starts its
// worker suspended and exits before putting it in a job, as a controller
// that ends between the two would.
func strandAWorker() int {
	controller, session, err := currentController()
	if err != nil {
		return 4
	}
	guard := Custody{RunID: "stranded-run", Token: "slot-1:1:1", Job: JobName("stranded-run"), Controller: controller, Session: session, Created: time.Now().UTC()}
	if err := writeCustody(os.Getenv("VERIFY_RECOVERY_ADMISSION"), guard); err != nil {
		return 3
	}
	worker := exec.Command(os.Args[0], os.Args[1:]...)
	worker.Env = append(os.Environ(), recoveryRole+"=stranded")
	worker.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err := worker.Start(); err != nil {
		return 2
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("VERIFY_RECOVERY_ROOT"), "stranded.pid"), []byte(strconv.Itoa(worker.Process.Pid)), 0o600); err != nil {
		return 1
	}
	return 0
}

func writeCustody(admissionDir string, guard Custody) error {
	if err := os.MkdirAll(filepath.Join(admissionDir, "custody"), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(guard)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(admissionDir, "custody", guard.RunID+".json"), data, 0o600)
}

// exitedProcess is the identity of a process that has exited, as an
// abandoned execution's controller is.
func exitedProcess(t *testing.T) (lock.Info, uint32) {
	t.Helper()
	controller, session, err := currentController()
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command(os.Args[0], "-test.run=^$")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(process.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	controller.PID, controller.OwnerPID = process.Process.Pid, process.Process.Pid
	controller.Start = time.Unix(0, creation.Nanoseconds()).UTC()
	return controller, session
}

func readExecution(t *testing.T, path string) ExecutionRecord {
	t.Helper()
	data, err := fsx.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record ExecutionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func waitForFile(t *testing.T, path string, within time.Duration) []byte {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data
		}
	}
	t.Fatalf("%s did not appear within %s", path, within)
	return nil
}

func openProcess(t *testing.T, pidFile string) windows.Handle {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(string(waitForFile(t, pidFile, 20*time.Second))))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		windows.TerminateProcess(handle, 1)
		windows.CloseHandle(handle)
	})
	return handle
}

func hasEnded(handle windows.Handle) bool {
	event, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && event == windows.WAIT_OBJECT_0
}

func TestRecoveryEndsTheJobOfAKilledControllerAndFreesTheTurn(t *testing.T) {
	switch os.Getenv(recoveryRole) {
	case "controller":
		os.Exit(controlAnExecution())
	case "worker":
		// The worker starts its own worker and exits first, so the job holds
		// a process whose parent has gone.
		child := execx.Command(os.Args[0], os.Args[1:]...)
		child.Env = append(os.Environ(), recoveryRole+"=descendant")
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		os.Exit(0)
	case "descendant":
		if err := os.WriteFile("descendant.pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(7)
		}
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	root := t.TempDir()
	requested := admission(t)
	controller := exec.Command(os.Args[0], "-test.run=^TestRecoveryEndsTheJobOfAKilledControllerAndFreesTheTurn$")
	controller.Env = append(os.Environ(), recoveryRole+"=controller", "VERIFY_RECOVERY_ADMISSION="+requested.Dir, "VERIFY_RECOVERY_ROOT="+root)
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { controller.Process.Kill(); controller.Wait() })
	descendant := openProcess(t, filepath.Join(root, "descendant.pid"))
	records, err := filepath.Glob(filepath.Join(requested.Dir, "custody", "*.json"))
	if err != nil || len(records) != 1 {
		t.Fatalf("custody = %v, %v", records, err)
	}
	runID := strings.TrimSuffix(filepath.Base(records[0]), ".json")
	// A second handle keeps the job, and what runs in it, past its
	// controller's end, as anything else holding it open would.
	name, err := windows.UTF16PtrFromString(JobName(runID))
	if err != nil {
		t.Fatal(err)
	}
	held, _, openError := windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW").Call(0x0004, 0, uintptr(unsafe.Pointer(name)))
	if held == 0 {
		t.Fatal(openError)
	}
	t.Cleanup(func() { windows.CloseHandle(windows.Handle(held)) })
	if err := controller.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	controller.Wait()
	if hasEnded(descendant) {
		t.Fatal("the fixture's descendant ended with its controller, so it proves nothing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Act
	turn, err := requested.Wait(ctx)
	defer turn.Release()

	// Assert
	if err != nil {
		t.Fatalf("an abandoned execution kept the machine's turn: %v", err)
	}
	if !hasEnded(descendant) {
		t.Fatal("the turn was given while the abandoned execution's descendant still ran")
	}
	if left, _ := filepath.Glob(filepath.Join(requested.Dir, "custody", "*.json")); len(left) != 0 {
		t.Fatalf("custody left after recovery: %v", left)
	}
	record := readExecution(t, filepath.Join(store, "executions", runID+".json"))
	if record.Status != "failed" || record.Measurement.State != Interrupted || !record.Measurement.CleanupConfirmed || record.Measurement.Usage.ActiveProcesses != 0 || !strings.Contains(record.Failure, "recovered") {
		t.Fatalf("recovered evidence = %+v", record)
	}
}

func TestRecoveryRefusesAWorkerThatNeverJoinedItsJob(t *testing.T) {
	switch os.Getenv(recoveryRole) {
	case "stranding":
		os.Exit(strandAWorker())
	case "stranded":
		os.Exit(6) // Never runs: it stays suspended until the test ends it.
	}
	// Arrange
	root := t.TempDir()
	requested := admission(t)
	controller := exec.Command(os.Args[0], "-test.run=^TestRecoveryRefusesAWorkerThatNeverJoinedItsJob$")
	controller.Env = append(os.Environ(), recoveryRole+"=stranding", "VERIFY_RECOVERY_ADMISSION="+requested.Dir, "VERIFY_RECOVERY_ROOT="+root)
	if err := controller.Run(); err != nil {
		t.Fatal(err)
	}
	stranded := openProcess(t, filepath.Join(root, "stranded.pid"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Act
	refused, refusal := requested.Wait(ctx)
	refused.Release()
	if err := windows.TerminateProcess(stranded, 1); err != nil {
		t.Fatal(err)
	}
	if event, err := windows.WaitForSingleObject(stranded, 5000); err != nil || event != windows.WAIT_OBJECT_0 {
		t.Fatalf("the stranded worker did not end: %d, %v", event, err)
	}
	turn, err := requested.Wait(context.Background())
	defer turn.Release()

	// Assert
	if refusal == nil || !strings.Contains(refusal.Error(), "outside its job") {
		t.Fatalf("a worker outside its job was not counted: %v", refusal)
	}
	if err != nil {
		t.Fatalf("custody stayed after the stranded worker ended: %v", err)
	}
}

func TestRecoveryRefusesAJobItCannotOpenUntilTheJobIsGone(t *testing.T) {
	// Arrange
	requested := admission(t)
	controller, session := exitedProcess(t)
	guard := Custody{RunID: "unreadable-run", Token: "slot-1:1:1", Job: JobName("unreadable-run"), Controller: controller, Session: session, Created: time.Now().UTC()}
	if err := writeCustody(requested.Dir, guard); err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:(D;;GA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(guard.Job)
	if err != nil {
		t.Fatal(err)
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	job, err := windows.CreateJobObject(&attributes, name)
	if err != nil {
		t.Fatal(err)
	}
	isClosed := false
	t.Cleanup(func() {
		if !isClosed {
			windows.CloseHandle(job)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Act
	refused, refusal := requested.Wait(ctx)
	refused.Release()
	windows.CloseHandle(job)
	isClosed = true
	turn, err := requested.Wait(context.Background())
	defer turn.Release()

	// Assert
	if refusal == nil || !strings.Contains(refusal.Error(), "prove its job empty") {
		t.Fatalf("a job recovery could not open was taken for empty: %v", refusal)
	}
	if err != nil {
		t.Fatalf("custody stayed after its job was gone: %v", err)
	}
}

func TestRecoveryNeverPassesAnExecutionItRecovers(t *testing.T) {
	for _, status := range []string{"pending", "failed", "passed"} {
		t.Run(status, func(t *testing.T) {
			// Arrange
			store := t.TempDir()
			t.Setenv("CFO_VERIFY_DIR", store)
			requested := admission(t)
			execution, err := BeginExecution(t.TempDir(), []string{"go", "test"})
			if err != nil {
				t.Fatal(err)
			}
			record := execution.record
			record.Status = status
			if status == "passed" {
				record.Measurement = Measurement{State: Completed, CleanupConfirmed: true, Samples: 1}
			}
			if err := execution.persist(record); err != nil {
				t.Fatal(err)
			}
			controller, session := exitedProcess(t)
			// The controller recorded its custody and ended before it
			// created its job, so no job of that name ever existed.
			guard := Custody{RunID: execution.ID(), Token: "slot-1:1:1", Job: JobName(execution.ID()), Controller: controller, Session: session, Created: time.Now().UTC(), Evidence: execution.Path()}
			if err := writeCustody(requested.Dir, guard); err != nil {
				t.Fatal(err)
			}

			// Act
			turn, err := requested.Wait(context.Background())
			defer turn.Release()

			// Assert
			if err != nil {
				t.Fatalf("an execution whose job never existed kept the turn: %v", err)
			}
			recovered := readExecution(t, execution.Path())
			wanted := "failed"
			if status == "passed" {
				wanted = "passed"
			}
			if recovered.Status != wanted {
				t.Fatalf("recovery turned %s into %s", status, recovered.Status)
			}
			if status != "passed" && (recovered.Measurement.State != Interrupted || !recovered.Measurement.CleanupConfirmed) {
				t.Fatalf("recovered evidence = %+v", recovered)
			}
		})
	}
}

func TestRecoveryLeavesCustodyItCannotProveAbandoned(t *testing.T) {
	live, session, err := currentController()
	if err != nil {
		t.Fatal(err)
	}
	gone, goneSession := exitedProcess(t)
	for name, guard := range map[string]Custody{
		"a live controller":              {Controller: live, Session: session},
		"no controller":                  {Session: session},
		"another run's job":              {Controller: gone, Session: goneSession, Job: JobName("another-run")},
		"a job named in another session": {Controller: gone, Session: goneSession + 1},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			requested := admission(t)
			guard.RunID, guard.Token, guard.Created = "unproven-run", "slot-1:1:1", time.Now().UTC()
			if guard.Job == "" {
				guard.Job = JobName(guard.RunID)
			}
			if err := writeCustody(requested.Dir, guard); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			// Act
			turn, err := requested.Wait(ctx)
			turn.Release()

			// Assert
			if err == nil || !strings.Contains(err.Error(), "unconfirmed worker cleanup") {
				t.Fatalf("unproven custody gave the turn: %v", err)
			}
			if _, err := os.Stat(filepath.Join(requested.Dir, "custody", guard.RunID+".json")); err != nil {
				t.Fatalf("unproven custody was removed: %v", err)
			}
		})
	}
}

func TestTurnReleaseRecoversANestedControllerThatEndedInsideIt(t *testing.T) {
	// Arrange
	requested := admission(t)
	turn, err := requested.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Release()
	controller, session := exitedProcess(t)
	guard := Custody{RunID: "nested-run", Token: turn.Token(), Job: JobName("nested-run"), Controller: controller, Session: session, Created: time.Now().UTC()}
	if err := writeCustody(requested.Dir, guard); err != nil {
		t.Fatal(err)
	}

	// Act
	err = turn.Release()

	// Assert
	if err != nil {
		t.Fatalf("a nested controller that ended kept its holder's turn: %v", err)
	}
	if _, err := os.Stat(filepath.Join(requested.Dir, "slot-1")); !os.IsNotExist(err) {
		t.Fatalf("the turn was not given back: %v", err)
	}
}

func TestRecoveryKeepsACustodyRecordReplacedBeforeItsRemoval(t *testing.T) {
	// Arrange
	requested := admission(t)
	controller, session := exitedProcess(t)
	proved := Custody{RunID: "replaced-run", Token: "slot-1:1:1", Job: JobName("replaced-run"), Controller: controller, Session: session, Created: time.Now().UTC(), dir: filepath.Join(requested.Dir, "custody")}
	replacement := proved
	replacement.Created = proved.Created.Add(time.Second)
	if err := writeCustody(requested.Dir, replacement); err != nil {
		t.Fatal(err)
	}

	// Act
	err := proved.removeIfUnchanged(context.Background())

	// Assert
	if err == nil {
		t.Fatal("recovery removed a custody record other than the one it proved")
	}
	if _, err := os.Stat(filepath.Join(requested.Dir, "custody", "replaced-run.json")); err != nil {
		t.Fatalf("the replacement record is gone: %v", err)
	}
}
