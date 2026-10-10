package proc

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procVirtualAllocEx = kernel32.NewProc("VirtualAllocEx")
	procVirtualFreeEx  = kernel32.NewProc("VirtualFreeEx")
)

// environmentReaders are the three ways the fleet reads another process's
// environment.
var environmentReaders = []struct {
	name string
	read func(pid int, start time.Time) ([]string, error)
}{
	{"Environment", func(pid int, _ time.Time) ([]string, error) { return Environment(pid) }},
	{"Identify", func(pid int, start time.Time) ([]string, error) {
		identity, err := Identify(pid, start)
		return identity.Environment, err
	}},
	{"ParametersAndEnvironment", func(pid int, _ time.Time) ([]string, error) {
		_, _, environment, err := ParametersAndEnvironment(pid)
		return environment, err
	}},
}

// A starting process moves its environment into its own heap before it moves
// its parameter block, and frees the one CreateProcess built, while its PEB
// still points at the same block. A reader that had the old address read
// memory as it was freed, and Identify answered "process environment
// unavailable" for a process that ran, as a train's CI did on 2026-10-10 (run
// 38017561464). Each reader reads the environment the block points to once
// the move is done.
func TestReadersReadAnEnvironmentThatMovesUnderASteadyBlock(t *testing.T) {
	for _, reader := range environmentReaders {
		t.Run(reader.name, func(t *testing.T) {
			// Arrange
			child := startSuspended(t)
			first := child.moveEnvironment(t, "CFO_ENVIRONMENT=first")
			if environment, err := reader.read(child.pid, child.start); err != nil || !slices.Contains(environment, "CFO_ENVIRONMENT=first") {
				t.Fatalf("the premise does not hold: with nothing moving the reader read %d entries and %v, want CFO_ENVIRONMENT=first", len(environment), err)
			}
			follows := 0
			environmentFollowed = func() {
				if follows++; follows == 1 {
					child.moveEnvironment(t, "CFO_ENVIRONMENT=second")
					child.free(t, first)
				}
			}
			t.Cleanup(func() { environmentFollowed = func() {} })

			// Act
			environment, err := reader.read(child.pid, child.start)

			// Assert
			if err != nil {
				t.Fatalf("the read across the move: %v", err)
			}
			if !slices.Contains(environment, "CFO_ENVIRONMENT=second") {
				t.Errorf("the read across the move gave %d entries without CFO_ENVIRONMENT=second", len(environment))
			}
			// The premise: the first read was of freed memory. Were it not,
			// a reader that never looked again would pass too.
			if err := readMemory(syscall.Handle(child.process), first, make([]byte, 2)); err == nil {
				t.Errorf("the premise does not hold: the environment the reader first followed, at 0x%x, can still be read", first)
			}
		})
	}
}

// An environment that has moved by the end of every read is never trusted:
// what was read may be what the process had before.
func TestReadersRefuseAnEnvironmentThatNeverHoldsStill(t *testing.T) {
	for _, reader := range environmentReaders {
		t.Run(reader.name, func(t *testing.T) {
			// Arrange
			child := startSuspended(t)
			child.moveEnvironment(t, "CFO_ENVIRONMENT=0")
			follows := 0
			environmentFollowed = func() {
				follows++
				child.moveEnvironment(t, "CFO_ENVIRONMENT="+strings.Repeat("moved ", follows))
			}
			t.Cleanup(func() { environmentFollowed = func() {} })

			// Act
			environment, err := reader.read(child.pid, child.start)

			// Assert
			if follows != maxWalks {
				t.Errorf("the reader followed the environment %d times, want %d", follows, maxWalks)
			}
			// ParametersAndEnvironment gives no environment where it cannot
			// read one, and the other two an error.
			if environment != nil || (err == nil && reader.name != "ParametersAndEnvironment") {
				t.Errorf("the reader gave %d entries and %v, want no environment from a process that never held still", len(environment), err)
			}
		})
	}
}

// suspended is a child that was created and never run, so nothing in it
// moves but what a test moves.
type suspended struct {
	pid        int
	start      time.Time
	process    windows.Handle
	parameters uintptr
}

func startSuspended(t *testing.T) suspended {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestParametersLongArgumentFixture$")
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	child := suspended{pid: command.Process.Pid}
	var ok bool
	if child.start, ok = StartTime(child.pid); !ok {
		t.Fatal("read the child's start time")
	}
	var err error
	child.process, err = windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_OPERATION|windows.PROCESS_VM_READ|windows.PROCESS_VM_WRITE, false, uint32(child.pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(child.process) })
	if child.parameters, err = parameterBlock(syscall.Handle(child.process), child.pid); err != nil {
		t.Fatal(err)
	}
	return child
}

// moveEnvironment gives the child a new environment of entries in memory of
// its own and points the child's parameter block at it, as a starting process
// does, and returns where it is.
func (child suspended) moveEnvironment(t *testing.T, entries ...string) uintptr {
	t.Helper()
	var units []uint16
	for _, entry := range entries {
		units = append(append(units, utf16.Encode([]rune(entry))...), 0)
	}
	units = append(units, 0)
	size := uintptr(len(units) * 2)
	region, _, err := procVirtualAllocEx.Call(uintptr(child.process), 0, size, windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if region == 0 {
		t.Fatalf("allocate the child's new environment: %v", err)
	}
	child.write(t, region, unsafe.Slice((*byte)(unsafe.Pointer(&units[0])), size))
	child.write(t, child.parameters+unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.EnvironmentSize), unsafe.Slice((*byte)(unsafe.Pointer(&size)), unsafe.Sizeof(size)))
	child.write(t, child.parameters+unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.Environment), unsafe.Slice((*byte)(unsafe.Pointer(&region)), unsafe.Sizeof(region)))
	return region
}

func (child suspended) write(t *testing.T, address uintptr, data []byte) {
	t.Helper()
	var written uintptr
	if err := windows.WriteProcessMemory(child.process, address, &data[0], uintptr(len(data)), &written); err != nil || written != uintptr(len(data)) {
		t.Fatalf("write %d bytes at 0x%x in the child: wrote %d, %v", len(data), address, written, err)
	}
}

// free releases the region of the child's memory at region, as a starting
// process frees the environment it has moved out of.
func (child suspended) free(t *testing.T, region uintptr) {
	t.Helper()
	if freed, _, err := procVirtualFreeEx.Call(uintptr(child.process), region, 0, windows.MEM_RELEASE); freed == 0 {
		t.Fatalf("free the child's environment at 0x%x: %v", region, err)
	}
}
