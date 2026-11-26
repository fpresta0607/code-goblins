package processor

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math/bits"
	"os"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// MachineCores lists this machine's online cores with their kind and
// threads, as /sys names them (readCores).
func MachineCores() ([]Core, error) {
	return readCores(os.DirFS("/sys"))
}

// readCores lists the online cores the sysfs sys names, in the order of their
// first thread, each with the online threads its topology/core_cpus_list
// names and its kind as an efficiency class, higher for a faster kind:
//   - on Intel's hybrid processors, 1 for the threads devices/cpu_core names
//     and 0 for the rest, the ones devices/cpu_atom names, since the kernel
//     keeps a counter unit for each of the two kinds;
//   - elsewhere, the rank of the core's cpu_capacity among the machine's,
//     which ARM's kernels set from each kind of core's speed;
//   - 0 for every core where neither is there, a machine of one kind.
func readCores(sys fs.FS) ([]Core, error) {
	online, err := readThreadList(sys, "devices/system/cpu/online")
	if err != nil {
		return nil, err
	}
	classes, err := readClasses(sys, online)
	if err != nil {
		return nil, err
	}
	isOnline := make(map[int]bool, len(online))
	for _, thread := range online {
		isOnline[thread] = true
	}
	named := make(map[int]bool, len(online))
	var cores []Core
	for _, thread := range online {
		if named[thread] {
			continue
		}
		siblings, err := readThreadList(sys, fmt.Sprintf("devices/system/cpu/cpu%d/topology/core_cpus_list", thread))
		if err != nil {
			return nil, err
		}
		core := Core{EfficiencyClass: classes[thread]}
		for _, sibling := range siblings {
			if isOnline[sibling] && !named[sibling] {
				core.Threads = append(core.Threads, sibling)
				named[sibling] = true
			}
		}
		cores = append(cores, core)
	}
	if len(cores) == 0 {
		return nil, errors.New("linux names no online processor cores")
	}
	return cores, nil
}

// readClasses is the efficiency class of each online thread's core
// (readCores).
func readClasses(sys fs.FS, online []int) (map[int]uint8, error) {
	classes := make(map[int]uint8, len(online))
	performance, err := readThreadList(sys, "devices/cpu_core/cpus")
	if err == nil {
		for _, thread := range performance {
			classes[thread] = 1
		}
		return classes, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	capacities := make(map[int]int, len(online))
	for _, thread := range online {
		path := fmt.Sprintf("devices/system/cpu/cpu%d/cpu_capacity", thread)
		text, err := fs.ReadFile(sys, path)
		if errors.Is(err, fs.ErrNotExist) {
			return classes, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read the processor thread's capacity: %w", err)
		}
		capacity, err := strconv.Atoi(strings.TrimSpace(string(text)))
		if err != nil {
			return nil, fmt.Errorf("read the processor thread's capacity from %s: %w", path, err)
		}
		capacities[thread] = capacity
	}
	ranks := slices.Compact(slices.Sorted(maps.Values(capacities)))
	for thread, capacity := range capacities {
		classes[thread] = uint8(slices.Index(ranks, capacity))
	}
	return classes, nil
}

// readThreadList reads a list of processor threads in the kernel's form,
// numbers and ranges split by commas, such as 0-3,8.
func readThreadList(sys fs.FS, path string) ([]int, error) {
	text, err := fs.ReadFile(sys, path)
	if err != nil {
		return nil, fmt.Errorf("read the processor threads: %w", err)
	}
	var threads []int
	for _, part := range strings.Split(strings.TrimSpace(string(text)), ",") {
		if part == "" {
			continue
		}
		first, last, isRange := strings.Cut(part, "-")
		from, err := strconv.Atoi(first)
		if err != nil {
			return nil, fmt.Errorf("read the processor threads %q from %s: %w", text, path, err)
		}
		to := from
		if isRange {
			if to, err = strconv.Atoi(last); err != nil || to < from {
				return nil, fmt.Errorf("read the processor threads %q from %s: %q is no range", text, path, part)
			}
		}
		for thread := from; thread <= to; thread++ {
			threads = append(threads, thread)
		}
	}
	return threads, nil
}

// MachineFleetThreads is the mask of this machine's processor threads the
// fleet's work keeps to, which leaves the Overlord's own apps half of the
// performance cores (fleetThreads), and 0 for no limit. A mask names a
// machine's first threads only, as many as it has bits, so a machine with
// more threads than that sets no limit, as one of more than one processor
// group does on Windows.
func MachineFleetThreads() (uintptr, error) {
	cores, err := MachineCores()
	if err != nil {
		return 0, err
	}
	var set unix.CPUSet
	if err := unix.SchedGetaffinity(0, &set); err != nil {
		return 0, fmt.Errorf("read the processor threads this process may run on: %w", err)
	}
	var own uintptr
	for thread := range bits.UintSize {
		if set.IsSet(thread) {
			own |= 1 << thread
		}
	}
	highest := 0
	for _, core := range cores {
		for _, thread := range core.Threads {
			highest = max(highest, thread)
		}
	}
	return fleetThreads(cores, highest/bits.UintSize+1, own), nil
}
