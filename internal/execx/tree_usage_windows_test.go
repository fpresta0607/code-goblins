package execx_test

import (
	"context"
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
	"github.com/fpresta0607/code-goblins/internal/execx/treetest"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// usageOnce reads the tree's usage until counts says it has the processes
// it should, for ten seconds at most. Windows can still count a process
// that has exited, and that a wait has seen exit, as active in its job for a
// moment, which a busy machine stretches.
func usageOnce(t *testing.T, tree *execx.Tree, counts func(execx.TreeUsage) bool) execx.TreeUsage {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		usage, err := tree.Usage()
		if err != nil {
			t.Fatal(err)
		}
		if counts(usage) || time.Now().After(deadline) {
			return usage
		}
	}
}

func TestTreeUsageIncludesWorkersAfterTheirParentExits(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	command := execx.Command(os.Args[0], treetest.Args(folder, false)...)
	tree, err := execx.StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End() })
	found, err := treetest.Find(folder)
	t.Cleanup(found.End)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	usage := usageOnce(t, tree, func(usage execx.TreeUsage) bool { return usage.ActiveProcesses == 3 })

	// Assert
	if usage.ActiveProcesses != 3 || usage.TotalProcesses != 4 {
		t.Errorf("usage = %+v, want three live workers and the exited relay", usage)
	}
	// CPU time is charged at the clock's ticks, so workers that mostly wait
	// can read none yet: TestTreeUsageCountsTheCPUTimeOfAWorkerThatExited
	// proves it is read.
	if usage.CommitBytes == 0 || usage.PeakCommitBytes < usage.CommitBytes || usage.SampledPrivateWorkingSetBytes == 0 {
		t.Errorf("usage = %+v, want measured commitment and private working set", usage)
	}
	if err := treetest.Release(folder); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if !found.Child.Ends() {
		t.Fatal("child did not exit")
	}
	after := usageOnce(t, tree, func(usage execx.TreeUsage) bool { return usage.ActiveProcesses == 1 })
	if after.ActiveProcesses != 1 || after.TotalProcesses != 4 || after.CPU < usage.CPU || after.PeakCommitBytes < usage.PeakCommitBytes {
		t.Errorf("usage after parent exit = %+v, before = %+v", after, usage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	final, err := tree.TerminateAndWait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if final.ActiveProcesses != 0 || final.TotalProcesses != 4 || final.CPU < after.CPU || !found.Helper.Ends() {
		t.Errorf("final usage = %+v, want all workers ended and cumulative CPU retained", final)
	}
	if _, err := tree.Usage(); err == nil {
		t.Fatal("measurement after handle closure reported success")
	}
}

func TestTreeTerminationRetainsCustodyWhenCancellationPreventsProof(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	command := execx.Command(os.Args[0], treetest.Args(folder, false)...)
	tree, err := execx.StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End(); _ = command.Wait() })
	found, err := treetest.Find(folder)
	t.Cleanup(found.End)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	_, err = tree.TerminateAndWait(ctx)

	// Assert
	if err == nil {
		t.Fatal("canceled cleanup reported success")
	}
	if _, err := tree.Usage(); err != nil {
		t.Fatalf("uncertain cleanup lost its job handle: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	final, err := tree.TerminateAndWait(ctx)
	if err != nil || final.ActiveProcesses != 0 {
		t.Fatalf("retry cleanup = %+v, %v", final, err)
	}
}

func TestTreeMembersNamesEachProcessStillInTheJob(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	command := execx.Command(os.Args[0], treetest.Args(folder, false)...)
	tree, err := execx.StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	found, err := treetest.Find(folder)
	defer found.End()
	defer func() { tree.End(); command.Wait() }()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	members, err := tree.Members()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	named := make(map[int]string)
	for _, member := range members {
		named[member.PID] = member.Name
	}
	for _, process := range found.Members() {
		if !strings.EqualFold(named[process.PID], filepath.Base(os.Args[0])) {
			t.Fatalf("members = %+v, want pid %d named %s", members, process.PID, filepath.Base(os.Args[0]))
		}
	}
	if _, err := tree.TerminateAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if members, err := tree.Members(); err == nil {
		t.Fatalf("a closed job still names members: %+v", members)
	}
}

func TestTreeUsageCountsTheCPUTimeOfAWorkerThatExited(t *testing.T) {
	const burn = 100 * time.Millisecond
	if os.Getenv("EXECX_TEST_BURN") != "" {
		// Spin until Windows has charged this process the CPU time, which it
		// does at the clock's ticks.
		for spent := time.Duration(0); spent < burn; {
			var creation, exit, kernel, user windows.Filetime
			if err := windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
				os.Exit(2)
			}
			spent = filetimeDuration(kernel) + filetimeDuration(user)
		}
		os.Exit(0)
	}
	// Arrange
	command := execx.Command(os.Args[0], "-test.run=^TestTreeUsageCountsTheCPUTimeOfAWorkerThatExited$")
	command.Env = append(os.Environ(), "EXECX_TEST_BURN=1")
	tree, err := execx.StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End() })
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Act
	usage, err := tree.TerminateAndWait(ctx)

	// Assert
	if err != nil || usage.TotalProcesses != 1 || usage.CPU < burn {
		t.Fatalf("usage = %+v, %v, want the exited worker's %s of CPU time", usage, err, burn)
	}
}

// filetimeDuration is a span of 100-nanosecond intervals as a duration.
func filetimeDuration(span windows.Filetime) time.Duration {
	return time.Duration(uint64(span.HighDateTime)<<32|uint64(span.LowDateTime)) * 100
}

func TestTreeCountsAMemberItMayNotReadInsteadOfFailing(t *testing.T) {
	if os.Getenv("EXECX_TEST_LOCKED") != "" {
		// Deny everyone the rights a sample opens a process with, as a
		// sandboxed browser process can, or the DACL a test names.
		lock := os.Getenv("EXECX_TEST_LOCKED_DACL")
		if lock == "" {
			lock = "D:(D;;0x101410;;;WD)(A;;GA;;;OW)"
		}
		descriptor, err := windows.SecurityDescriptorFromString(lock)
		if err != nil {
			os.Exit(3)
		}
		dacl, _, err := descriptor.DACL()
		if err != nil {
			os.Exit(4)
		}
		if err := windows.SetSecurityInfo(windows.CurrentProcess(), windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
			os.Exit(5)
		}
		if err := os.WriteFile(os.Getenv("EXECX_TEST_LOCKED"), []byte("locked"), 0o600); err != nil {
			os.Exit(6)
		}
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	// Arrange
	ready := filepath.Join(t.TempDir(), "locked")
	command := execx.Command(os.Args[0], "-test.run=^TestTreeCountsAMemberItMayNotReadInsteadOfFailing$")
	command.Env = append(os.Environ(), "EXECX_TEST_LOCKED="+ready)
	tree, err := execx.StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End(); command.Wait() })
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the locked worker never said it was ready")
		}
	}
	// A process holding SeDebugPrivilege, as a CI runner's can, opens any
	// process whatever its DACL says, so the lock proves nothing there.
	// TestTreeCountsAMemberItCannotOpen covers the count on every machine.
	if opened, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(command.Process.Pid)); err == nil {
		windows.CloseHandle(opened)
		t.Skip("this process opens a process whose DACL denies it, so it cannot make a member it may not read")
	}

	// Act
	usage, usageError := tree.Usage()
	members, membersError := tree.Members()

	// Assert
	if usageError != nil || usage.UnreadableMembers != 1 || usage.ActiveProcesses != 1 {
		t.Fatalf("usage = %+v, %v, want the locked worker counted as unreadable", usage, usageError)
	}
	if membersError != nil || len(members) != 1 || members[0].PID != command.Process.Pid {
		t.Fatalf("members = %+v, %v, want the locked worker named as still running", members, membersError)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if final, err := tree.TerminateAndWait(ctx); err != nil || final.ActiveProcesses != 0 {
		t.Fatalf("ending the locked worker = %+v, %v", final, err)
	}
}

// A run's end that must not end a machine service, as Docker Desktop a
// command started serves the whole machine, spares it with what runs under
// it and ends the rest, as End does. Recovery's end spares nothing, since a
// job whose controller died has had its kill-on-close end everything in it.
func TestTreeSparesAMachineServiceOnlyWhereItsEndAsksTo(t *testing.T) {
	if programs := os.Getenv("EXECX_TEST_SERVICE"); programs != "" {
		for _, name := range []string{"com.docker.standin", "worker"} {
			started := exec.Command(filepath.Join(programs, name+".exe"))
			if lock := os.Getenv("EXECX_TEST_LOCKED_WORKER"); name == "worker" && lock != "" {
				// A worker whose DACL denies everyone what lock denies.
				started = exec.Command(os.Args[0], "-test.run=^TestTreeCountsAMemberItMayNotReadInsteadOfFailing$")
				started.Env = append(os.Environ(), "EXECX_TEST_SERVICE=", "EXECX_TEST_LOCKED="+filepath.Join(programs, "locked"), "EXECX_TEST_LOCKED_DACL="+lock)
			}
			if started.Start() != nil {
				os.Exit(8)
			}
			// Written whole and then named, so the test never reads half a pid.
			pid := filepath.Join(programs, name+".pid")
			if os.WriteFile(pid+".tmp", []byte(strconv.Itoa(started.Process.Pid)), 0o600) != nil || os.Rename(pid+".tmp", pid) != nil {
				os.Exit(7)
			}
		}
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	for _, test := range []struct {
		name          string
		end           func(context.Context, *execx.Tree) ([]execx.EndedProcess, error)
		isServiceKept bool
		isSpareNamed  bool
	}{
		{name: "EndAndWait", end: func(ctx context.Context, tree *execx.Tree) ([]execx.EndedProcess, error) {
			_, spared, err := tree.EndAndWait(ctx)
			return spared, err
		}, isServiceKept: true, isSpareNamed: true},
		{name: "End", end: func(_ context.Context, tree *execx.Tree) ([]execx.EndedProcess, error) {
			_, err := tree.End()
			return nil, err
		}, isServiceKept: true},
		{name: "TerminateAndWait", end: func(ctx context.Context, tree *execx.Tree) ([]execx.EndedProcess, error) {
			_, err := tree.TerminateAndWait(ctx)
			return nil, err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			tree, command, pids, held := serviceTree(t, "")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Act
			spared, err := test.end(ctx, tree)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			// Windows can count a job empty a moment before a process its end
			// ended signals, so an ended service is waited for, and a kept
			// one must still run a moment later.
			wait := uint32(5000)
			if test.isServiceKept {
				wait = 500
			}
			event, err := windows.WaitForSingleObject(held["com.docker.standin"], wait)
			if isServiceRunning := err == nil && event == uint32(windows.WAIT_TIMEOUT); isServiceRunning != test.isServiceKept {
				t.Fatalf("the machine service runs: %v, want %v", isServiceRunning, test.isServiceKept)
			}
			wantSpared := 0
			if test.isSpareNamed {
				wantSpared = 1
			}
			if len(spared) != wantSpared || wantSpared == 1 && (spared[0].PID != pids["com.docker.standin"] || !strings.EqualFold(spared[0].Name, "com.docker.standin.exe")) {
				t.Fatalf("spared = %+v, want the machine service %d alone where it is kept", spared, pids["com.docker.standin"])
			}
			if event, err := windows.WaitForSingleObject(held["worker"], 5000); err != nil || event != windows.WAIT_OBJECT_0 {
				t.Fatal("the worker still runs after the tree's end")
			}
			if own, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(command.Process.Pid)); err == nil {
				event, err := windows.WaitForSingleObject(own, 5000)
				windows.CloseHandle(own)
				if err != nil || event != windows.WAIT_OBJECT_0 {
					t.Fatal("the tree's own process still runs after the tree's end")
				}
			}
		})
	}
}

// serviceTree starts a tree whose command starts a stand-in named like
// Docker Desktop and a worker, both running until ended, and returns the
// tree, its command, and the two by name, by pid and held open. A worker
// locked by the DACL lock denies everyone what lock denies, so it is not
// held.
func serviceTree(t *testing.T, lock string) (*execx.Tree, *exec.Cmd, map[string]int, map[string]windows.Handle) {
	t.Helper()
	programs := t.TempDir()
	standin.RemoveAtCleanup(t, programs)
	for _, name := range []string{"com.docker.standin", "worker"} {
		standin.Put(t, filepath.Join(programs, name+".exe"))
	}
	command := execx.Command(os.Args[0], "-test.run=^TestTreeSparesAMachineServiceOnlyWhereItsEndAsksTo$")
	command.Env = append(append(os.Environ(), "EXECX_TEST_SERVICE="+programs), standin.Env("", standin.Rule{Any: true, Hold: true})...)
	isWorkerLocked := lock != ""
	if isWorkerLocked {
		command.Env = append(command.Env, "EXECX_TEST_LOCKED_WORKER="+lock)
	}
	tree, err := execx.StartTree(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End(); _ = command.Wait() })
	if isWorkerLocked {
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(filepath.Join(programs, "locked")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the locked worker never said it was ready")
			}
		}
	}
	pids := map[string]int{}
	held := map[string]windows.Handle{}
	for _, name := range []string{"com.docker.standin", "worker"} {
		pid := filepath.Join(programs, name+".pid")
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if data, err := os.ReadFile(pid); err == nil {
				if pids[name], err = strconv.Atoi(string(data)); err != nil {
					t.Fatal(err)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never started", name)
			}
		}
		if name != "worker" || !isWorkerLocked {
			held[name] = standin.Hold(t, pids[name])
		}
	}
	return tree, command, pids, held
}

// withoutDebugPrivilege disables, until the test ends, this process's
// SeDebugPrivilege, which a CI runner's process can hold and which opens any
// process whatever its DACL says.
func withoutDebugPrivilege(t *testing.T) {
	t.Helper()
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = token.Close() })
	var debug windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeDebugPrivilege"), &debug); err != nil {
		t.Fatal(err)
	}
	disabled := windows.Tokenprivileges{PrivilegeCount: 1, Privileges: [1]windows.LUIDAndAttributes{{Luid: debug}}}
	var previous windows.Tokenprivileges
	var size uint32
	if err := windows.AdjustTokenPrivileges(token, false, &disabled, uint32(unsafe.Sizeof(previous)), &previous, &size); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if previous.PrivilegeCount > 0 {
			_ = windows.AdjustTokenPrivileges(token, false, &previous, 0, nil, nil)
		}
	})
}

// A member this process may not read, as a sandboxed browser process can be,
// is in no reading the end acts on, and one it may not end outlives every
// try. Beside a machine service the end then confirms nothing within its
// caller's deadline, and the job still ends everything it holds when its
// last handle closes, so the member never outlives the run's custody.
func TestTreeEndAndWaitFailsClosedOverAMemberItCannotEnd(t *testing.T) {
	for _, test := range []struct {
		name, lock string
		access     uint32
	}{
		{name: "a member it may not read", lock: "D:(D;;0x101410;;;WD)(A;;GA;;;OW)", access: windows.PROCESS_QUERY_LIMITED_INFORMATION},
		{name: "a member it may not end", lock: "D:(D;;0x1;;;WD)(A;;GA;;;OW)", access: windows.PROCESS_TERMINATE},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			withoutDebugPrivilege(t)
			tree, _, pids, held := serviceTree(t, test.lock)
			// The end below confirms nothing, so the strict end clears the job.
			t.Cleanup(func() { _, _ = tree.TerminateAndWait(context.Background()) })
			if opened, err := windows.OpenProcess(test.access, false, uint32(pids["worker"])); err == nil {
				windows.CloseHandle(opened)
				t.Skip("this process opens a process whose DACL denies it, so it cannot make the member")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			started := time.Now()

			// Act
			_, spared, err := tree.EndAndWait(ctx)

			// Assert
			if err == nil || !strings.Contains(err.Error(), strconv.Itoa(pids["worker"])) || len(spared) != 0 {
				t.Fatalf("end = %+v, %v; want no confirmation, naming the member", spared, err)
			}
			if took := time.Since(started); took > 4*time.Second {
				t.Fatalf("the end took %s, past its caller's deadline of a second", took)
			}
			if isArmed, err := tree.KillsOnCloseForTest(); err != nil || !isArmed {
				t.Fatalf("kill on close armed = %v, %v; want the job still to end everything when it closes", isArmed, err)
			}
			if event, err := windows.WaitForSingleObject(held["com.docker.standin"], 0); err != nil || event != uint32(windows.WAIT_TIMEOUT) {
				t.Fatal("the machine service was ended though the end confirmed nothing")
			}
		})
	}
}
