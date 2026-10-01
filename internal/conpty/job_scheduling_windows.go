package conpty

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const JOB_OBJECT_MSG_NEW_PROCESS = 6

var (
	isProcessInJob            = kernel32.NewProc("IsProcessInJob")
	getQueuedCompletionStatus = kernel32.NewProc("GetQueuedCompletionStatus")
)

type jobScheduling struct {
	job, port      windows.Handle
	done           chan struct{}
	mu             sync.Mutex
	lastReconciled time.Time
	isClosed       bool
}

func monitorJobScheduling(job windows.Handle) (*jobScheduling, error) {
	// Like Go's netpoller, allow goroutine migration between OS threads
	// without leaving a previous thread occupying the port's only slot.
	port, err := windows.CreateIoCompletionPort(windows.InvalidHandle, 0, 0, ^uint32(0))
	if err != nil {
		return nil, fmt.Errorf("conpty: scheduling completion port: %w", err)
	}
	association := struct {
		Key  uintptr
		Port windows.Handle
	}{1, port}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectAssociateCompletionPortInformation, uintptr(unsafe.Pointer(&association)), uint32(unsafe.Sizeof(association))); err != nil {
		windows.CloseHandle(port)
		return nil, fmt.Errorf("conpty: associate scheduling port: %w", err)
	}
	scheduling := &jobScheduling{job: job, port: port, done: make(chan struct{})}
	go scheduling.monitor()
	return scheduling, nil
}

func (s *jobScheduling) monitor() {
	defer close(s.done)
	for {
		var message uint32
		var key, pid uintptr
		// Job messages carry an integer PID in lpOverlapped, not a Go pointer.
		result, _, err := getQueuedCompletionStatus.Call(uintptr(s.port), uintptr(unsafe.Pointer(&message)), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&pid)), windows.INFINITE)
		if result == 0 {
			fmt.Fprintf(os.Stderr, "conpty: scheduling monitor: %v\n", err)
			return
		}
		if key == 0 {
			return
		}
		if message == JOB_OBJECT_MSG_NEW_PROCESS {
			if err := s.scheduleProcess(uint32(pid)); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}
	}
}

func (s *jobScheduling) scheduleProcess(pid uint32) error {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_SET_INFORMATION, false, pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil // The notification can outlive a short-lived process.
	}
	if err != nil {
		return fmt.Errorf("conpty: open job process %d for scheduling: %w", pid, err)
	}
	defer windows.CloseHandle(process)
	var isMember int32
	result, _, err := isProcessInJob.Call(uintptr(process), uintptr(s.job), uintptr(unsafe.Pointer(&isMember)))
	if result == 0 {
		return fmt.Errorf("conpty: verify job process %d: %w", pid, err)
	}
	if isMember == 0 {
		return nil // A recycled PID or a process that broke away is not ours.
	}
	policy, err := processPowerPolicy(process)
	if err != nil {
		return fmt.Errorf("conpty: read job process %d scheduling: %w", pid, err)
	}
	if policy.ControlMask&EXECUTION_SPEED != 0 {
		return nil // Keep an application's deliberate EcoQoS or HighQoS choice.
	}
	if err := interactiveScheduling(process); err != nil {
		return fmt.Errorf("conpty: schedule job process %d: %w", pid, err)
	}
	return nil
}

// Job notifications are advisory. Reconcile at an input boundary, at most
// once a second, so a lost notification cannot leave a wrapper's harness
// permanently throttled. This queries only our job, never the whole machine.
func (s *jobScheduling) reconcile() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isClosed {
		return errors.New("conpty: scheduling job has closed")
	}
	if time.Since(s.lastReconciled) < time.Second {
		return nil
	}
	s.lastReconciled = time.Now()
	for capacity := 64; capacity <= 65536; capacity *= 2 {
		buffer := make([]uintptr, capacity+2)
		err := windows.QueryInformationJobObject(s.job, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(&buffer[0])), uint32(len(buffer))*uint32(unsafe.Sizeof(uintptr(0))), nil)
		if errors.Is(err, windows.ERROR_MORE_DATA) {
			continue
		}
		if err != nil {
			return fmt.Errorf("conpty: reconcile job scheduling: %w", err)
		}
		count := *(*uint32)(unsafe.Add(unsafe.Pointer(&buffer[0]), 4))
		ids := unsafe.Slice((*uintptr)(unsafe.Add(unsafe.Pointer(&buffer[0]), 8)), int(count))
		var failures []error
		for _, pid := range ids {
			if err := s.scheduleProcess(uint32(pid)); err != nil {
				failures = append(failures, err)
			}
		}
		return errors.Join(failures...)
	}
	return errors.New("conpty: job process list exceeds 65536 processes")
}

func (s *jobScheduling) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isClosed = true
	_ = windows.PostQueuedCompletionStatus(s.port, 0, 0, nil)
	<-s.done
	windows.CloseHandle(s.port)
}
