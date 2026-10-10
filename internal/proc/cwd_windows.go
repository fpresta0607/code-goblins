package proc

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Access rights needed to read another process's parameter block.
const (
	processQueryInformation = 0x0400
	processVMRead           = 0x0010
)

// Offsets into the 64-bit process structures walked below. They are fixed by
// the Windows ABI and unchanged since Vista; a build that ever reads a 32-bit
// target instead gets a short or failed read and reports no value rather
// than a wrong one.
const (
	// pebOffsetProcessParameters is PEB.ProcessParameters.
	pebOffsetProcessParameters = 0x20
	// paramsOffsetCurrentDirectory is
	// RTL_USER_PROCESS_PARAMETERS.CurrentDirectory.DosPath and
	// paramsOffsetCommandLine its CommandLine, each a UNICODE_STRING whose
	// Length is at +0x00 and whose Buffer pointer is at +0x08.
	paramsOffsetCurrentDirectory = 0x38
	paramsOffsetCommandLine      = 0x70
	unicodeStringBufferOffset    = 0x08
)

// maxParameterBytes bounds the UTF-16 copy taken out of the target process.
// A path or a command line (at most 32767 characters) fits; the bound exists
// so a garbage Length field cannot turn into a huge allocation.
const maxParameterBytes = 64 * 1024

// maxEnvironmentBytes bounds the copy of a process's environment.
const maxEnvironmentBytes = 1 << 20

// A process moves its environment and then its parameter block into its own
// heap as it starts, frees the block CreateProcess built, and can put
// something else at that address at once. It points its PEB at the new block
// before it frees the old one, so a walk that still finds the PEB pointing at
// the block it started from once it is done read nothing freed. One that does
// not may have read a partial copy, zeros or another allocation's bytes, and
// walks the new block again. The block moves once, so maxWalks bounds a
// target that never holds still.
const maxWalks = 3

// errKeptMoving reports a process whose parameter block moved during every
// walk of it.
var errKeptMoving = errors.New("parameter block moved during every read")

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

// ErrCommandLineUnreadable reports that a process's command line could not
// be read, for the same reasons as its working directory.
var ErrCommandLineUnreadable = errors.New("proc: command line is unreadable")

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
	directory, err := parameterString(pid, paramsOffsetCurrentDirectory)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDirectoryUnreadable, err)
	}
	return directory, nil
}

// CommandLine returns the command line pid was started with, from the same
// parameter block, so a process can be named by what it runs and not only
// by its executable.
func CommandLine(pid int) (string, error) {
	line, err := parameterString(pid, paramsOffsetCommandLine)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
	}
	return line, nil
}

// Arguments returns the arguments pid was started with, split by Windows'
// own CommandLineToArgvW, the rule the program itself read them by.
func Arguments(pid int) ([]string, error) {
	line, err := CommandLine(pid)
	if err != nil {
		return nil, err
	}
	return splitCommandLine(line)
}

// Parameters returns the directory pid is running in and the arguments it was
// started with, read through one handle and one steady walk of its parameter
// block. A fresh first-page snapshot can contain both strings, avoiding
// separate remote reads for them. Values outside that snapshot are read
// individually. A value that could not be read is empty, and err says why.
func Parameters(pid int) (string, []string, error) {
	directory, arguments, _, err := readParameters(pid, false)
	return directory, arguments, err
}

// ParametersAndEnvironment is Parameters with the process's environment,
// read through the same handle and the same walk. Each read of another
// process's memory took a millisecond or more on 2026-10-09, when a pass
// over 500 processes spent 4 seconds on their parameters and 5 more on
// their environments read page by page, so the environment is copied in one
// read of the size its block records. One that could not be read is nil. It
// is private to its process: a caller checks it and never keeps, serializes
// or logs it.
func ParametersAndEnvironment(pid int) (string, []string, []string, error) {
	return readParameters(pid, true)
}

func readParameters(pid int, withEnvironment bool) (string, []string, []string, error) {
	unreadable := func(err error) error {
		return fmt.Errorf("%w: %w: %v", ErrDirectoryUnreadable, ErrCommandLineUnreadable, err)
	}
	handle, err := syscall.OpenProcess(processQueryInformation|processVMRead, false, uint32(pid))
	if err != nil {
		return "", nil, nil, unreadable(fmt.Errorf("open process %d: %v", pid, err))
	}
	defer syscall.CloseHandle(handle)
	var directory string
	var arguments, environment []string
	err = walkSteady(handle, pid, func() (uintptr, error) {
		directory, arguments, environment = "", nil, nil
		parameters, err := parameterBlock(handle, pid)
		if err != nil {
			return 0, unreadable(err)
		}
		// Do not prefetch into the next page, which could have a guard on it.
		pageSize := uintptr(os.Getpagesize())
		snapshot := make([]byte, max(pageSize-parameters%pageSize, paramsOffsetCommandLine+16))
		if err := readMemory(handle, parameters, snapshot); err != nil {
			return parameters, unreadable(err)
		}
		var directoryErr error
		directory, directoryErr = unicodeString(handle, pid, snapshot[paramsOffsetCurrentDirectory:], parameters, snapshot)
		if directoryErr != nil {
			directoryErr = fmt.Errorf("%w: %v", ErrDirectoryUnreadable, directoryErr)
		}
		line, argumentsErr := unicodeString(handle, pid, snapshot[paramsOffsetCommandLine:], parameters, snapshot)
		if argumentsErr != nil {
			argumentsErr = fmt.Errorf("%w: %v", ErrCommandLineUnreadable, argumentsErr)
		} else {
			arguments, argumentsErr = splitCommandLine(line)
		}
		if withEnvironment {
			environment = environmentOf(handle, parameters, snapshot)
		}
		return parameters, errors.Join(directoryErr, argumentsErr)
	})
	if errors.Is(err, errKeptMoving) {
		return "", nil, nil, unreadable(err)
	}
	return directory, arguments, environment, err
}

// environmentOf reads the environment of the process behind handle, whose
// parameter block at parameters begins with snapshot: in one read of the
// size the block records, or page by page where it records none or that
// read is refused. It is nil when it cannot be read.
func environmentOf(handle syscall.Handle, parameters uintptr, snapshot []byte) []string {
	field := func(offset uintptr) uintptr {
		if int(offset)+8 <= len(snapshot) {
			return uintptr(*(*uint64)(unsafe.Pointer(&snapshot[offset])))
		}
		value, err := readPointer(handle, parameters+offset)
		if err != nil {
			return 0
		}
		return value
	}
	address := field(unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.Environment))
	if address == 0 {
		return nil
	}
	if size := field(unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.EnvironmentSize)); size >= 2 && size <= maxEnvironmentBytes {
		block := make([]byte, size-size%2)
		if err := readMemory(handle, address, block); err == nil {
			// The block ends with an empty entry. One the recorded size cut
			// short does not, and is read again page by page below.
			var values []string
			units := decodeUTF16(block)
			for start := 0; start < len(units); {
				end := start
				for end < len(units) && units[end] != 0 {
					end++
				}
				if end == len(units) {
					break
				}
				if end == start {
					return values
				}
				values = append(values, syscall.UTF16ToString(units[start:end]))
				start = end + 1
			}
		}
	}
	values, err := environmentAt(handle, address)
	if err != nil {
		return nil
	}
	return values
}

// splitCommandLine splits a command line by Windows' own CommandLineToArgvW.
func splitCommandLine(line string) ([]string, error) {
	pointer, err := syscall.UTF16PtrFromString(line)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
	}
	var count int32
	argv, err := syscall.CommandLineToArgv(pointer, &count)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCommandLineUnreadable, err)
	}
	defer syscall.LocalFree(syscall.Handle(uintptr(unsafe.Pointer(argv))))
	args := make([]string, count)
	for index := range args {
		args[index] = syscall.UTF16ToString(argv[index][:])
	}
	return args, nil
}

// parameterString reads the UNICODE_STRING at offset in pid's process
// parameter block.
func parameterString(pid int, offset uintptr) (string, error) {
	handle, err := syscall.OpenProcess(processQueryInformation|processVMRead, false, uint32(pid))
	if err != nil {
		return "", fmt.Errorf("open process %d: %v", pid, err)
	}
	defer syscall.CloseHandle(handle)

	var value string
	err = walkSteady(handle, pid, func() (uintptr, error) {
		parameters, err := parameterBlock(handle, pid)
		if err != nil {
			return 0, err
		}
		descriptor := make([]byte, 16)
		if err := readMemory(handle, parameters+offset, descriptor); err != nil {
			return parameters, err
		}
		value, err = unicodeString(handle, pid, descriptor, 0, nil)
		return parameters, err
	})
	if err != nil {
		return "", err
	}
	return value, nil
}

// walkSteady runs walk, a read of pid's memory that returns the parameter
// block it walked (zero when it found none), until the PEB still points at
// that block once the walk is done.
func walkSteady(handle syscall.Handle, pid int, walk func() (uintptr, error)) error {
	for range maxWalks {
		parameters, err := walk()
		if parameters == 0 || stillAt(handle, pid, parameters) {
			return err
		}
	}
	return fmt.Errorf("process %d: %w", pid, errKeptMoving)
}

// stillAt reports whether pid's PEB points at the parameter block at
// parameters. A PEB that could not be read proves nothing, so it does not.
func stillAt(handle syscall.Handle, pid int, parameters uintptr) bool {
	current, err := parameterBlock(handle, pid)
	return err == nil && current == parameters
}

// parameterBlock returns the address of pid's process parameter block.
func parameterBlock(handle syscall.Handle, pid int) (uintptr, error) {
	peb, err := processEnvironmentBlock(handle)
	if err != nil {
		return 0, err
	}
	parameters, err := readPointer(handle, peb+pebOffsetProcessParameters)
	if err != nil {
		return 0, err
	}
	if parameters == 0 {
		return 0, fmt.Errorf("process %d has no parameter block", pid)
	}
	return parameters, nil
}

// unicodeString reads the value a 16-byte UNICODE_STRING descriptor, copied
// out of pid's parameter block, points to.
func unicodeString(handle syscall.Handle, pid int, descriptor []byte, snapshotBase uintptr, snapshot []byte) (string, error) {
	length := int(*(*uint16)(unsafe.Pointer(&descriptor[0])))
	buffer := uintptr(*(*uint64)(unsafe.Pointer(&descriptor[unicodeStringBufferOffset])))
	if length == 0 || buffer == 0 {
		return "", fmt.Errorf("process %d reports an empty value", pid)
	}
	if length%2 != 0 || length > maxParameterBytes {
		return "", fmt.Errorf("process %d reports a %d byte value", pid, length)
	}

	var raw []byte
	if length <= len(snapshot) && buffer >= snapshotBase && buffer-snapshotBase <= uintptr(len(snapshot)-length) {
		offset := buffer - snapshotBase
		raw = snapshot[offset : offset+uintptr(length)]
	} else {
		raw = make([]byte, length)
		if err := readMemory(handle, buffer, raw); err != nil {
			return "", err
		}
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
		return 0, fmt.Errorf("NtQueryInformationProcess returned 0x%x", status)
	}
	peb := uintptr(*(*uint64)(unsafe.Pointer(&information[8])))
	if peb == 0 {
		return 0, errors.New("process reports no environment block")
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
		return fmt.Errorf("read %d bytes at 0x%x: %w", len(buffer), address, err)
	}
	if int(read) != len(buffer) {
		return fmt.Errorf("read %d of %d bytes at 0x%x", read, len(buffer), address)
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
