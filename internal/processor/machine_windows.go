package processor

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                         = windows.NewLazySystemDLL("kernel32.dll")
	getLogicalProcessorInformationEx = kernel32.NewProc("GetLogicalProcessorInformationEx")
	getProcessAffinityMask           = kernel32.NewProc("GetProcessAffinityMask")
	getActiveProcessorGroupCount     = kernel32.NewProc("GetActiveProcessorGroupCount")
)

// relationProcessorCore asks GetLogicalProcessorInformationEx for one record
// a core.
const relationProcessorCore = 0

// MachineCores lists the cores of processor group 0 with their efficiency
// class and threads, from Windows' records of SYSTEM_LOGICAL_PROCESSOR_
// INFORMATION_EX: each is its relationship and size, then for a core its
// flags, its efficiency class at byte 9, and from byte 32 the mask of its
// threads in its group and at byte 40 that group's number.
func MachineCores() ([]Core, error) {
	var size uint32
	_, _, _ = getLogicalProcessorInformationEx.Call(relationProcessorCore, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		return nil, errors.New("windows names no processor cores")
	}
	buffer := make([]byte, size)
	if ok, _, err := getLogicalProcessorInformationEx.Call(relationProcessorCore, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size))); ok == 0 {
		return nil, fmt.Errorf("read the processor cores: %w", err)
	}
	var cores []Core
	for offset := uint32(0); offset+48 <= size; {
		record := buffer[offset:]
		length := binary.LittleEndian.Uint32(record[4:8])
		if length < 48 || offset+length > size {
			return nil, errors.New("windows gave a processor core record of no sensible size")
		}
		mask := binary.LittleEndian.Uint64(record[32:40])
		if group := binary.LittleEndian.Uint16(record[40:42]); group == 0 {
			core := Core{EfficiencyClass: record[9]}
			for ; mask != 0; mask &= mask - 1 {
				core.Threads = append(core.Threads, bits.TrailingZeros64(mask))
			}
			cores = append(cores, core)
		}
		offset += length
	}
	if len(cores) == 0 {
		return nil, errors.New("windows names no processor cores in the first processor group")
	}
	return cores, nil
}

// MachineFleetThreads is the mask of this machine's processor threads the
// fleet's work keeps to, which leaves the Overlord's own apps half of the
// performance cores (fleetThreads), and 0 for no limit.
func MachineFleetThreads() (uintptr, error) {
	cores, err := MachineCores()
	if err != nil {
		return 0, err
	}
	own, err := ProcessThreads(windows.CurrentProcess())
	if err != nil {
		return 0, err
	}
	groups, _, _ := getActiveProcessorGroupCount.Call()
	return fleetThreads(cores, int(groups), own), nil
}

// ProcessThreads is the mask of the processor threads process may run on.
func ProcessThreads(process windows.Handle) (uintptr, error) {
	var own, machine uintptr
	if result, _, err := getProcessAffinityMask.Call(uintptr(process), uintptr(unsafe.Pointer(&own)), uintptr(unsafe.Pointer(&machine))); result == 0 {
		return 0, fmt.Errorf("read the processor threads a process may run on: %w", err)
	}
	return own, nil
}
