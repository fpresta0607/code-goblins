package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func restartCFO(ctx context.Context, h home.Home, record supervisor.CFOResume) error {
	args, err := onboarding.ResumeArgs(record.Harness, record.Session)
	if err != nil {
		return err
	}
	program, err := spawn.NativeProgram(record.Harness, args...)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, ".cfo-launch.lock"); err != nil {
		return fmt.Errorf("another CFO launch is in progress: %w", err)
	}
	defer lock.ReleaseExclusiveNamed(h.State, ".cfo-launch.lock")
	current, err := (&supervisor.CFORecovery{State: h.State}).Preview()
	if err != nil {
		return err
	}
	if current.Identity != record.Identity {
		return errors.New("the CFO changed before restart; nothing stopped")
	}
	terminal, readErr := host.ReadRecord(h.State, record.Terminal)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if readErr == nil && host.Running(terminal) {
		if terminal.ChildPID != record.Process.PID {
			return errors.New("the terminal runs a different process; nothing stopped")
		}
		if err := stopCFOProcess(record.Process); err != nil {
			return err
		}
		if err := host.Close(h.State, terminal, 5*time.Second); err != nil {
			return err
		}
	} else if record.Process.Alive() {
		return errors.New("the recorded CFO process is alive but its terminal identity cannot be proved; nothing stopped")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = host.Launch(h.State, []string{self, "host"}, nativeCFOEnvironment(os.Environ()), host.Spec{ID: record.Terminal, Args: program, Dir: h.Root, Cols: 120, Rows: 40})
	if err != nil {
		return fmt.Errorf("the CFO stopped but its conversation could not restart; run goblins resume to retry: %w", err)
	}
	return nil
}

func stopCFOProcess(process lock.Info) error {
	hostname, err := os.Hostname()
	if err != nil || process.Hostname != hostname || process.PID <= 0 || process.Start.IsZero() {
		return errors.New("the CFO process has no verified local identity; nothing stopped")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(process.PID))
	if err != nil {
		return fmt.Errorf("cannot verify the CFO process; nothing stopped: %w", err)
	}
	defer windows.CloseHandle(handle)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return err
	}
	if !time.Unix(0, created.Nanoseconds()).UTC().Equal(process.Start) {
		return errors.New("the CFO process ID was reused; nothing stopped")
	}
	if err := windows.TerminateProcess(handle, 0); err != nil {
		return err
	}
	status, err := windows.WaitForSingleObject(handle, 10000)
	if err != nil {
		return err
	}
	if status != windows.WAIT_OBJECT_0 {
		return errors.New("the CFO process has not ended; no replacement started")
	}
	return nil
}
