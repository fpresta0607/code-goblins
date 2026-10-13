package execx

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// openProcess opens a job member, and a test makes one this process may not
// open without the privilege that would let it open any.
var openProcess = windows.OpenProcess

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

func (t *Tree) jobUsage() (TreeUsage, error) {
	if t.isDone {
		return TreeUsage{}, errors.New("execx: job accounting is closed")
	}
	var accounting jobAccounting
	if err := windows.QueryInformationJobObject(t.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
		return TreeUsage{}, fmt.Errorf("execx: read job accounting: %w", err)
	}
	var memory jobMemoryUsage
	const jobObjectMemoryUsageInformation = 28
	if err := windows.QueryInformationJobObject(t.job, jobObjectMemoryUsageInformation, uintptr(unsafe.Pointer(&memory)), uint32(unsafe.Sizeof(memory)), nil); err != nil {
		return TreeUsage{}, fmt.Errorf("execx: read job commitment: %w", err)
	}
	return TreeUsage{
		CPU:         time.Duration(accounting.UserTime+accounting.KernelTime) * 100,
		CommitBytes: memory.Commit, PeakCommitBytes: memory.PeakCommit,
		ActiveProcesses: accounting.ActiveProcesses, TotalProcesses: accounting.TotalProcesses,
	}, nil
}

// privateWorkingSet sums the private working set of the job's members, and
// counts the members this process may not read, which the sum leaves out.
func (t *Tree) privateWorkingSet() (total uint64, unreadable uint32, err error) {
	ids, err := t.memberIDs()
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

// memberIDs reads the ids of the processes the job holds now.
func (t *Tree) memberIDs() ([]uint32, error) {
	// Two DWORD counts precede pointer-sized process IDs. Growth is bounded
	// so a changing job cannot turn one sample into an unbounded allocation.
	const headerWords = 8 / unsafe.Sizeof(uintptr(0))
	for capacity := 64; capacity <= 65536; capacity *= 4 {
		buffer := make([]uintptr, int(headerWords)+capacity)
		err := windows.QueryInformationJobObject(t.job, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(&buffer[0])), uint32(len(buffer))*uint32(unsafe.Sizeof(uintptr(0))), nil)
		if errors.Is(err, windows.ERROR_MORE_DATA) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("execx: read job members: %w", err)
		}
		counts := (*[2]uint32)(unsafe.Pointer(&buffer[0]))
		if counts[0] != counts[1] || counts[1] > uint32(capacity) {
			return nil, errors.New("execx: incomplete job member sample")
		}
		ids := make([]uint32, counts[1])
		for index, pid := range buffer[int(headerWords) : int(headerWords)+int(counts[1])] {
			ids[index] = uint32(pid)
		}
		return ids, nil
	}
	return nil, errors.New("execx: job member sample exceeds its bound")
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
	ids, err := t.memberIDs()
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
// own list names must have exited, and one this process may not open counts
// as running, since the end could neither read nor end it. Only then does
// the job stop ending what it holds when its last handle closes, so the
// services run on after it, outside its custody. Until then, and after any
// failure, that close still ends everything in the job. With none to spare
// it confirms the job empty as TerminateAndWait does.
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
			// With no service there, the end ended the whole job.
			if len(spared) == 0 {
				break
			}
			ids, err := t.memberIDs()
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
			if len(running) == 0 {
				if err := proc.KeepJobOnClose(t.job); err != nil {
					return TreeUsage{}, nil, err
				}
				usage, err := t.jobUsage()
				if err != nil {
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
				return TreeUsage{}, nil, fmt.Errorf("execx: the job's processes %v still run beside the machine services its end spared: %w", running, ctx.Err())
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
