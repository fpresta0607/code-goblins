package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

var ErrIdentityChanged = errors.New("process identity changed; left untouched")

const STILL_ACTIVE uint32 = 259

// Terminate reports whether a stopped process is still finishing Windows teardown.
func Terminate(ctx context.Context, identity Identity) (bool, error) {
	return terminate(ctx, identity, windows.TerminateProcess, windows.GetExitCodeProcess)
}

func terminate(ctx context.Context, identity Identity, stop func(windows.Handle, uint32) error, exitCode func(windows.Handle, *uint32) error) (bool, error) {
	if identity.PID <= 0 || identity.Started.IsZero() {
		return false, ErrIdentityChanged
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(identity.PID))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open process %d: %w", identity.PID, err)
	}
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return false, fmt.Errorf("read process %d identity: %w", identity.PID, err)
	}
	if !time.Unix(0, creation.Nanoseconds()).Equal(identity.Started) {
		return false, ErrIdentityChanged
	}
	if result, err := windows.WaitForSingleObject(handle, 0); err == nil && result == windows.WAIT_OBJECT_0 {
		return false, nil
	}
	var code uint32
	if err := exitCode(handle, &code); err != nil {
		return false, fmt.Errorf("read process %d exit status: %w", identity.PID, err)
	}
	var stopErr error
	if code == STILL_ACTIVE {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		stopErr = stop(handle, 1)
	}
	for {
		if err := exitCode(handle, &code); err != nil {
			return false, fmt.Errorf("read process %d exit status: %w", identity.PID, err)
		}
		// Exit status ends execution; an unsignaled handle can retain Windows
		// teardown and memory for much longer, so keep that identity visible.
		if code != STILL_ACTIVE {
			result, err := windows.WaitForSingleObject(handle, 0)
			return err != nil || result != windows.WAIT_OBJECT_0, nil
		}
		if stopErr != nil {
			return false, fmt.Errorf("terminate process %d: %w", identity.PID, stopErr)
		}
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("process %d still active: %w", identity.PID, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
