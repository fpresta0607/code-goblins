package proc

import (
	"errors"
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
	peb, err := processEnvironmentBlock(handle)
	if err != nil {
		return nil, errors.New("process environment unavailable")
	}
	parameters, err := readPointer(handle, peb+pebOffsetProcessParameters)
	if err != nil || parameters == 0 {
		return nil, errors.New("process parameters unavailable")
	}
	address, err := readPointer(handle, parameters+unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.Environment))
	if err != nil || address == 0 {
		return nil, errors.New("process environment unavailable")
	}
	return environmentAt(handle, address)
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
