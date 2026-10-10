package supervisor

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/processor"
)

// processorMeter keeps the last reading of the processors' times, which the
// next is measured from, and when it was taken: the zero time before any.
type processorMeter struct {
	mu        sync.Mutex
	cores     []processor.Core
	times     []processorTime
	processes []processCommit
	at        time.Time
	last      Processors
}

var machineProcessors processorMeter

// MachineProcessors reads this machine's processor cores by kind and the
// share of its performance cores that sat idle since the last reading, which
// the supervisor takes every minute while work waits, or over a moment when
// there is no reading that recent (howToReadProcessors). It reads the cores
// of the first processor group, all of them on a machine of up to 64 threads.
func MachineProcessors() (Processors, error) {
	return machineProcessors.read()
}

func (m *processorMeter) read() (Processors, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cores == nil {
		cores, err := processor.MachineCores()
		if err != nil {
			return Processors{}, err
		}
		m.cores = cores
	}
	window := time.Since(m.at)
	switch howToReadProcessors(!m.at.IsZero(), window) {
	case repeatLast:
		return m.last, nil
	case watchAMoment:
		times, err := machineProcessorTimes()
		if err != nil {
			return Processors{}, err
		}
		// Who was busy is a courtesy to the tip: a process list that cannot
		// be read names nobody and holds no reading back.
		m.times, m.processes, window = times, nil, processorMoment
		if processes, err := machineProcessList(); err == nil {
			m.processes = processes
		}
		time.Sleep(processorMoment)
	}
	times, err := machineProcessorTimes()
	if err != nil {
		return Processors{}, err
	}
	processors, err := readProcessors(m.cores, m.times, times)
	if err != nil {
		return Processors{}, err
	}
	processes, err := machineProcessList()
	if err == nil && m.processes != nil {
		processors.Busiest = busiestApps(m.processes, processes, window)
	}
	m.times, m.processes, m.at, m.last = times, processes, time.Now(), processors
	return processors, nil
}

// systemProcessorPerformance is Windows'
// SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION, in units of 100 ns: the kernel
// time counts the idle time.
type systemProcessorPerformance struct {
	idle, kernel, user, dpc, interrupt int64
	interrupts                         uint32
}

// machineProcessorTimes reads each thread's idle and whole time since the
// machine started, for the threads of the first processor group.
func machineProcessorTimes() ([]processorTime, error) {
	rows := make([]systemProcessorPerformance, 64)
	var written uint32
	if err := windows.NtQuerySystemInformation(windows.SystemProcessorPerformanceInformation, unsafe.Pointer(&rows[0]), uint32(len(rows))*uint32(unsafe.Sizeof(rows[0])), &written); err != nil {
		return nil, fmt.Errorf("read the processors' times: %w", err)
	}
	count := int(written) / int(unsafe.Sizeof(rows[0]))
	times := make([]processorTime, count)
	for thread := range count {
		times[thread] = processorTime{idle: uint64(rows[thread].idle), total: uint64(rows[thread].kernel + rows[thread].user)}
	}
	return times, nil
}
