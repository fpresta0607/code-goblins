package proc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/awake"
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
		ids, _, err := JobProcessIDs(job)
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
	return jobEntries(h.jobs)
}

// jobEntries returns the live processes in jobs, sorted by pid: those the
// jobs list, and those Windows still holds in one of them but lists no
// more, as it lists no process that a job nested in one of them is ending
// (JobProcessIDs). Only the machine's processes name those, so each that is
// in one of the jobs and has not ended is one of them. The jobs are read
// before the machine's processes, so a process that joined one just before
// has its name.
func jobEntries(jobs []syscall.Handle) ([]Entry, error) {
	held := make([][]uint32, len(jobs))
	listed := map[uint32]bool{}
	for index, job := range jobs {
		ids, _, err := JobProcessIDs(job)
		if err != nil {
			return nil, err
		}
		held[index] = ids
		for _, id := range ids {
			listed[id] = true
		}
	}
	processes, err := snapshotProcesses()
	if err != nil {
		return nil, err
	}
	seen := map[uint32]bool{}
	var jobbed []Entry
	for index, job := range jobs {
		for _, id := range held[index] {
			start, alive := jobProcessStart(job, id)
			if seen[id] || !alive {
				continue
			}
			seen[id] = true
			jobbed = append(jobbed, Entry{PID: int(id), ParentPID: int(processes[id].parentPID), ExeBase: processes[id].exeBase, Start: start})
		}
	}
	for id, process := range processes {
		if listed[id] {
			continue
		}
		if start, isMember := unlistedProcessStart(jobs, id); isMember {
			jobbed = append(jobbed, Entry{PID: int(id), ParentPID: int(process.parentPID), ExeBase: process.exeBase, Start: start})
		}
	}
	sort.Slice(jobbed, func(i, j int) bool { return jobbed[i].PID < jobbed[j].PID })
	return jobbed, nil
}

// unlistedProcessStart reports when process id started if it is in one of
// jobs and has not ended. Like jobProcessStart it reads membership and birth
// through one handle, so a reused pid never names another process.
func unlistedProcessStart(jobs []syscall.Handle, id uint32) (time.Time, bool) {
	process, err := syscall.OpenProcess(processQueryLimitedInformation|syscall.SYNCHRONIZE, false, id)
	if err != nil {
		return time.Time{}, false
	}
	defer syscall.CloseHandle(process)
	if event, err := syscall.WaitForSingleObject(process, 0); err != nil || event != syscall.WAIT_TIMEOUT {
		return time.Time{}, false
	}
	for _, job := range jobs {
		var isMember int32
		if ok, _, _ := procIsProcessInJob.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&isMember))); ok == 0 || isMember == 0 {
			continue
		}
		var creation, exit, kernel, user syscall.Filetime
		if err := syscall.GetProcessTimes(process, &creation, &exit, &kernel, &user); err != nil {
			return time.Time{}, false
		}
		return time.Unix(0, creation.Nanoseconds()).UTC(), true
	}
	return time.Time{}, false
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

// endJobFor bounds how long EndJob keeps ending a job's processes one by one
// while a machine service is spared.
const endJobFor = 5 * time.Second

// EndJob ends every process in job, as TerminateJobObject does, except a
// machine service started in it and what runs under it there
// (RunningServicesAmong): Docker Desktop or the no-mistakes daemon a goblin
// started serves the whole machine, and the end of what started it must not
// end it. With one there, the rest are ended one by one until none is left,
// one Windows no longer lists included (jobEntries), within endJobFor or
// ctx's deadline. It returns the processes the job held as it ended them,
// and the services it spared with what runs under them, each sorted by pid,
// and an end that fails beside a service still returns the services. A job
// that ends its processes when its last handle closes still does: its
// caller stops that (KeepJobOnClose) once the service is to outlive the
// job, so a caller that dies first still ends the rest.
func EndJob(ctx context.Context, job windows.Handle) (ended, spared []Entry, err error) {
	members, err := jobEntries([]syscall.Handle{syscall.Handle(job)})
	if err != nil {
		return nil, nil, errors.Join(err, windows.TerminateJobObject(job, 1))
	}
	services, err := RunningServicesAmong(members)
	if err != nil {
		return members, nil, errors.Join(err, windows.TerminateJobObject(job, 1))
	}
	if !slices.ContainsFunc(members, func(member Entry) bool { return services[member.PID] != NoService }) {
		if err := windows.TerminateJobObject(job, 1); err != nil {
			return nil, nil, fmt.Errorf("proc: end a job: %w", err)
		}
		return members, nil, nil
	}
	ended, spared, err = endEachBut(ctx, job, members, services)
	sort.Slice(ended, func(i, j int) bool { return ended[i].PID < ended[j].PID })
	return ended, spared, err
}

// endEachBut ends job's processes one by one, members as they were last
// read, but the machine services and what runs under them, until none else
// is left, within endJobFor or ctx's deadline, and returns the processes it
// ended and the services it spared. A failure returns the services last
// seen with it.
func endEachBut(ctx context.Context, job windows.Handle, members []Entry, services map[int]Service) (ended, spared []Entry, err error) {
	ctx, cancel := awake.WithTimeout(ctx, endJobFor)
	defer cancel()
	seen := servicesIn(members, services)
	for {
		var rest []int
		for _, member := range members {
			if services[member.PID] != NoService {
				continue
			}
			if endJobMember(job, member) && !slices.Contains(ended, member) {
				ended = append(ended, member)
			}
			rest = append(rest, member.PID)
		}
		if len(rest) == 0 {
			return ended, members, nil
		}
		select {
		case <-ctx.Done():
			return ended, seen, fmt.Errorf("proc: the job's processes %v outlived it beside the machine services it keeps running: %w", rest, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
		if members, err = jobEntries([]syscall.Handle{syscall.Handle(job)}); err != nil {
			return ended, seen, err
		}
		if services, err = RunningServicesAmong(members); err != nil {
			return ended, seen, err
		}
		seen = servicesIn(members, services)
	}
}

// servicesIn are the members services names as a machine service or as
// running under one.
func servicesIn(members []Entry, services map[int]Service) []Entry {
	var kept []Entry
	for _, member := range members {
		if services[member.PID] != NoService {
			kept = append(kept, member)
		}
	}
	return kept
}

// endJobMember ends member if it is still in job and still the process that
// started when member did, so a reused ID never ends another process, and
// reports whether it ended it.
func endJobMember(job windows.Handle, member Entry) bool {
	process, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(member.PID))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(process)
	var isMember int32
	if ok, _, _ := procIsProcessInJob.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&isMember))); ok == 0 || isMember == 0 {
		return false
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &creation, &exit, &kernel, &user); err != nil || !time.Unix(0, creation.Nanoseconds()).UTC().Equal(member.Start) {
		return false
	}
	return windows.TerminateProcess(process, 1) == nil
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
	for size := 1 << 20; size <= maxHandleTable; {
		buffer := make([]byte, size)
		var returned uint32
		status, _, _ := ntQuerySystemInformation.Call(systemExtendedHandleInfo, uintptr(unsafe.Pointer(&buffer[0])), uintptr(size), uintptr(unsafe.Pointer(&returned)))
		if uint32(status) == statusInfoLengthMismatch {
			// Windows says how much the table needs. Asking for that and
			// an eighth more, for the handles opened meanwhile, reads it in
			// two calls, where doubling from the first size made and
			// cleared seven buffers on a machine with a million handles.
			size = max(size*2, int(returned)+int(returned)/8)
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

// readJobList fills buffer with job's JOBOBJECT_BASIC_PROCESS_ID_LIST: two
// counts, then the ids as pointers. A test stands in for it.
var readJobList = func(job syscall.Handle, buffer []uintptr) error {
	if ok, _, err := procQueryInformationJobObject.Call(uintptr(job), jobObjectBasicProcessIDList, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer))*unsafe.Sizeof(uintptr(0)), 0); ok == 0 {
		return err
	}
	return nil
}

// JobProcessIDs lists the processes in a job, and says how many more the job
// counts than it lists. Windows counts a job's processes and lists them in
// one call. A process that a job nested in this one ends leaves the list as
// that end begins, at times before it has an exit status, and the count
// either with it or some time after, as its threads end, which no reader
// bounds. A headless Chrome puts most of its processes each in a job of its
// own inside the one the browser runs in, which ends what it holds once the
// browser has ended: such a process left the list with up to 21 threads not
// yet ended and was done up to 2.5 s later, while the job counted it beyond
// the list for up to 474 ms or not at all (issues 1025 and 1066). A process
// that exits, ends itself, is ended by its handle or ends with this job
// stays listed until it has ended. A sample takes the list as it is, and a
// caller that must show the job holds nothing else, as an end that spares a
// machine service does, also reads the machine's processes for those the
// list leaves out (jobEntries).
func JobProcessIDs(job syscall.Handle) (ids []uint32, unlisted uint32, err error) {
	// Two DWORD counts precede pointer-sized ids. The room grows only when
	// the list does not fit, within a bound, so a changing job cannot turn
	// one read into an unbounded allocation.
	const headerWords = int(8 / unsafe.Sizeof(uintptr(0)))
	capacity := 64
	buffer := make([]uintptr, headerWords+capacity)
	for {
		err := readJobList(job, buffer)
		counts := (*[2]uint32)(unsafe.Pointer(&buffer[0]))
		switch {
		case errors.Is(err, syscall.Errno(errorMoreData)) || (err == nil && counts[0] > uint32(capacity)):
			if capacity *= 4; capacity > 65536 {
				return nil, 0, errors.New("proc: the job's process list exceeds its bound")
			}
			buffer = make([]uintptr, headerWords+capacity)
			continue
		case err != nil:
			return nil, 0, fmt.Errorf("proc: QueryInformationJobObject: %w", err)
		case counts[1] > counts[0]:
			return nil, 0, fmt.Errorf("proc: the job lists %d processes but counts %d", counts[1], counts[0])
		}
		ids = make([]uint32, counts[1])
		for index, id := range buffer[headerWords : headerWords+int(counts[1])] {
			ids[index] = uint32(id)
		}
		return ids, counts[0] - counts[1], nil
	}
}
