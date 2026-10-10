package processor

import "testing"

// machine lists performance cores of so many threads each, then efficiency
// cores of one thread each, numbered as Windows numbers them: in order.
func machine(performance, threadsEach, efficiency int) []Core {
	var cores []Core
	next := 0
	for range performance {
		core := Core{EfficiencyClass: 1}
		for range threadsEach {
			core.Threads = append(core.Threads, next)
			next++
		}
		cores = append(cores, core)
	}
	for range efficiency {
		cores = append(cores, Core{EfficiencyClass: 0, Threads: []int{next}})
		next++
	}
	return cores
}

// The fleet's work took every core at the priority of the Overlord's own apps
// (2026-10-10 on a PC of six performance and four efficiency cores: a process
// launch took 1.70 s under 16 busy threads on every core, 0.21 s with the
// work kept off one performance core and 0.13 s with it kept off two). It
// keeps off a quarter of the performance cores, rounded up to whole cores.
func TestFleetWorkKeepsOffAQuarterOfThePerformanceCores(t *testing.T) {
	every := ^uintptr(0)
	for _, test := range []struct {
		name   string
		cores  []Core
		groups int
		own    uintptr
		want   uintptr
	}{
		{"the PC it was measured on, six performance cores of two threads and four efficiency cores", machine(6, 2, 4), 1, every, 0xF0FF},
		{"eight cores of one kind, two threads each", machine(8, 2, 0), 1, every, 0x0FFF},
		{"five cores of one kind", machine(5, 1, 0), 1, every, 0b00111},
		{"four cores of one kind", machine(4, 1, 0), 1, every, 0b0111},
		{"two cores of one kind", machine(2, 1, 0), 1, every, 0b01},
		{
			"efficiency cores numbered before the performance cores",
			[]Core{{0, []int{0}}, {0, []int{1}}, {1, []int{2, 3}}, {1, []int{4, 5}}, {1, []int{6, 7}}, {1, []int{8, 9}}},
			1, every, 0x0FF,
		},
		{"a process already kept to four of the PC's threads, which is given no thread it lacked", machine(6, 2, 4), 1, 0x0F0F, 0x000F},
		{"a process kept to the threads the apps are left, which has no other to keep to", machine(6, 2, 4), 1, 0x0F00, 0},
		{"one performance core, which cannot be spared", machine(1, 2, 4), 1, every, 0},
		{"a machine of more than one processor group, where a mask names one group's threads only", machine(6, 2, 4), 2, every, 0},
		{"a machine that names no cores", nil, 1, every, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := fleetThreads(test.cores, test.groups, test.own)

			// Assert
			if got != test.want {
				t.Fatalf("the fleet's work keeps to the processor threads %#x, want %#x", got, test.want)
			}
		})
	}
}
