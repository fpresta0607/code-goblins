// Package processor reads this machine's processor cores by kind and names
// the processor threads the fleet's work keeps to, so the Overlord's own apps
// keep cores of their own.
package processor

// Core is one processor core: its efficiency class, higher for a faster kind
// of core, and the numbers of its threads.
type Core struct {
	EfficiencyClass uint8
	Threads         []int
}
