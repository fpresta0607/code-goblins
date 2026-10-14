package voice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestProtocolCrashRetirementReportsTheOwnedStopError(t *testing.T) {
	voice, record := engine(t, "crashes", 8<<30)
	text, recognitionErr := voice.Recognize(context.Background(), []byte("RIFF-stop-diagnostic"))
	if recognitionErr == nil || text != "" || !strings.Contains(recognitionErr.Error(), "owned fixture crashed") {
		t.Fatalf("the actual crash path returned %q, %v", text, recognitionErr)
	}
	if run := ran(t, record); run.Payload != "RIFF-stop-diagnostic" {
		t.Fatalf("the stock pipe command did not receive the actual request: %+v", run)
	}
	voice.mutex.Lock()
	owned := voice.worker
	var stopErr error
	isExitObserved := false
	if owned != nil {
		stopErr = owned.shutdownErr
		select {
		case <-owned.exited:
			isExitObserved = true
		default:
		}
	}
	voice.mutex.Unlock()
	if owned == nil {
		t.Fatal("the residual stop error was not observed; no diagnostic failure evidence")
	}
	var nativeErr *os.SyscallError
	var nativeCode syscall.Errno
	hasNativeCall := errors.As(stopErr, &nativeErr)
	hasNativeCode := errors.As(stopErr, &nativeCode)
	waitStatus := uint32(syscall.WAIT_FAILED)
	var waitErr error
	var exitCode uint32
	var exitCodeErr error
	handleErr := owned.command.Process.WithHandle(func(handle uintptr) {
		waitStatus, waitErr = syscall.WaitForSingleObject(syscall.Handle(handle), 0)
		exitCodeErr = syscall.GetExitCodeProcess(syscall.Handle(handle), &exitCode)
	})
	isExitObservedAfter := false
	select {
	case <-owned.exited:
		isExitObservedAfter = true
	default:
	}
	t.Fatalf("crashes retained the failed child: stop_error=%v; native_call=%v; has_native_call=%t; native_code=%d; has_native_code=%t; exited_before=%t; has_native_snapshot=%t; handle_error=%v; wait_status=%d; wait_error=%v; exit_code=%d; exit_code_error=%v; exited_after=%t; recognition_error=%v", stopErr, nativeErr, hasNativeCall, nativeCode, hasNativeCode, isExitObserved, handleErr == nil, handleErr, waitStatus, waitErr, exitCode, exitCodeErr, isExitObservedAfter, recognitionErr)
}

func TestRetirementKeepsANativelyCompletedChildUntilCommandWaitFinishes(t *testing.T) {
	voice, record := engine(t, "crashes", 8<<30)
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"voice-worker", filepath.Join(voice.folder(voice.Settings.Engine), voice.Settings.Program)}
	for _, argument := range voice.Settings.Args {
		arguments = append(arguments, strings.ReplaceAll(argument, "{model}", voice.folder(voice.Settings.Model)))
	}
	command := execx.Command(program, arguments...)
	command.Dir = voice.folder(voice.Settings.Engine)
	command.Env = []string{}
	for _, name := range []string{"SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC", "PATH", "PATHEXT", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	owned := &worker{command: command, input: input, output: output, exited: make(chan struct{})}
	command.Stderr = owned
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	voice.mutex.Lock()
	voice.worker = owned
	voice.mutex.Unlock()
	isWaitStarted := false
	defer func() {
		if !isWaitStarted {
			go func() {
				_ = command.Wait()
				close(owned.exited)
			}()
		}
	}()
	if err := writeFrame(input, []byte("RIFF-native-completed")); err != nil {
		t.Fatal(err)
	}
	if err := command.Process.WithHandle(func(handle uintptr) {
		status, err := syscall.WaitForSingleObject(syscall.Handle(handle), 5000)
		if err != nil || status != syscall.WAIT_OBJECT_0 {
			t.Fatalf("the native completion premise answered %d, %v", status, err)
		}
		var code uint32
		if err := syscall.GetExitCodeProcess(syscall.Handle(handle), &code); err != nil || code != 255 {
			t.Fatalf("the native crash premise answered %d, %v", code, err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := command.Process.Signal(os.Interrupt); err != syscall.Errno(syscall.EWINDOWS) {
		t.Fatalf("the not-yet-waited handle premise answered %v", err)
	}
	retired := make(chan error, 1)
	go func() { retired <- voice.retire(owned) }()
	deadline := time.Now().Add(5 * time.Second)
	hasRetirementLock := false
	for time.Now().Before(deadline) {
		select {
		case err := <-retired:
			t.Fatalf("retirement finished before command Wait: %v", err)
		default:
		}
		if !voice.mutex.TryLock() {
			hasRetirementLock = true
			break
		}
		voice.mutex.Unlock()
		time.Sleep(time.Millisecond)
	}
	if !hasRetirementLock {
		t.Fatal("retirement did not reach the command Wait barrier")
	}
	select {
	case err := <-retired:
		t.Fatalf("retirement dropped the completed child before command Wait: %v", err)
	default:
	}
	isWaitStarted = true
	go func() {
		_ = command.Wait()
		close(owned.exited)
	}()
	select {
	case err := <-retired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retirement did not finish after command Wait")
	}
	assertWorkerReaped(t, voice, owned)
	if command.ProcessState == nil || command.ProcessState.ExitCode() != 255 || !strings.Contains(string(owned.log), "owned fixture crashed") {
		t.Fatalf("the completed child premise failed: %v, %q", command.ProcessState, owned.log)
	}
	if run := ran(t, record); run.Payload != "RIFF-native-completed" || run.PID != command.Process.Pid {
		t.Fatalf("the owned stock pipe command ran as %+v", run)
	}
}

func TestWindowsKillRaceAcceptsOnlyObservedOwnedChildCompletion(t *testing.T) {
	voice, _ := engine(t, "crashes", 8<<30)
	owned, err := voice.startWorker()
	if err != nil {
		t.Fatal(err)
	}
	voice.mutex.Lock()
	voice.worker = owned
	voice.mutex.Unlock()
	owned.logMutex.Lock()
	isLogLocked := true
	defer func() {
		if isLogLocked {
			owned.logMutex.Unlock()
		}
	}()
	killCalls := 0
	err = owned.command.Process.WithHandle(func(handle uintptr) {
		err := killWorkerWithHandle(owned.command.Process, func() error {
			killCalls++
			if err := writeFrame(owned.input, []byte("RIFF-kill-race")); err != nil {
				t.Fatal(err)
			}
			status, err := syscall.WaitForSingleObject(syscall.Handle(handle), 5000)
			if err != nil || status != syscall.WAIT_OBJECT_0 {
				t.Fatalf("the owned completion premise answered %d, %v", status, err)
			}
			err = syscall.TerminateProcess(syscall.Handle(handle), 1)
			if err != syscall.ERROR_ACCESS_DENIED {
				t.Fatalf("the completed-child Kill premise answered %v", err)
			}
			return os.NewSyscallError("TerminateProcess", err)
		})
		if err != nil {
			t.Fatalf("a failed Kill retained an observed completed child: %v", err)
		}
	})
	if err != nil || killCalls != 1 {
		t.Fatalf("the owned handle acquisition answered %v after %d Kill calls", err, killCalls)
	}
	select {
	case <-owned.exited:
		t.Fatal("the held stderr completion did not keep command Wait pending")
	default:
	}
	owned.logMutex.Unlock()
	isLogLocked = false
	select {
	case <-owned.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("command Wait did not complete after the exit barrier")
	}
	if owned.command.ProcessState == nil || owned.command.ProcessState.ExitCode() != 255 {
		t.Fatalf("the controlled owned crash answered %v", owned.command.ProcessState)
	}
	if err := voice.retire(owned); err != nil {
		t.Fatal(err)
	}
	assertWorkerReaped(t, voice, owned)
}

func TestWindowsKillCompletionCheckPreservesWrappedLiveErrors(t *testing.T) {
	voice, _ := engine(t, "hears", 8<<30)
	owned, err := voice.startWorker()
	if err != nil {
		t.Fatal(err)
	}
	voice.mutex.Lock()
	voice.worker = owned
	voice.mutex.Unlock()
	for _, killErr := range []error{
		os.NewSyscallError("DuplicateHandle", syscall.ERROR_ACCESS_DENIED),
		os.NewSyscallError("TerminateProcess", syscall.ERROR_ACCESS_DENIED),
		os.NewSyscallError("TerminateProcess", syscall.EINVAL),
	} {
		killCalls := 0
		err := killWorkerWithHandle(owned.command.Process, func() error {
			killCalls++
			return killErr
		})
		if err != killErr || killCalls != 1 {
			t.Fatalf("a genuine wrapped live Kill failure changed: %v, want %v, calls=%d", err, killErr, killCalls)
		}
		if err := owned.command.Process.WithHandle(func(handle uintptr) {
			status, err := syscall.WaitForSingleObject(syscall.Handle(handle), 0)
			if err != nil || status != syscall.WAIT_TIMEOUT {
				t.Fatalf("the live owned-child premise answered %d, %v", status, err)
			}
		}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-owned.exited:
			t.Fatal("a refused live Kill dropped the owned worker")
		default:
		}
	}
}

func TestRetirementWaitsForExitCompletionAfterWindowsReleasesTheHandle(t *testing.T) {
	voice, record := engine(t, "crashes", 8<<30)
	owned, err := voice.startWorker()
	if err != nil {
		t.Fatal(err)
	}
	voice.mutex.Lock()
	voice.worker = owned
	voice.mutex.Unlock()
	owned.logMutex.Lock()
	isLogLocked := true
	defer func() {
		if isLogLocked {
			owned.logMutex.Unlock()
		}
	}()
	if err := writeFrame(owned.input, []byte("RIFF-exit-race")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := owned.command.Process.Signal(os.Interrupt)
		if err == syscall.EINVAL {
			break
		}
		if err != syscall.Errno(syscall.EWINDOWS) {
			t.Fatalf("the released-handle premise answered %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("Windows Wait did not release the owned crash handle")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-owned.exited:
		t.Fatal("the held stderr completion did not keep Wait pending")
	default:
	}
	retired := make(chan error, 1)
	go func() { retired <- voice.retire(owned) }()
	hasRetirementLock := false
	for time.Now().Before(deadline) {
		select {
		case err := <-retired:
			t.Fatalf("retirement finished before the held Wait completion: %v", err)
		default:
		}
		if !voice.mutex.TryLock() {
			hasRetirementLock = true
			break
		}
		voice.mutex.Unlock()
		time.Sleep(time.Millisecond)
	}
	if !hasRetirementLock {
		t.Fatal("retirement did not enter while Wait completion was held")
	}
	select {
	case err := <-retired:
		t.Fatalf("retirement dropped ownership before Wait completion: %v", err)
	default:
	}
	owned.logMutex.Unlock()
	isLogLocked = false
	select {
	case err := <-retired:
		if err != nil {
			t.Fatalf("retirement after releasing the exit barrier: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retirement stayed blocked after Wait completion")
	}
	assertWorkerReaped(t, voice, owned)
	if owned.command.ProcessState == nil || owned.command.ProcessState.ExitCode() != 255 || !strings.Contains(string(owned.log), "owned fixture crashed") {
		t.Fatalf("the controlled crash premise failed: %v, %q", owned.command.ProcessState, owned.log)
	}
	writeWorkerFixture(t, voice, "hears", record)
	if text, err := voice.Recognize(context.Background(), []byte("RIFF-after-exit-race")); err != nil || text == "" {
		t.Fatalf("a fresh request could not start after the exit race: %q, %v", text, err)
	}
}
