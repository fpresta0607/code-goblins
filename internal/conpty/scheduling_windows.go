package conpty

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	PROCESS_POWER_THROTTLING = 4
	EXECUTION_SPEED          = 1
)

var (
	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	getProcessInformation = kernel32.NewProc("GetProcessInformation")
	setProcessInformation = kernel32.NewProc("SetProcessInformation")
)

type powerThrottling struct {
	Version, ControlMask, StateMask uint32
}

// KeepCurrentProcessInteractive prevents automatic throttling of a hidden
// process serving interactive clients, as the native terminal host does.
func KeepCurrentProcessInteractive() error {
	return interactiveScheduling(windows.CurrentProcess())
}

// Hidden consoles have no foreground window to earn interactive scheduling.
// Explicit HighQoS prevents automatic efficiency-core throttling, without
// changing priority, affinity, timer policy or other power controls.
func interactiveScheduling(process windows.Handle) error {
	state, err := processPowerPolicy(process)
	if err != nil {
		return err
	}
	if state.ControlMask&EXECUTION_SPEED != 0 && state.StateMask&EXECUTION_SPEED == 0 {
		return nil
	}
	state.ControlMask |= EXECUTION_SPEED
	state.StateMask &^= EXECUTION_SPEED
	result, _, err := setProcessInformation.Call(uintptr(process), PROCESS_POWER_THROTTLING, uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
	if result == 0 {
		return fmt.Errorf("set interactive process power policy: %w", err)
	}
	return nil
}

func processPowerPolicy(process windows.Handle) (powerThrottling, error) {
	state := powerThrottling{Version: 1}
	result, _, err := getProcessInformation.Call(uintptr(process), PROCESS_POWER_THROTTLING, uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
	if result == 0 {
		return state, fmt.Errorf("read process power policy: %w", err)
	}
	return state, nil
}
