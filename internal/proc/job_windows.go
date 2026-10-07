package proc

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Access rights, information classes and layouts used to read the job
// objects another process holds open.
const (
	processDupHandle            = 0x0040
	jobObjectQuery              = 0x0004
	jobObjectSetAttributes      = 0x0002
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
	procIsProcessInJob            = kernel32.NewProc("IsProcessInJob")
)

// JobProcesses returns the live processes in the job objects holderPID holds
// open. Windows PowerShell's Start-Process -Wait puts the process it starts
// in a job and waits until the job is empty, and every descendant joins that
// job, even one whose parent has exited, so these are exactly the processes a
// shell blocked in that wait is waiting on. A job holderPID itself runs in is
// not one it waits on, so it is skipped. Entries are sorted by pid.
func JobProcesses(holderPID int) ([]Entry, error) {
	jobs, err := heldJobs(holderPID, jobObjectQuery)
	if err != nil {
		return nil, err
	}
	defer closeAll(jobs)
	processes, err := snapshotProcesses()
	if err != nil {
		return nil, err
	}
	seen := map[uint32]bool{}
	var jobbed []Entry
	for _, job := range jobs {
		ids, err := jobProcessIDs(job)
		if err != nil {
			return nil, err
		}
		if slices.Contains(ids, uint32(holderPID)) {
			continue
		}
		for _, id := range ids {
			start, alive := jobProcessStart(job, id)
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

// KeepJobsOnClose stops the jobs holderPID holds open from ending their
// processes when their last handle closes, so a machine service left in one
// outlives holderPID. A job holderPID itself runs in is left as it is.
func KeepJobsOnClose(holderPID int) error {
	jobs, err := heldJobs(holderPID, jobObjectQuery|jobObjectSetAttributes)
	if err != nil {
		return err
	}
	defer closeAll(jobs)
	for _, job := range jobs {
		ids, err := jobProcessIDs(job)
		if err != nil {
			return err
		}
		if slices.Contains(ids, uint32(holderPID)) {
			continue
		}
		if err := KeepJobOnClose(windows.Handle(job)); err != nil {
			return fmt.Errorf("proc: a job process %d holds: %w", holderPID, err)
		}
	}
	return nil
}

// KeepJobOnClose stops job from ending its processes when its last handle
// closes, keeping its other limits.
func KeepJobOnClose(job windows.Handle) error {
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
		return fmt.Errorf("read the job's limits: %w", err)
	}
	if limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		return nil
	}
	limits.BasicLimitInformation.LimitFlags &^= windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return fmt.Errorf("keep the job's processes when it closes: %w", err)
	}
	return nil
}

// JobMembers lists the processes in job.
func JobMembers(job windows.Handle) ([]int, error) {
	ids, err := jobProcessIDs(syscall.Handle(job))
	if err != nil {
		return nil, err
	}
	members := make([]int, len(ids))
	for index, id := range ids {
		members[index] = int(id)
	}
	return members, nil
}

// EndJobMember ends process pid if it is still in job, so a reused ID never
// ends another process.
func EndJobMember(job windows.Handle, pid int) {
	process, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(process)
	var isMember int32
	if ok, _, _ := procIsProcessInJob.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&isMember))); ok != 0 && isMember != 0 {
		_ = windows.TerminateProcess(process, 1)
	}
}

// heldJobs duplicates, with access, every job object holderPID holds open.
// The caller closes them.
func heldJobs(holderPID int, access uint32) ([]syscall.Handle, error) {
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
	current, err := syscall.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	var jobs []syscall.Handle
	for _, entry := range table {
		if entry.process != uintptr(holderPID) || entry.objectType != jobType {
			continue
		}
		var job syscall.Handle
		if err := syscall.DuplicateHandle(holder, syscall.Handle(entry.handle), current, &job, access, false, 0); err != nil {
			closeAll(jobs)
			return nil, fmt.Errorf("proc: read a job process %d holds: %w", holderPID, err)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func closeAll(handles []syscall.Handle) {
	for _, handle := range handles {
		syscall.CloseHandle(handle)
	}
}

func jobProcessStart(job syscall.Handle, id uint32) (time.Time, bool) {
	process, err := syscall.OpenProcess(processQueryLimitedInformation, false, id)
	if err != nil {
		return time.Time{}, false
	}
	defer syscall.CloseHandle(process)
	// Keep one process handle for membership and birth, so PID reuse between
	// the job snapshot and this read cannot authorize an unrelated process.
	var isMember int32
	ok, _, _ := procIsProcessInJob.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&isMember)))
	if ok == 0 || isMember == 0 {
		return time.Time{}, false
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(process, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, creation.Nanoseconds()).UTC(), true
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
