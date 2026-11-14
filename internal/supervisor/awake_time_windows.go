package supervisor

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var queryUnbiasedInterruptTime = windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryUnbiasedInterruptTime")

// awakeTime is how long the machine has been awake since it started:
// Windows' unbiased interrupt time, which stops while the machine sleeps or
// hibernates, where Go's monotonic clock on Windows runs on.
func awakeTime() time.Duration {
	var ticks uint64 // in units of 100 ns
	queryUnbiasedInterruptTime.Call(uintptr(unsafe.Pointer(&ticks)))
	return time.Duration(ticks) * 100
}
