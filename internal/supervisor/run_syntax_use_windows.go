package supervisor

import (
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shellUseKernel           = windows.NewLazySystemDLL("kernel32.dll")
	getShellIoCounters       = shellUseKernel.NewProc("GetProcessIoCounters")
	getShellMemoryInfo       = shellUseKernel.NewProc("K32GetProcessMemoryInfo")
	shellMemoryCountersBytes = uint32(unsafe.Sizeof(shellMemoryCounters{}))
)

// shellMemoryCounters is PROCESS_MEMORY_COUNTERS, of which only the page
// faults are read.
type shellMemoryCounters struct {
	Size, PageFaults               uint32
	PeakWorkingSet, WorkingSet     uintptr
	PeakPagedPool, PagedPool       uintptr
	PeakNonPagedPool, NonPagedPool uintptr
	Pagefile, PeakPagefile         uintptr
}

// shellUse says what the shell process did until now: the processor time it
// used, the pages it faulted in and the bytes it read.
func shellUse(process *os.Process) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(process.Pid))
	if err != nil {
		return fmt.Sprintf("its use could not be read: %v", err)
	}
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return fmt.Sprintf("its processor time could not be read: %v", err)
	}
	var io windows.IO_COUNTERS
	if ok, _, err := getShellIoCounters.Call(uintptr(handle), uintptr(unsafe.Pointer(&io))); ok == 0 {
		return fmt.Sprintf("its reads could not be read: %v", err)
	}
	memory := shellMemoryCounters{Size: shellMemoryCountersBytes}
	if ok, _, err := getShellMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&memory)), uintptr(memory.Size)); ok == 0 {
		return fmt.Sprintf("its page faults could not be read: %v", err)
	}
	// A process's times are counts of 100 ns, not times since an epoch.
	ticks := func(filetime windows.Filetime) int64 {
		return int64(filetime.HighDateTime)<<32 | int64(filetime.LowDateTime)
	}
	processor := time.Duration(ticks(kernel)+ticks(user)) * 100
	return fmt.Sprintf("in that time it used %s of processor time, %d page faults and %d bytes read in %d reads", processor.Round(time.Millisecond), memory.PageFaults, io.ReadTransferCount, io.ReadOperationCount)
}
