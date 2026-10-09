package supervisor

import (
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	// processorWindow is the least time between two readings of the
	// processors' times: a call sooner than that gets the last answer, so two
	// callers at one moment do not judge the machine by a few milliseconds.
	processorWindow = 10 * time.Second
	// processorStale is the oldest reading the next is still measured from.
	// The supervisor reads every minute while work waits, and not at all
	// while none does.
	processorStale = 2 * time.Minute
	// processorMoment is how long a reading watches when it has no earlier
	// one to measure from.
	processorMoment = 250 * time.Millisecond
)

// processorReading is how one reading of the processors is taken.
type processorReading int

const (
	// repeatLast gives the last answer again.
	repeatLast processorReading = iota
	// watchAMoment reads the times twice, processorMoment apart.
	watchAMoment
	// sinceLast measures from the last reading's times.
	sinceLast
)

// howToReadProcessors says how a reading is taken, age after the last one: a
// reading of how busy the processors were since the last says little of now
// when the last is from before a quiet stretch, so then, as with none at all,
// the reader watches for a moment instead.
func howToReadProcessors(hasEarlier bool, age time.Duration) processorReading {
	switch {
	case !hasEarlier || age > processorStale:
		return watchAMoment
	case age < processorWindow:
		return repeatLast
	}
	return sinceLast
}

// processorsNext is the share of the performance cores that must sit idle for
// the supervisor to start or resume a goblin by itself. It is a first mark:
// on 2026-10-09 a test start of the Overlord's Chrome was quick with half of
// them idle and slow with none.
const processorsNext = 0.25

// Processors is the machine's processor cores by kind and the share of the
// performance cores, the ones the Overlord's own apps run on, that sat idle
// over the reading. A machine with cores of one kind has performance cores
// only.
type Processors struct {
	PerformanceCores int `json:"performance_cores"`
	EfficiencyCores  int `json:"efficiency_cores"`
	// Free is the share of the performance cores that sat idle, from 0 to 1.
	Free float64 `json:"free"`
	// EfficiencyFree is the same of the efficiency cores, and 0 on a machine
	// with none.
	EfficiencyFree float64 `json:"efficiency_free"`
	// Next is the share of the performance cores that must be free for the
	// supervisor to start a goblin by itself, which the snapshot sets for the
	// meter's mark.
	Next float64 `json:"next"`
}

// processorCore is one core: its efficiency class, higher for a faster kind of
// core, and the numbers of its threads.
type processorCore struct {
	efficiencyClass uint8
	threads         []int
}

// processorTime is one thread's idle time and whole time since the machine
// started, in the same unit.
type processorTime struct {
	idle, total uint64
}

// readProcessors makes of two readings of every thread's times, the later one
// second, the cores by kind and how free the performance cores were between
// them. A core counts as busy while either of its threads is, since the
// second thread of a busy core gives an app a fraction of a core. Readings
// that say nothing, with no time between them or not of these cores, are an
// error and never a machine with room.
func readProcessors(cores []processorCore, before, after []processorTime) (Processors, error) {
	if len(cores) == 0 {
		return Processors{}, errors.New("the machine names no processor cores")
	}
	if len(before) != len(after) {
		return Processors{}, fmt.Errorf("two readings of the processors name %d and %d threads", len(before), len(after))
	}
	fastest := uint8(0)
	for _, core := range cores {
		fastest = max(fastest, core.efficiencyClass)
	}
	var processors Processors
	idle, efficiencyIdle := 0.0, 0.0
	for _, core := range cores {
		busiest := 0.0
		for _, thread := range core.threads {
			if thread < 0 || thread >= len(after) {
				return Processors{}, fmt.Errorf("a processor core names thread %d and the reading has %d", thread, len(after))
			}
			whole := after[thread].total - before[thread].total
			if after[thread].total <= before[thread].total || after[thread].idle < before[thread].idle {
				return Processors{}, fmt.Errorf("no time passed on thread %d between two readings of the processors", thread)
			}
			busiest = max(busiest, 1-float64(after[thread].idle-before[thread].idle)/float64(whole))
		}
		if core.efficiencyClass != fastest {
			processors.EfficiencyCores++
			efficiencyIdle += 1 - min(max(busiest, 0), 1)
			continue
		}
		processors.PerformanceCores++
		idle += 1 - min(max(busiest, 0), 1)
	}
	processors.Free = idle / float64(processors.PerformanceCores)
	if processors.EfficiencyCores > 0 {
		processors.EfficiencyFree = efficiencyIdle / float64(processors.EfficiencyCores)
	}
	return processors, nil
}

// shortfall says how many performance cores are free when that is under the
// mark at which the supervisor starts a goblin by itself, rounded down so a
// reading just under the mark never reads as the mark itself, and is empty
// when they reach it.
func (p Processors) shortfall() string {
	if p.Free >= processorsNext {
		return ""
	}
	kind := "performance cores"
	if p.EfficiencyCores == 0 {
		kind = "cores"
	}
	count := float64(p.PerformanceCores)
	return fmt.Sprintf("Only %.1f of %d %s are free, and a start by itself waits for %.1f", math.Floor(p.Free*count*10)/10, p.PerformanceCores, kind, processorsNext*count)
}
