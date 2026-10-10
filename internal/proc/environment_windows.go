package proc

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Environment is private input to a scoped connection check. Callers must
// verify process ownership and must never serialize or log this value.
func Environment(pid int) ([]string, error) {
	handle, err := syscall.OpenProcess(processQueryInformation|processVMRead, false, uint32(pid))
	if err != nil {
		return nil, errors.New("process environment unavailable")
	}
	defer syscall.CloseHandle(handle)
	var environment []string
	err = walkSteady(handle, pid, func() (uintptr, error) {
		peb, err := processEnvironmentBlock(handle)
		if err != nil {
			return 0, errors.New("process environment unavailable")
		}
		parameters, err := readPointer(handle, peb+pebOffsetProcessParameters)
		if err != nil || parameters == 0 {
			return 0, errors.New("process parameters unavailable")
		}
		environment, err = steadyEnvironment(handle, pid, parameters, 0, func(address uintptr) ([]string, error) {
			return environmentAt(handle, address)
		})
		return parameters, err
	})
	if err != nil {
		return nil, err
	}
	return environment, nil
}

// environmentFollowed runs once a read has the address of an environment and
// before it reads there. A test moves the environment in it, as a starting
// process does.
var environmentFollowed = func() {}

// steadyEnvironment reads, with read, the environment the parameter block at
// parameters points to, until the block still points there once the read is
// done. followed is the address a caller that copied the block already has,
// or zero for the block to be asked.
//
// A starting process moves its environment into its own heap before it moves
// the block, and frees the one CreateProcess built, while its PEB still
// points at the same block, so walkSteady does not see that move. A read
// that followed the old address read memory as it was freed: it failed with
// "process environment unavailable", as a train's CI did on 2026-10-10 (run
// 38017561464), or it read what was no longer the environment. A block that
// cannot be asked again has moved itself, which walkSteady sees.
func steadyEnvironment(handle syscall.Handle, pid int, parameters, followed uintptr, read func(address uintptr) ([]string, error)) ([]string, error) {
	field := parameters + unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.Environment)
	for range maxWalks {
		if followed == 0 {
			var err error
			if followed, err = readPointer(handle, field); err != nil || followed == 0 {
				return nil, errors.New("process environment unavailable")
			}
		}
		environmentFollowed()
		environment, err := read(followed)
		current, currentErr := readPointer(handle, field)
		if currentErr != nil || current == followed {
			return environment, err
		}
		followed = current
	}
	return nil, fmt.Errorf("process %d: its environment moved during every read", pid)
}

// environmentAt reads the environment block at address in the process
// behind handle.
func environmentAt(handle syscall.Handle, address uintptr) ([]string, error) {
	var units []uint16
	for offset := uintptr(0); offset < 1<<20; {
		size := uintptr(4096) - (address+offset)%4096
		chunk := make([]byte, size)
		if err := readMemory(handle, address+offset, chunk); err != nil {
			return nil, errors.New("process environment unavailable")
		}
		for _, unit := range decodeUTF16(chunk) {
			if unit == 0 && len(units) > 0 && units[len(units)-1] == 0 {
				var values []string
				start := 0
				for i, value := range units {
					if value == 0 {
						values = append(values, syscall.UTF16ToString(units[start:i]))
						start = i + 1
					}
				}
				return values, nil
			}
			units = append(units, unit)
		}
		offset += size
	}
	return nil, errors.New("process environment exceeds its limit")
}
