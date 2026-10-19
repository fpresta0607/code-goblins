package execx

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// openProcess opens a job member, and a test makes one this process may not
// open without the privilege that would let it open any.
var openProcess = windows.OpenProcess

// jobProcessIDs reads a job's members, and a test makes it count one more
// than it lists, as Windows does while a process exits.
var jobProcessIDs = proc.JobProcessIDs

var (
	usageKernel           = windows.NewLazySystemDLL("kernel32.dll")
	isProcessInUsageJob   = usageKernel.NewProc("IsProcessInJob")
	getProcessMemoryUsage = usageKernel.NewProc("K32GetProcessMemoryInfo")
)

type jobAccounting struct {
	UserTime, KernelTime, PeriodUserTime, PeriodKernelTime           int64
	PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
}

type jobMemoryUsage struct {
	Commit, PeakCommit uint64
}

// jobMemoryLimit bounds how long a job's memory is read again while each read
// cannot be so.
const jobMemoryLimit = 250 * time.Millisecond

// queryJobMemory reads the job's commitment now and its peak, and
// queryJobPeak the peak the job's limits keep, which Windows documents. A
// test replaces them to return what a query was seen to return.
var (
	queryJobMemory = func(job windows.Handle) (jobMemoryUsage, error) {
		var memory jobMemoryUsage
		const jobObjectMemoryUsageInformation = 28
		err := proc.QueryJobInformation(job, jobObjectMemoryUsageInformation, unsafe.Pointer(&memory), unsafe.Sizeof(memory))
		return memory, err
	}
	queryJobPeak = func(job windows.Handle) (uint64, error) {
		var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		err := proc.QueryJobInformation(job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), unsafe.Sizeof(limits))
		return uint64(limits.PeakJobMemoryUsed), err
	}
)

// PROCESS_MEMORY_COUNTERS_EX2, available on the supported Windows 11 desktop.
type processMemoryUsage struct {
	Size, PageFaults                                                      uint32
	PeakWorkingSet, WorkingSet                                            uintptr
	PeakPagedPool, PagedPool, PeakNonPagedPool, NonPagedPool              uintptr
	Pagefile, PeakPagefile, PrivateUsage, PrivateWorkingSet, SharedCommit uintptr
}

// Usage reads the job, not a parent walk, so workers whose parents exited
// remain accounted for. A failed read returns an error, never a zero sample.
func (t *Tree) Usage() (TreeUsage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	usage, err := t.jobUsage()
	if err != nil {
		return TreeUsage{}, err
	}
	usage.SampledPrivateWorkingSetBytes, usage.UnreadableMembers, err = t.privateWorkingSet()
	if err != nil {
		return TreeUsage{}, err
	}
	return usage, nil
}

// jobUsage reads the job's accounting and memory until the read can be so,
// for at most jobMemoryLimit, past which the job is unreadable. A read cannot
// be so with no commitment while a member runs, with no peak once a process
// ran, or with a peak under one read before, since a peak never falls: a
// read once returned nothing for both with a member running, as a buffer the
// stack moved away from reads (proc.QueryJobInformation).
func (t *Tree) jobUsage() (TreeUsage, error) {
	if t.isDone {
		return TreeUsage{}, errors.New("execx: job accounting is closed")
	}
	deadline := time.Now().Add(jobMemoryLimit)
	for {
		usage, why, err := t.readJob()
		if err != nil {
			return TreeUsage{}, err
		}
		if why == "" {
			t.peak = usage.PeakCommitBytes
			return usage, nil
		}
		if time.Now().After(deadline) {
			return TreeUsage{}, fmt.Errorf("execx: read job commitment: %s, through every read for %s", why, jobMemoryLimit)
		}
		time.Sleep(time.Millisecond)
	}
}

// readJob reads the job's accounting and memory once, and says why the read
// cannot be so, or nothing. The peak is the higher of the two Windows keeps.
func (t *Tree) readJob() (TreeUsage, string, error) {
	var accounting jobAccounting
	if err := proc.QueryJobInformation(t.job, windows.JobObjectBasicAccountingInformation, unsafe.Pointer(&accounting), unsafe.Sizeof(accounting)); err != nil {
		return TreeUsage{}, "", fmt.Errorf("execx: read job accounting: %w", err)
	}
	memory, err := queryJobMemory(t.job)
	if err != nil {
		return TreeUsage{}, "", fmt.Errorf("execx: read job commitment: %w", err)
	}
	peak, err := queryJobPeak(t.job)
	if err != nil {
		return TreeUsage{}, "", fmt.Errorf("execx: read job peak: %w", err)
	}
	usage := TreeUsage{
		CPU:         time.Duration(accounting.UserTime+accounting.KernelTime) * 100,
		CommitBytes: memory.Commit, PeakCommitBytes: max(memory.PeakCommit, peak),
		ActiveProcesses: accounting.ActiveProcesses, TotalProcesses: accounting.TotalProcesses,
	}
	switch {
	case usage.ActiveProcesses > 0 && usage.CommitBytes == 0:
		return usage, fmt.Sprintf("it read no commitment while %d processes run", usage.ActiveProcesses), nil
	case usage.TotalProcesses > 0 && usage.PeakCommitBytes == 0:
		return usage, fmt.Sprintf("it read no peak after %d processes ran", usage.TotalProcesses), nil
	case usage.PeakCommitBytes < t.peak:
		return usage, fmt.Sprintf("its peak read %d bytes, under the %d read before", usage.PeakCommitBytes, t.peak), nil
	}
	return usage, "", nil
}

// privateWorkingSet sums the private working set of the job's members, and
// counts the members this process may not read, which the sum leaves out.
func (t *Tree) privateWorkingSet() (total uint64, unreadable uint32, err error) {
	ids, _, err := t.memberIDs()
	if err != nil {
		return 0, 0, err
	}
	for _, pid := range ids {
		memory, err := t.processWorkingSet(pid)
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			unreadable++
			continue
		}
		if err != nil {
			return 0, 0, err
		}
		total += memory
	}
	return total, unreadable, nil
}

// memberIDs reads the ids of the processes the job lists now, and how many
// more it counts than it lists, as proc.JobProcessIDs reads them.
func (t *Tree) memberIDs() (ids []uint32, unlisted uint32, err error) {
	ids, unlisted, err = jobProcessIDs(syscall.Handle(t.job))
	if err != nil {
		return nil, 0, fmt.Errorf("execx: read job members: %w", err)
	}
	return ids, unlisted, nil
}

// Members names the processes still running in the job, by image name and
// pid, sorted by pid. A process that has exited is left out, though Windows
// may count it in the job for a moment after, and so is a reused pid that
// names a process outside the job.
func (t *Tree) Members() ([]EndedProcess, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.isDone {
		return nil, errors.New("execx: job accounting is closed")
	}
	ids, _, err := t.memberIDs()
	if err != nil {
		return nil, err
	}
	processes, err := proc.Processes()
	if err != nil {
		return nil, fmt.Errorf("execx: name job members: %w", err)
	}
	names := make(map[int]string, len(processes))
	for _, process := range processes {
		names[process.PID] = process.ExeBase
	}
	var members []EndedProcess
	for _, pid := range ids {
		isMember, err := t.holds(pid)
		if err != nil {
			return nil, err
		}
		if isMember {
			members = append(members, EndedProcess{Name: names[int(pid)], PID: int(pid)})
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].PID < members[j].PID })
	return members, nil
}

// holds reports whether pid is a process still running in the job. A pid
// the job lists that this process may not open is counted as running, since
// nothing shows it has exited.
func (t *Tree) holds(pid uint32) (bool, error) {
	process, err := openProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil // The process has exited.
	}
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("execx: open job member %d: %w", pid, err)
	}
	defer windows.CloseHandle(process)
	var isMember int32
	if ok, _, err := isProcessInUsageJob.Call(uintptr(process), uintptr(t.job), uintptr(unsafe.Pointer(&isMember))); ok == 0 {
		return false, fmt.Errorf("execx: verify job member %d: %w", pid, err)
	}
	if isMember == 0 {
		return false, nil
	}
	event, err := windows.WaitForSingleObject(process, 0)
	if err != nil {
		return false, fmt.Errorf("execx: read whether job member %d runs: %w", pid, err)
	}
	return event == uint32(windows.WAIT_TIMEOUT), nil
}

func (t *Tree) processWorkingSet(pid uint32) (uint64, error) {
	process, err := openProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return 0, nil // The sampled process has exited.
	}
	if err != nil {
		return 0, fmt.Errorf("execx: open job member %d for measurement: %w", pid, err)
	}
	defer windows.CloseHandle(process)
	// A recycled PID must not attribute an unrelated process to this job.
	var isMember int32
	if ok, _, err := isProcessInUsageJob.Call(uintptr(process), uintptr(t.job), uintptr(unsafe.Pointer(&isMember))); ok == 0 {
		return 0, fmt.Errorf("execx: verify job member %d: %w", pid, err)
	}
	if isMember == 0 {
		return 0, nil
	}
	memory := processMemoryUsage{Size: uint32(unsafe.Sizeof(processMemoryUsage{}))}
	if ok, _, err := getProcessMemoryUsage.Call(uintptr(process), uintptr(unsafe.Pointer(&memory)), uintptr(memory.Size)); ok == 0 {
		return 0, fmt.Errorf("execx: read job member %d private working set: %w", pid, err)
	}
	return uint64(memory.PrivateWorkingSet), nil
}

// TerminateAndWait ends every member, sparing none, and retains the job
// until Windows confirms it empty. Recovery uses it: a run's end
// (EndAndWait) keeps its job's kill on close armed until nothing but machine
// services is left in it, so a controller that died before then took
// everything in its job with it. An error retains custody for a retry.
// Cleanup takes at most five seconds, or the context's limit. Unlike
// Release, no descendant may outlive successful cleanup.
func (t *Tree) TerminateAndWait(ctx context.Context) (TreeUsage, error) {
	usage, _, err := t.endAndWait(ctx, false)
	return usage, err
}

// EndAndWait is TerminateAndWait for a run whose end must not end a machine
// service it started: it ends every member but a machine service and what
// runs under it (proc.EndJob), as End does, and returns the services it
// spared once nothing else runs in the job: every other process the job's
// own list names must have exited, one this process may not open counts as
// running, since the end could neither read nor end it, and the job must
// count no process beyond its list, as it does while a process exits
// (proc.JobProcessIDs). Only then does the job stop ending what it holds
// when its last handle closes, so the services run on after it, outside its
// custody. Until then, and after any failure, that close still ends
// everything in the job. With none to spare it confirms the job empty as
// TerminateAndWait does.
func (t *Tree) EndAndWait(ctx context.Context) (TreeUsage, []EndedProcess, error) {
	return t.endAndWait(ctx, true)
}

func (t *Tree) endAndWait(ctx context.Context, shouldSpareServices bool) (TreeUsage, []EndedProcess, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !t.mu.TryLock() {
		select {
		case <-ctx.Done():
			return TreeUsage{}, nil, ctx.Err()
		case <-ticker.C:
		}
	}
	defer t.mu.Unlock()
	if t.isDone {
		return TreeUsage{}, nil, errors.New("execx: job custody is closed")
	}
	if err := ctx.Err(); err != nil {
		return TreeUsage{}, nil, err
	}
	if shouldSpareServices {
		for {
			_, spared, err := proc.EndJob(ctx, t.job)
			if err != nil {
				return TreeUsage{}, nil, err
			}
			// With no service left to spare, the job must empty, as after a
			// strict end.
			if len(spared) == 0 {
				break
			}
			ids, unlisted, err := t.memberIDs()
			if err != nil {
				return TreeUsage{}, nil, err
			}
			isSpared := make(map[uint32]bool, len(spared))
			for _, entry := range spared {
				isSpared[uint32(entry.PID)] = true
			}
			var running []uint32
			for _, id := range ids {
				if isSpared[id] {
					continue
				}
				isRunning, err := t.holds(id)
				if err != nil {
					return TreeUsage{}, nil, err
				}
				if isRunning {
					running = append(running, id)
				}
			}
			if len(running) == 0 && unlisted == 0 {
				usage, err := t.jobUsage()
				if err != nil {
					return TreeUsage{}, nil, err
				}
				if err := proc.KeepJobOnClose(t.job); err != nil {
					return TreeUsage{}, nil, err
				}
				if err := windows.CloseHandle(t.job); err != nil {
					return usage, nil, fmt.Errorf("execx: close verification job: %w", err)
				}
				t.isDone = true
				kept := make([]EndedProcess, len(spared))
				for index, entry := range spared {
					kept[index] = EndedProcess{Name: entry.ExeBase, PID: entry.PID}
				}
				return usage, kept, nil
			}
			select {
			case <-ctx.Done():
				return TreeUsage{}, nil, fmt.Errorf("execx: the job's processes %v still run, and it counts %d more than it lists, beside the machine services its end spared: %w", running, unlisted, ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	} else if err := windows.TerminateJobObject(t.job, 1); err != nil {
		return TreeUsage{}, nil, fmt.Errorf("execx: terminate verification job: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return TreeUsage{}, nil, err
		}
		usage, err := t.jobUsage()
		if err != nil {
			return TreeUsage{}, nil, err
		}
		if err := ctx.Err(); err != nil {
			return usage, nil, err
		}
		if usage.ActiveProcesses == 0 {
			if err := windows.CloseHandle(t.job); err != nil {
				return usage, nil, fmt.Errorf("execx: close empty verification job: %w", err)
			}
			t.isDone = true
			return usage, nil, nil
		}
		select {
		case <-ctx.Done():
			return usage, nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
