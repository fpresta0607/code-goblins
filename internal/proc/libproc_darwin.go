//go:build cgo

package proc

/*
#include <stdint.h>
#include <libproc.h>
#include <mach/mach_time.h>
#include <sys/proc_info.h>
#include <sys/resource.h>

// start_ticks sets ticks to when pid started, in the ticks of the machine's
// absolute clock, and fails with errno as proc_pid_rusage leaves it.
static int start_ticks(int pid, uint64_t *ticks) {
	struct rusage_info_v0 info;
	if (proc_pid_rusage(pid, RUSAGE_INFO_V0, (rusage_info_t *)&info) != 0) {
		return -1;
	}
	*ticks = info.ri_proc_start_abstime;
	return 0;
}

// use_of sets used to the processor time pid and the children it collected
// used, in the ticks of the machine's absolute clock, and private to its
// memory footprint, the memory no other process shares, and fails with errno
// as proc_pid_rusage leaves it.
static int use_of(int pid, uint64_t *used, uint64_t *private) {
	struct rusage_info_v2 info;
	if (proc_pid_rusage(pid, RUSAGE_INFO_V2, (rusage_info_t *)&info) != 0) {
		return -1;
	}
	*used = info.ri_user_time + info.ri_system_time + info.ri_child_user_time + info.ri_child_system_time;
	*private = info.ri_phys_footprint;
	return 0;
}

// nanoseconds_per_tick sets numer and denom to the fraction that turns the
// absolute clock's ticks into nanoseconds.
static int nanoseconds_per_tick(uint32_t *numer, uint32_t *denom) {
	mach_timebase_info_data_t base;
	if (mach_timebase_info(&base) != KERN_SUCCESS || base.denom == 0) {
		return -1;
	}
	*numer = base.numer;
	*denom = base.denom;
	return 0;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"time"
	"unsafe"
)

// The readings below have no sysctl and no call in golang.org/x/sys, so they
// go through libproc, which needs cgo (docs/plans/2026-11-11-linux-port.md,
// "cgo on macOS").

// startTicks is when the process that has pid now started, in the ticks of
// the machine's absolute clock (ri_proc_start_abstime from
// proc_pid_rusage). That clock counts from the machine's start and is never
// stepped, and one ID is held by one process at a time, so no two processes
// with one ID read the same ticks before the machine starts again. The
// kernel's own unique ID of a process (p_uniqueid) is not in the public SDK.
// Only a process of the same user, or root, may read it.
func startTicks(pid int) (uint64, error) {
	var ticks C.uint64_t
	if failed, err := C.start_ticks(C.int(pid), &ticks); failed != 0 {
		return 0, fmt.Errorf("proc: read process %d's start: %w", pid, readError(err))
	}
	return uint64(ticks), nil
}

// useOf is what the process that has pid uses now: the processor time it
// and the children it collected used, and its memory footprint, which only a
// process of the same user, or root, may read.
func useOf(pid int) (Use, error) {
	var used, private C.uint64_t
	if failed, err := C.use_of(C.int(pid), &used, &private); failed != 0 {
		return Use{}, fmt.Errorf("proc: read what process %d uses: %w", pid, readError(err))
	}
	var numer, denom C.uint32_t
	if C.nanoseconds_per_tick(&numer, &denom) != 0 {
		return Use{}, errors.New("proc: read the machine's absolute clock rate")
	}
	// Divided first, so the product with the rate stays within 64 bits.
	ticks, over, base := uint64(used), uint64(numer), uint64(denom)
	nanoseconds := ticks/base*over + ticks%base*over/base
	return Use{CPU: time.Duration(nanoseconds), PrivateBytes: uint64(private)}, nil
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
