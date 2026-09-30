package supervisor

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	globalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	getPerformanceInfo   = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetPerformanceInfo")
)

// memoryStatusEx is Windows' MEMORYSTATUSEX.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// performanceInformation is Windows' PERFORMANCE_INFORMATION; its sizes are
// in pages of pageSize bytes.
type performanceInformation struct {
	size              uint32
	commitTotal       uintptr
	commitLimit       uintptr
	commitPeak        uintptr
	physicalTotal     uintptr
	physicalAvailable uintptr
	systemCache       uintptr
	kernelTotal       uintptr
	kernelPaged       uintptr
	kernelNonpaged    uintptr
	pageSize          uintptr
	handleCount       uint32
	processCount      uint32
	threadCount       uint32
}

// MachineMemory reads the machine's memory. Available counts the standby
// list, memory a new process can have for the asking, as the fleet's memory
// rule does. Commit is what every process's private memory is charged
// against, RAM plus page file, and a new process fails to start once it runs
// out even while physical memory looks fine. The kernel's paged and nonpaged
// pools are resident too, and a paged pool that keeps growing is a driver
// leaking memory.
func MachineMemory() (Memory, error) {
	status := memoryStatusEx{}
	status.length = uint32(unsafe.Sizeof(status))
	if ok, _, callErr := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status))); ok == 0 {
		return Memory{}, callErr
	}
	performance := performanceInformation{}
	performance.size = uint32(unsafe.Sizeof(performance))
	if ok, _, callErr := getPerformanceInfo.Call(uintptr(unsafe.Pointer(&performance)), uintptr(performance.size)); ok == 0 {
		return Memory{}, callErr
	}
	return Memory{
		Available:       status.availPhys,
		Total:           status.totalPhys,
		CommitAvailable: status.availPageFile,
		CommitLimit:     status.totalPageFile,
		PagedPool:       uint64(performance.kernelPaged) * uint64(performance.pageSize),
		NonpagedPool:    uint64(performance.kernelNonpaged) * uint64(performance.pageSize),
	}, nil
}

// CommitHolders reads every process's commit, its private memory, from one
// system process list, which needs no handle to any process, and names the
// apps that hold the most (see topCommitHolders).
func CommitHolders() ([]CommitHolder, error) {
	buffer := make([]byte, 1<<20)
	for {
		var needed uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buffer[0]), uint32(len(buffer)), &needed)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			return nil, err
		}
		// The list grows between calls as processes start.
		buffer = make([]byte, max(int(needed), 2*len(buffer)))
	}
	var processes []processCommit
	for offset := uint32(0); ; {
		entry := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buffer[offset]))
		processes = append(processes, processCommit{
			pid:     uint32(entry.UniqueProcessID),
			parent:  uint32(entry.InheritedFromUniqueProcessID),
			name:    entry.ImageName.String(),
			created: entry.CreateTime,
			commit:  uint64(entry.PagefileUsage),
		})
		if entry.NextEntryOffset == 0 {
			break
		}
		offset += entry.NextEntryOffset
	}
	return topCommitHolders(processes, commitHolderCount), nil
}
