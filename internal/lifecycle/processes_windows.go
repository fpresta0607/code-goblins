package lifecycle

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

var ErrIdentityChanged = errors.New("process identity changed; left untouched")

func Terminate(identity Identity) error {
	return terminate(identity, windows.TerminateProcess)
}

func terminate(identity Identity, stop func(windows.Handle, uint32) error) error {
	if identity.PID <= 0 || identity.Started.IsZero() {
		return ErrIdentityChanged
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(identity.PID))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open process %d: %w", identity.PID, err)
	}
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return fmt.Errorf("read process %d identity: %w", identity.PID, err)
	}
	if !time.Unix(0, creation.Nanoseconds()).Equal(identity.Started) {
		return ErrIdentityChanged
	}
	if result, err := windows.WaitForSingleObject(handle, 0); err == nil && result == windows.WAIT_OBJECT_0 {
		return nil
	}
	if err := stop(handle, 1); err != nil {
		if result, waitErr := windows.WaitForSingleObject(handle, 250); waitErr == nil && result == windows.WAIT_OBJECT_0 {
			return nil
		}
		return fmt.Errorf("terminate process %d: %w", identity.PID, err)
	}
	return nil
}
