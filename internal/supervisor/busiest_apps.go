package supervisor

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

const (
	// busiestAppCount is how many apps the CPU meter's tip names.
	busiestAppCount = 2
	// busiestAppShare is the least an app must have used, of one core, to be
	// named: under it nobody is worth naming.
	busiestAppShare = 0.25
)

// busiestApps names the apps that used the most processor between two
// readings of the process list, window apart, most first: an app is a first
// process and everything it started, as the commit holders count one, so a
// goblin's compilers count as the fleet. A process in after only, or one
// that took an ended process's ID, is new and its whole use counts. It names
// at most busiestAppCount, and none that used under busiestAppShare of one
// core.
func busiestApps(before, after []processCommit, window time.Duration) []string {
	if window <= 0 {
		return nil
	}
	type identity struct {
		pid     uint32
		created int64
	}
	earlier := make(map[identity]uint64, len(before))
	for _, process := range before {
		earlier[identity{process.pid, process.created}] = process.cpu
	}
	app := appNames(after)
	type use struct {
		name string
		cpu  uint64
	}
	groups := map[string]*use{}
	for _, process := range after {
		used := process.cpu
		if was, ok := earlier[identity{process.pid, process.created}]; ok {
			if process.cpu < was {
				continue
			}
			used = process.cpu - was
		}
		if process.name == "" || used == 0 {
			continue
		}
		name := app(process)
		key := strings.ToLower(name)
		if groups[key] == nil {
			groups[key] = &use{name: name}
		}
		groups[key].cpu += used
	}
	least := uint64(busiestAppShare * float64(window/100))
	var busiest []use
	for _, group := range groups {
		if group.cpu >= least {
			busiest = append(busiest, *group)
		}
	}
	slices.SortFunc(busiest, func(a, b use) int {
		return cmp.Or(cmp.Compare(b.cpu, a.cpu), strings.Compare(a.name, b.name))
	})
	var names []string
	for _, one := range busiest[:min(busiestAppCount, len(busiest))] {
		names = append(names, one.name)
	}
	return names
}
