package proc

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"syscall"
	"unsafe"
)

// Access rights, information classes and layouts used to read the job
// objects another process holds open.
const (
	processDupHandle            = 0x0040
	jobObjectQuery              = 0x0004
	systemExtendedHandleInfo    = 64
	jobObjectBasicProcessIDList = 3
	statusInfoLengthMismatch    = 0xC0000004
	errorMoreData               = 234
	// handleTableHeader is SYSTEM_HANDLE_INFORMATION_EX's two pointers, and
	// handleEntrySize its SYSTEM_HANDLE_TABLE_ENTRY_INFO_EX on 64-bit Windows:
	// UniqueProcessId at +8, HandleValue at +16 and ObjectTypeIndex at +30.
	handleTableHeader = 16
	handleEntrySize   = 40
	// maxHandleTable bounds the copy of the system handle table.
	maxHandleTable = 256 << 20
)

var (
	ntQuerySystemInformation      = ntdll.NewProc("NtQuerySystemInformation")
	procCreateJobObjectW          = kernel32.NewProc("CreateJobObjectW")
	procQueryInformationJobObject = kernel32.NewProc("QueryInformationJobObject")
)

// JobProcesses returns the live processes in the job objects holderPID holds
// open. Windows PowerShell's Start-Process -Wait puts the process it starts
// in a job and waits until the job is empty, and every descendant joins that
// job, even one whose parent has exited, so these are exactly the processes a
// shell blocked in that wait is waiting on. A job holderPID itself runs in is
// not one it waits on, so it is skipped. Entries are sorted by pid.
func JobProcesses(holderPID int) ([]Entry, error) {
	holder, err := syscall.OpenProcess(processDupHandle, false, uint32(holderPID))
	if err != nil {
		return nil, fmt.Errorf("proc: open process %d: %w", holderPID, err)
	}
	defer syscall.CloseHandle(holder)

	// Object type numbers differ between boots, so a job of this process's
	// own shows which type in the handle table is a job.
	own, _, err := procCreateJobObjectW.Call(0, 0)
	if own == 0 {
		return nil, fmt.Errorf("proc: create a job object: %w", err)
	}
	defer syscall.CloseHandle(syscall.Handle(own))
	table, err := handleTable()
	if err != nil {
		return nil, err
	}
	jobType, found := uint16(0), false
	for _, entry := range table {
		if entry.process == uintptr(os.Getpid()) && entry.handle == own {
			jobType, found = entry.objectType, true
		}
	}
	if !found {
		return nil, errors.New("proc: the system handle table does not list this process's own job")
	}

	processes, err := snapshotProcesses()
	if err != nil {
		return nil, err
	}
	current, err := syscall.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	seen := map[uint32]bool{}
	var jobbed []Entry
	for _, entry := range table {
		if entry.process != uintptr(holderPID) || entry.objectType != jobType {
			continue
		}
		var job syscall.Handle
		if err := syscall.DuplicateHandle(holder, syscall.Handle(entry.handle), current, &job, jobObjectQuery, false, 0); err != nil {
			return nil, fmt.Errorf("proc: read a job process %d holds: %w", holderPID, err)
		}
		ids, err := jobProcessIDs(job)
		syscall.CloseHandle(job)
		if err != nil {
			return nil, err
		}
		if slices.Contains(ids, uint32(holderPID)) {
			continue
		}
		for _, id := range ids {
			start, alive := processStart(int(id))
			if seen[id] || !alive {
				continue
			}
			seen[id] = true
			jobbed = append(jobbed, Entry{PID: int(id), ParentPID: int(processes[id].parentPID), ExeBase: processes[id].exeBase, Start: start})
		}
	}
	sort.Slice(jobbed, func(i, j int) bool { return jobbed[i].PID < jobbed[j].PID })
	return jobbed, nil
}

// handleEntry is one open handle on the system.
type handleEntry struct {
	process    uintptr
	handle     uintptr
	objectType uint16
}

// handleTable reads every handle open on the system.
func handleTable() ([]handleEntry, error) {
	for size := 1 << 20; size <= maxHandleTable; size *= 2 {
		buffer := make([]byte, size)
		var returned uint32
		status, _, _ := ntQuerySystemInformation.Call(systemExtendedHandleInfo, uintptr(unsafe.Pointer(&buffer[0])), uintptr(size), uintptr(unsafe.Pointer(&returned)))
		if uint32(status) == statusInfoLengthMismatch {
			continue
		}
		if status != 0 {
			return nil, fmt.Errorf("proc: NtQuerySystemInformation returned 0x%x", status)
		}
		count := int(*(*uintptr)(unsafe.Pointer(&buffer[0])))
		if count < 0 || handleTableHeader+count*handleEntrySize > len(buffer) {
			return nil, errors.New("proc: the system handle table is truncated")
		}
		entries := make([]handleEntry, count)
		for index := range entries {
			at := handleTableHeader + index*handleEntrySize
			entries[index] = handleEntry{
				process:    *(*uintptr)(unsafe.Pointer(&buffer[at+8])),
				handle:     *(*uintptr)(unsafe.Pointer(&buffer[at+16])),
				objectType: *(*uint16)(unsafe.Pointer(&buffer[at+30])),
			}
		}
		return entries, nil
	}
	return nil, errors.New("proc: the system handle table exceeds its bound")
}

// jobProcessIDs lists the processes in a job, from
// JOBOBJECT_BASIC_PROCESS_ID_LIST: two counts, then the ids as pointers.
func jobProcessIDs(job syscall.Handle) ([]uint32, error) {
	for size := 4096; size <= 1<<20; size *= 4 {
		buffer := make([]byte, size)
		ok, _, err := procQueryInformationJobObject.Call(uintptr(job), jobObjectBasicProcessIDList, uintptr(unsafe.Pointer(&buffer[0])), uintptr(size), 0)
		if ok == 0 {
			if errors.Is(err, syscall.Errno(errorMoreData)) {
				continue
			}
			return nil, fmt.Errorf("proc: QueryInformationJobObject: %w", err)
		}
		count := int(*(*uint32)(unsafe.Pointer(&buffer[4])))
		if 8+count*8 > len(buffer) {
			return nil, errors.New("proc: the job's process list is truncated")
		}
		ids := make([]uint32, count)
		for index := range ids {
			ids[index] = uint32(*(*uintptr)(unsafe.Pointer(&buffer[8+index*8])))
		}
		return ids, nil
	}
	return nil, errors.New("proc: the job's process list exceeds its bound")
}
