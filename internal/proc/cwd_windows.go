package proc

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// Access rights needed to read another process's parameter block.
const (
	processQueryInformation = 0x0400
	processVMRead           = 0x0010
)

// Offsets into the 64-bit process structures walked below. They are fixed by
// the Windows ABI and unchanged since Vista; a build that ever reads a 32-bit
// target instead gets a short or failed read and reports no directory rather
// than a wrong one.
const (
	// pebOffsetProcessParameters is PEB.ProcessParameters.
	pebOffsetProcessParameters = 0x20
	// paramsOffsetCurrentDirectory is
	// RTL_USER_PROCESS_PARAMETERS.CurrentDirectory.DosPath, a UNICODE_STRING
	// whose Length is at +0x00 and whose Buffer pointer is at +0x08.
	paramsOffsetCurrentDirectory = 0x38
	unicodeStringBufferOffset    = 0x08
)

// maxDirectoryBytes bounds the UTF-16 copy taken out of the target process.
// A working directory is a path, and no path reaches this length; the bound
// exists so a garbage Length field cannot turn into a huge allocation.
const maxDirectoryBytes = 64 * 1024

var (
	ntdll                     = syscall.NewLazyDLL("ntdll.dll")
	ntQueryInformationProcess = ntdll.NewProc("NtQueryInformationProcess")
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procReadProcessMemory     = kernel32.NewProc("ReadProcessMemory")
)

// ErrDirectoryUnreadable reports that a process's working directory could not
// be read: it exited mid-walk, or it runs at a privilege this process cannot
// open. Callers report the process without its directory rather than dropping
// it, because a listening port with an unknown directory is still a finding.
var ErrDirectoryUnreadable = errors.New("proc: working directory is unreadable")

// WorkingDirectory returns the directory pid is currently running in.
//
// It exists for the one question a command line cannot answer. A dev server
// started as `next start -p 3300` carries no path at all, so the worktree it
// is holding open - the whole point of finding it - is invisible to every
// process listing. Windows keeps that directory in the process's own
// parameter block, and reading it needs one query plus three small reads out
// of the target's memory, which is what this does.
//
// Nothing is written and no handle outlives the call.
func WorkingDirectory(pid int) (string, error) {
	handle, err := syscall.OpenProcess(processQueryInformation|processVMRead, false, uint32(pid))
	if err != nil {
		return "", fmt.Errorf("%w: open process %d: %v", ErrDirectoryUnreadable, pid, err)
	}
	defer syscall.CloseHandle(handle)

	peb, err := processEnvironmentBlock(handle)
	if err != nil {
		return "", err
	}
	parameters, err := readPointer(handle, peb+pebOffsetProcessParameters)
	if err != nil {
		return "", err
	}
	if parameters == 0 {
		return "", fmt.Errorf("%w: process %d has no parameter block", ErrDirectoryUnreadable, pid)
	}

	descriptor := make([]byte, 16)
	if err := readMemory(handle, parameters+paramsOffsetCurrentDirectory, descriptor); err != nil {
		return "", err
	}
	length := int(*(*uint16)(unsafe.Pointer(&descriptor[0])))
	buffer := uintptr(*(*uint64)(unsafe.Pointer(&descriptor[unicodeStringBufferOffset])))
	if length == 0 || buffer == 0 {
		return "", fmt.Errorf("%w: process %d reports no working directory", ErrDirectoryUnreadable, pid)
	}
	if length%2 != 0 || length > maxDirectoryBytes {
		return "", fmt.Errorf("%w: process %d reports a %d byte directory", ErrDirectoryUnreadable, pid, length)
	}

	raw := make([]byte, length)
	if err := readMemory(handle, buffer, raw); err != nil {
		return "", err
	}
	return syscall.UTF16ToString(decodeUTF16(raw)), nil
}

// processEnvironmentBlock returns the address of the target's PEB.
// PROCESS_BASIC_INFORMATION is 48 bytes on 64-bit Windows and holds
// PebBaseAddress at offset 8.
func processEnvironmentBlock(handle syscall.Handle) (uintptr, error) {
	var information [48]byte
	var returned uint32
	status, _, _ := ntQueryInformationProcess.Call(
		uintptr(handle),
		0, // ProcessBasicInformation
		uintptr(unsafe.Pointer(&information[0])),
		uintptr(len(information)),
		uintptr(unsafe.Pointer(&returned)),
	)
	if status != 0 {
		return 0, fmt.Errorf("%w: NtQueryInformationProcess returned 0x%x", ErrDirectoryUnreadable, status)
	}
	peb := uintptr(*(*uint64)(unsafe.Pointer(&information[8])))
	if peb == 0 {
		return 0, fmt.Errorf("%w: process reports no environment block", ErrDirectoryUnreadable)
	}
	return peb, nil
}

func readPointer(handle syscall.Handle, address uintptr) (uintptr, error) {
	buffer := make([]byte, 8)
	if err := readMemory(handle, address, buffer); err != nil {
		return 0, err
	}
	return uintptr(*(*uint64)(unsafe.Pointer(&buffer[0]))), nil
}

// readMemory fills buffer from the target's address space, and treats a short
// read as a failure: a partially read path is a wrong path, and a wrong path
// here would attribute a live goblin's server to somebody else.
func readMemory(handle syscall.Handle, address uintptr, buffer []byte) error {
	var read uintptr
	ok, _, err := procReadProcessMemory.Call(
		uintptr(handle),
		address,
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&read)),
	)
	if ok == 0 {
		return fmt.Errorf("%w: read %d bytes at 0x%x: %v", ErrDirectoryUnreadable, len(buffer), address, err)
	}
	if int(read) != len(buffer) {
		return fmt.Errorf("%w: read %d of %d bytes at 0x%x", ErrDirectoryUnreadable, read, len(buffer), address)
	}
	return nil
}

// decodeUTF16 reinterprets a little-endian UTF-16 byte run as code units.
func decodeUTF16(raw []byte) []uint16 {
	units := make([]uint16, len(raw)/2)
	for index := range units {
		units[index] = uint16(raw[2*index]) | uint16(raw[2*index+1])<<8
	}
	return units
}
