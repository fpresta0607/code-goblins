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
// to: every thread of cores but those of the last performance cores, the ones
// left to the Overlord's own apps, and none that own, the threads the asking
// process may run on, lacks. It is 0, for no limit, when the machine has one
// performance core or none to read, when it has more than one processor
// group, where a mask names one group's threads only, and when own has no
// thread outside the cores left to the apps.
func fleetThreads(cores []Core, groups int, own uintptr) uintptr {
	if groups != 1 {
		return 0
	}
	fastest := uint8(0)
	for _, core := range cores {
		fastest = max(fastest, core.EfficiencyClass)
	}
	var performance []Core
	var threads uintptr
	for _, core := range cores {
		if core.EfficiencyClass == fastest {
			performance = append(performance, core)
		}
		for _, thread := range core.Threads {
			threads |= 1 << thread
		}
	}
	left := (len(performance) + appsCoreIn - 1) / appsCoreIn
	if left >= len(performance) {
		return 0
	}
	for _, core := range performance[len(performance)-left:] {
		for _, thread := range core.Threads {
			threads &^= 1 << thread
		}
	}
	return threads & own
}
