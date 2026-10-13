package execx

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// KillsOnCloseForTest reports whether the tree's job still ends what it holds
// when its last handle closes.
func (t *Tree) KillsOnCloseForTest() (bool, error) {
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(t.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
		return false, err
	}
	return limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE != 0, nil
}
