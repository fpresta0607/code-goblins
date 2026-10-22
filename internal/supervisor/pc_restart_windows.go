package supervisor

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

var initiateShutdown = windows.NewLazySystemDLL("advapi32.dll").NewProc("InitiateShutdownW")

const (
	// shutdownRestart restarts the PC once its apps have closed, and
	// shutdownRestartApps starts again, after he signs in, each app that
	// registered to be: no app is closed by force, so one holding unsaved
	// work asks him first, as a restart from the Start menu does.
	shutdownRestart     = 0x4
	shutdownRestartApps = 0x80
	// reasonOtherPlanned is the restart's reason in Windows' event log:
	// Other (Planned).
	reasonOtherPlanned = 0x80000000
)

// RestartWindows restarts Windows now, as the last step of the Overlord's
// Restart PC. A test binary never restarts the PC it runs on.
func RestartWindows() error {
	if testing.Testing() {
		return errors.New("a test never restarts Windows")
	}
	if err := enableShutdownPrivilege(); err != nil {
		return err
	}
	message, err := windows.UTF16PtrFromString("Code Goblins is restarting the PC to free the memory the kernel's pools hold.")
	if err != nil {
		return err
	}
	if code, _, _ := initiateShutdown.Call(0, uintptr(unsafe.Pointer(message)), 0, shutdownRestart|shutdownRestartApps, reasonOtherPlanned); code != 0 {
		return fmt.Errorf("InitiateShutdown: %w", windows.Errno(code))
	}
	return nil
}

// enableShutdownPrivilege turns on the right to shut the PC down, which
// every user holds and no process has on until it asks.
func enableShutdownPrivilege() error {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return fmt.Errorf("open this process's token: %w", err)
	}
	defer token.Close()
	privileges := windows.Tokenprivileges{PrivilegeCount: 1}
	privileges.Privileges[0].Attributes = windows.SE_PRIVILEGE_ENABLED
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeShutdownPrivilege"), &privileges.Privileges[0].Luid); err != nil {
		return fmt.Errorf("look up the shutdown privilege: %w", err)
	}
	// A token that does not hold the privilege is left as it was, and
	// InitiateShutdown then says the privilege is not held.
	if err := windows.AdjustTokenPrivileges(token, false, &privileges, 0, nil, nil); err != nil {
		return fmt.Errorf("enable the shutdown privilege: %w", err)
	}
	return nil
}

// EndHeavyRuns ends every run in the machine's heavy line, each that holds a
// turn and each that waits for one, and names each it ended. A run is a cfo
// or goblins process whose command runs as a tree in a job that ends with
// it, so ending the process ends its command and gives its turn back.
func EndHeavyRuns() ([]string, error) {
	holding, waiting, err := verify.MachineLine()
	if err != nil {
		return nil, err
	}
	processes, err := proc.Processes()
	if err != nil {
		return nil, err
	}
	started := map[int]proc.Entry{}
	for _, entry := range processes {
		started[entry.PID] = entry
	}
	var ended []string
	var problems error
	for _, run := range append(holding, waiting...) {
		entry, isRunning := started[run.PID]
		if !isRunning {
			continue
		}
		err := proc.TerminateVerified(run.PID, entry.Start, func(arguments []string) error {
			if len(arguments) == 0 {
				return fmt.Errorf("process %d names no program", run.PID)
			}
			if program := strings.TrimSuffix(strings.ToLower(filepath.Base(arguments[0])), ".exe"); program != "cfo" && program != "goblins" {
				return fmt.Errorf("process %d runs %s, not a run of the heavy line", run.PID, arguments[0])
			}
			return nil
		})
		if err != nil {
			problems = errors.Join(problems, fmt.Errorf("%s (pid %d): %w", run.Who, run.PID, err))
			continue
		}
		ended = append(ended, fmt.Sprintf("%s (pid %d)", run.Who, run.PID))
	}
	return ended, problems
}
