package processor

// appsCoreIn is how many performance cores hold one the fleet's work leaves
// to the Overlord's own apps: half of them, rounded up to whole cores. On
// 2026-10-10, on a PC of six performance cores, a test Chrome showed its
// window after 0.53 s with nothing added, 4.11 s under goblin work on every
// core, 2.33 s with the work kept off two performance cores, 0.98 s with it
// kept off three and 1.75 s with it kept off four, each a median, of two
// starts for three cores and three for four. Kept off two, three and four
// cores the work got through 72%, 59% and 45% as much while it filled every
// core it kept. Three is the fewest from which one more bought nothing.
const appsCoreIn = 2

// fleetThreads is the mask of the processor threads the fleet's work keeps
// to: the threads of the performance cores, every core on a machine of one
// kind, but those of the last of them, the ones left to the Overlord's own
// apps, and none that own, the threads the asking process may run on, lacks.
// It is 0, for no limit, when the machine has one performance core or none to
// read, when it has more than one processor group, where a mask names one
// group's threads only, and when own has none of those threads.
//
// Efficiency cores are out because Windows keeps a hidden process that asks
// for nothing on them and does not move that work to a performance core when
// they fill, and what a terminal starts asks for nothing. On 2026-11-05, on a
// PC of six performance and four efficiency cores, eight hidden CPU-bound
// processes of 6 seconds' work each were started together. With the efficiency
// cores in the mask 1 or 2 of them finished within the wait, the efficiency
// cores 100 percent busy and the performance threads 13 to 70. Without them
// all 8 did, the performance threads 50 to 100 percent busy. The figures on
// appsCoreIn were taken with the efficiency cores in the mask.
func fleetThreads(cores []Core, groups int, own uintptr) uintptr {
	if groups != 1 {
		return 0
	}
	fastest := uint8(0)
	for _, core := range cores {
		fastest = max(fastest, core.EfficiencyClass)
	}
	var performance []Core
	for _, core := range cores {
		if core.EfficiencyClass == fastest {
			performance = append(performance, core)
		}
	}
	left := (len(performance) + appsCoreIn - 1) / appsCoreIn
	if left >= len(performance) {
		return 0
	}
	var threads uintptr
	for _, core := range performance[:len(performance)-left] {
		for _, thread := range core.Threads {
			threads |= 1 << thread
		}
	}
	return threads & own
}
