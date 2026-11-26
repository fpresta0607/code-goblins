package browserlogin

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// jobUsage is a job's JOBOBJECT_BASIC_AND_IO_ACCOUNTING_INFORMATION: what
// every process it has held used, the ended ones included. Times are in
// 100 ns units, and the blank fields hold the parts nothing reads.
type jobUsage struct {
	userTime, kernelTime       int64
	_, _                       int64
	pageFaults                 uint32
	processes, activeProcesses uint32
	_                          uint32
	io                         windows.IO_COUNTERS
}

// describeStart says what process's Chrome did in the waited time its start
// gave its DevTools: whether it used the processor, faulted pages in and
// read files, as a Chrome working slowly does, or sat idle, and whether its
// pipe brought anything back.
func describeStart(process Process, pipe *devToolsPipe, waited time.Duration) string {
	usage := new(jobUsage)
	if err := proc.QueryJobInformation(process.job.handle, windows.JobObjectBasicAndIoAccountingInformation, unsafe.Pointer(usage), unsafe.Sizeof(*usage)); err != nil {
		return fmt.Sprintf("chrome %d's DevTools wait ended %s after it was resumed, and its job's use could not be read: %v", process.PID, waited.Round(time.Millisecond), err)
	}
	processor := time.Duration(usage.userTime+usage.kernelTime) * 100
	return fmt.Sprintf("chrome %d's DevTools wait ended %s after it was resumed, when %d of the %d processes its job had held still ran, having used %s of processor time, %d page faults and %d bytes read in %d reads, and %d messages had come back on its pipe",
		process.PID, waited.Round(time.Millisecond), usage.activeProcesses, usage.processes, processor.Round(time.Millisecond), usage.pageFaults, usage.io.ReadTransferCount, usage.io.ReadOperationCount, pipe.received.Load())
}
