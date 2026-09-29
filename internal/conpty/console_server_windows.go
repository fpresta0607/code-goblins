package conpty

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

var consoleCreation sync.Mutex

func createInteractiveConsole(size windows.Coord, input, output windows.Handle, console *windows.Handle) error {
	consoleCreation.Lock()
	defer consoleCreation.Unlock()
	var started, finished windows.Filetime
	windows.GetSystemTimeAsFileTime(&started)
	if err := windows.CreatePseudoConsole(size, input, output, 0, console); err != nil {
		return err
	}
	windows.GetSystemTimeAsFileTime(&finished)
	if err := scheduleConsoleServer(started, finished); err != nil {
		windows.ClosePseudoConsole(*console)
		return err
	}
	return nil
}

// ConPTY does not expose its console server's process handle. Find only the
// new direct child from this serialized creation window, then verify its
// creation time and image on the handle used for the policy change.
func scheduleConsoleServer(started, finished windows.Filetime) error {
	processes, err := proc.Processes()
	if err != nil {
		return err
	}
	var candidate windows.Handle
	defer func() {
		if candidate != 0 {
			windows.CloseHandle(candidate)
		}
	}()
	for _, process := range processes {
		if process.ParentPID != os.Getpid() || !strings.EqualFold(process.ExeBase, "conhost.exe") {
			continue
		}
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_SET_INFORMATION, false, uint32(process.PID))
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			continue
		}
		if err != nil {
			return fmt.Errorf("conpty: open console server: %w", err)
		}
		var creation, exit, kernel, user windows.Filetime
		if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
			windows.CloseHandle(handle)
			return fmt.Errorf("conpty: console server identity: %w", err)
		}
		if creation.Nanoseconds() < started.Nanoseconds() || creation.Nanoseconds() > finished.Nanoseconds() {
			windows.CloseHandle(handle)
			continue
		}
		if candidate != 0 {
			windows.CloseHandle(handle)
			return fmt.Errorf("conpty: multiple new console servers in the creation window")
		}
		candidate = handle
		var image [windows.MAX_PATH]uint16
		length := uint32(len(image))
		if err := windows.QueryFullProcessImageName(candidate, 0, &image[0], &length); err != nil {
			return fmt.Errorf("conpty: console server image: %w", err)
		}
		systemDirectory, err := windows.GetSystemDirectory()
		if err != nil {
			return err
		}
		if !strings.EqualFold(windows.UTF16ToString(image[:length]), filepath.Join(systemDirectory, "conhost.exe")) {
			return fmt.Errorf("conpty: console server image is not the system conhost")
		}
	}
	if candidate == 0 {
		return fmt.Errorf("conpty: no new console server found")
	}
	return interactiveScheduling(candidate)
}
