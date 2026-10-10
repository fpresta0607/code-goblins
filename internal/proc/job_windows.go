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
	jobObjectTerminate          = 0x0008
	systemExtendedHandleInfo    = 64
	jobObjectBasicProcessIDList = 3
	statusInfoLengthMismatch    = 0xC0000004
	errorMoreData               = 234
	// stillActive is the exit status of a process that has none yet.
	stillActive = 259
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
	held, err := holdJobs(holderPID, jobObjectQuery)
	if err != nil {
		return nil, err
	}
	defer held.Close()
	return held.Processes()
}

// HeldJobs are the job objects a process held open, held open by this
// process too. A job lives while a handle to it is open, so its processes
// can still be listed and ended through these once the process that made it
// has ended, when nothing can reach the job through that process any more.
// While they are held, the end of that process is no longer the close of a
// job's last handle, so a job that ends its processes on that close ends
// them at Close instead.
type HeldJobs struct {
	jobs []syscall.Handle
}

// HoldJobs holds every job object holderPID holds open, but a job holderPID
// itself runs in. The caller closes them.
func HoldJobs(holderPID int) (*HeldJobs, error) {
	return holdJobs(holderPID, jobObjectQuery|jobObjectSetAttributes|jobObjectTerminate)
}

func holdJobs(holderPID int, access uint32) (*HeldJobs, error) {
	jobs, err := heldJobs(holderPID, access)
	if err != nil {
		return nil, err
	}
	held := &HeldJobs{}
	for index, job := range jobs {
		ids, err := jobProcessIDs(job)
		if err != nil {
			held.Close()
			closeAll(jobs[index:])
			return nil, err
		}
		if slices.Contains(ids, uint32(holderPID)) {
			syscall.CloseHandle(job)
			continue
		}
		held.jobs = append(held.jobs, job)
	}
	return held, nil
}

// Processes returns the live processes in the held jobs, sorted by pid.
func (h *HeldJobs) Processes() ([]Entry, error) {
	processes, err := snapshotProcesses()
	if err != nil {
		return nil, err
	}
	seen := map[uint32]bool{}
	var jobbed []Entry
	for _, job := range h.jobs {
		ids, err := jobProcessIDs(job)
		if err != nil {
			return nil, err
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

// KeepOnClose stops the held jobs from ending their processes when their
// last handle closes, so a machine service left in one outlives it.
func (h *HeldJobs) KeepOnClose() error {
	for _, job := range h.jobs {
		if err := KeepJobOnClose(windows.Handle(job)); err != nil {
			return fmt.Errorf("proc: a held job: %w", err)
		}
	}
	return nil
}

// End ends every process in the held jobs at once.
func (h *HeldJobs) End() error {
	var failures error
	for _, job := range h.jobs {
		if err := windows.TerminateJobObject(windows.Handle(job), 1); err != nil {
			failures = errors.Join(failures, fmt.Errorf("proc: end a held job: %w", err))
		}
	}
	return failures
}

// Close lets go of the held jobs.
func (h *HeldJobs) Close() {
	closeAll(h.jobs)
	h.jobs = nil
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

// endingWait is how long a holder that refused a handle is given to show an
// exit status. A holder that is ending and one Windows protects refuse a
// handle in the same way, and only the first gets a status.
const endingWait = 2 * time.Second

// heldJobs duplicates, with access, every job object holderPID holds open.
// The caller closes them.
func heldJobs(holderPID int, access uint32) ([]syscall.Handle, error) {
	return heldJobsBy(holderPID, access, syscall.DuplicateHandle)
}

// heldJobsBy is heldJobs copying each handle with duplicate. A holder that is
// ending holds none: Windows lists the handles it has yet to close and
// refuses each, and one that ends after the handle table was read is still
// in it, so a refusal is no error once the holder has an exit status.
func heldJobsBy(holderPID int, access uint32, duplicate func(source, handle, target syscall.Handle, copied *syscall.Handle, access uint32, inherit bool, options uint32) error) ([]syscall.Handle, error) {
	holder, err := syscall.OpenProcess(processDupHandle|processQueryLimitedInformation, false, uint32(holderPID))
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
		if err := duplicate(holder, syscall.Handle(entry.handle), current, &job, access, false, 0); err != nil {
			closeAll(jobs)
			if isEnding(holder) {
				return nil, nil
			}
			return nil, fmt.Errorf("proc: read a job process %d holds: %w", holderPID, err)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// isEnding reports whether the process behind handle has an exit status
// within endingWait. A process being ended refuses its handles a moment
// before it has one.
func isEnding(process syscall.Handle) bool {
	for deadline := time.Now().Add(endingWait); ; time.Sleep(10 * time.Millisecond) {
		var code uint32
		if err := syscall.GetExitCodeProcess(process, &code); err != nil {
			return false
		}
		if code != stillActive {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
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
