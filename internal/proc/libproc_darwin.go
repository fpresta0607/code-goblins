//go:build cgo

package proc

/*
#include <libproc.h>
#include <sys/proc_info.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"unsafe"
)

// The readings below have no sysctl and no call in golang.org/x/sys, so they
// go through libproc, which needs cgo (docs/plans/2026-11-11-linux-port.md,
// "cgo on macOS").

// uniqueID is the ID the kernel gave the process that has pid now, which it
// never gives another process until the machine starts again. Any process
// may read it of any other.
func uniqueID(pid int) (uint64, error) {
	var info C.struct_proc_uniqidentifierinfo
	if err := pidInfo(pid, C.PROC_PIDUNIQIDENTIFIERINFO, unsafe.Pointer(&info), C.int(unsafe.Sizeof(info))); err != nil {
		return 0, fmt.Errorf("proc: read process %d's unique ID: %w", pid, err)
	}
	return uint64(info.p_uniqueid), nil
}

// workingDirectoryOf is the directory pid runs in, which only a process of
// the same user, or root, may read.
func workingDirectoryOf(pid int) (string, error) {
	var info C.struct_proc_vnodepathinfo
	if err := pidInfo(pid, C.PROC_PIDVNODEPATHINFO, unsafe.Pointer(&info), C.int(unsafe.Sizeof(info))); err != nil {
		return "", err
	}
	return C.GoString(&info.pvi_cdir.vip_path[0]), nil
}

// imageOf is the path of the program file pid runs now, which follows a
// rename of the file.
func imageOf(pid int) (string, error) {
	buffer := make([]byte, C.PROC_PIDPATHINFO_MAXSIZE)
	read, err := C.proc_pidpath(C.int(pid), unsafe.Pointer(&buffer[0]), C.uint32_t(len(buffer)))
	if read <= 0 {
		return "", readError(err)
	}
	return string(buffer[:read]), nil
}

// pidInfo reads flavor's record of pid into info, which holds size bytes. A
// process that is gone is fs.ErrNotExist.
func pidInfo(pid int, flavor C.int, info unsafe.Pointer, size C.int) error {
	read, err := C.proc_pidinfo(C.int(pid), flavor, 0, info, size)
	if read != size {
		return readError(err)
	}
	return nil
}

// readError is the error of a libproc reading that failed with err, the
// errno it left. ESRCH, no such process, is fs.ErrNotExist.
func readError(err error) error {
	switch {
	case errors.Is(err, syscall.ESRCH):
		return fmt.Errorf("%v: %w", err, fs.ErrNotExist)
	case err == nil:
		return errors.New("libproc read less than asked and set no error")
	default:
		return err
	}
}
