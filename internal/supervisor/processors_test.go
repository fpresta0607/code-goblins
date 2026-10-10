package supervisor

import (
	"math"
	"strings"
	"testing"
	"time"
)

// overlordsProcessor is the Overlord's Intel Core 7 240H: six performance
// cores of two threads each, then four efficiency cores of one.
func overlordsProcessor() []processorCore {
	var cores []processorCore
	for core := range 6 {
		cores = append(cores, processorCore{efficiencyClass: 1, threads: []int{2 * core, 2*core + 1}})
	}
	for thread := 12; thread < 16; thread++ {
		cores = append(cores, processorCore{efficiencyClass: 0, threads: []int{thread}})
	}
	return cores
}

// uniformProcessor is a desktop with cores of one kind, two threads each.
func uniformProcessor(count int) []processorCore {
	var cores []processorCore
	for core := range count {
		cores = append(cores, processorCore{efficiencyClass: 0, threads: []int{2 * core, 2*core + 1}})
	}
	return cores
}

// after is a second reading one second after a first of all zeroes, in which
// each thread was busy for its share of that second.
func after(busy []float64) []processorTime {
	times := make([]processorTime, len(busy))
	for thread, share := range busy {
		times[thread] = processorTime{idle: uint64((1 - share) * 1e7), total: 1e7}
	}
	return times
}

// On 2026-10-09 three programs outside the fleet kept the Overlord's four
// efficiency cores full all day while half his performance cores sat idle, so
// a count of busy threads would have held every start. The free share is of
// the performance cores, the ones his apps run on, and a core counts as busy
// while either of its threads is.
func TestReadProcessorsCountsTheFreeShareOfThePerformanceCores(t *testing.T) {
	for _, test := range []struct {
		name               string
		cores              []processorCore
		busy               []float64
		wantPerformance    int
		wantEfficiency     int
		wantFree           float64
		wantEfficiencyFree float64
	}{
		{
			name:            "efficiency cores full, three performance cores busy on one thread each",
			cores:           overlordsProcessor(),
			busy:            []float64{1, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1},
			wantPerformance: 6, wantEfficiency: 4, wantFree: 0.5,
		},
		{
			name:            "a 16 thread build on every core",
			cores:           overlordsProcessor(),
			busy:            []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
			wantPerformance: 6, wantEfficiency: 4, wantFree: 0,
		},
		{
			name:            "performance cores idle, efficiency cores half busy between them",
			cores:           overlordsProcessor(),
			busy:            []float64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0.5, 0.5, 1, 0},
			wantPerformance: 6, wantEfficiency: 4, wantFree: 1, wantEfficiencyFree: 0.5,
		},
		{
			name:            "cores of one kind are all performance cores",
			cores:           uniformProcessor(8),
			busy:            []float64{1, 0, 0.5, 0.25, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			wantPerformance: 8, wantEfficiency: 0, wantFree: 6.5 / 8,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got, err := readProcessors(test.cores, make([]processorTime, len(test.busy)), after(test.busy))

			// Assert
			if err != nil {
				t.Fatalf("readProcessors: %v", err)
			}
			if got.PerformanceCores != test.wantPerformance || got.EfficiencyCores != test.wantEfficiency {
				t.Errorf("cores = %d performance and %d efficiency, want %d and %d", got.PerformanceCores, got.EfficiencyCores, test.wantPerformance, test.wantEfficiency)
			}
			if math.Abs(got.Free-test.wantFree) > 1e-9 {
				t.Errorf("free share of the performance cores = %v, want %v", got.Free, test.wantFree)
			}
			if math.Abs(got.EfficiencyFree-test.wantEfficiencyFree) > 1e-9 {
				t.Errorf("free share of the efficiency cores = %v, want %v", got.EfficiencyFree, test.wantEfficiencyFree)
			}
		})
	}
}

// Two readings with no time between them, or of a different machine, say
// nothing of how busy it was, and must not read as a machine with room.
func TestReadProcessorsRefusesReadingsThatSayNothing(t *testing.T) {
	for _, test := range []struct {
		name          string
		cores         []processorCore
		before, after []processorTime
	}{
		{"no time passed", uniformProcessor(1), []processorTime{{idle: 5, total: 9}, {idle: 5, total: 9}}, []processorTime{{idle: 5, total: 9}, {idle: 5, total: 9}}},
		{"fewer threads than the cores name", uniformProcessor(2), make([]processorTime, 2), after([]float64{0, 0})},
		{"readings of different lengths", uniformProcessor(1), make([]processorTime, 2), after([]float64{0, 0, 0})},
		{"no cores", nil, make([]processorTime, 2), after([]float64{0, 0})},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got, err := readProcessors(test.cores, test.before, test.after)

			// Assert
			if err == nil {
				t.Fatalf("readProcessors = %+v, want an error", got)
			}
		})
	}
}

// A start waits while under a quarter of the performance cores are free, and
// says how many are and how many it needs.
func TestProcessorsShortfallNamesTheCoresAStartNeeds(t *testing.T) {
	for _, test := range []struct {
		name       string
		processors Processors
		want       string
	}{
		{"none free", Processors{PerformanceCores: 6, EfficiencyCores: 4, Free: 0}, "Only 0.0 of 6 performance cores are free, and a start by itself waits for 1.5"},
		{"just under the mark", Processors{PerformanceCores: 6, EfficiencyCores: 4, Free: 0.24}, "Only 1.4 of 6 performance cores are free, and a start by itself waits for 1.5"},
		{"a hair under the mark never reads as the mark", Processors{PerformanceCores: 6, EfficiencyCores: 4, Free: 0.2499}, "Only 1.4 of 6 performance cores are free, and a start by itself waits for 1.5"},
		{"at the mark", Processors{PerformanceCores: 6, EfficiencyCores: 4, Free: 0.25}, ""},
		{"all free", Processors{PerformanceCores: 6, EfficiencyCores: 4, Free: 1}, ""},
		{"cores of one kind", Processors{PerformanceCores: 8, Free: 0.1}, "Only 0.8 of 8 cores are free, and a start by itself waits for 2.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := test.processors.shortfall()

			// Assert
			if got != test.want {
				t.Errorf("shortfall = %q, want %q", got, test.want)
			}
			if strings.ContainsAny(got, ";—") {
				t.Errorf("shortfall = %q, which the Overlord reads, has a semicolon or an em dash", got)
			}
		})
	}
}

// A reading of how busy the processors were since the last one says little of
// now when the last one is from before a quiet stretch: with nothing queued,
// nobody reads them for hours. Then, as with no earlier reading at all, the
// reader watches for a moment instead. Two callers at one moment get one
// answer.
func TestHowToReadProcessorsWatchesAfreshWhenTheLastReadingIsOld(t *testing.T) {
	for _, test := range []struct {
		name       string
		hasEarlier bool
		age        time.Duration
		want       processorReading
	}{
		{"no earlier reading", false, 0, watchAMoment},
		{"a reading five seconds old", true, 5 * time.Second, repeatLast},
		{"a reading at the least window", true, processorWindow, sinceLast},
		{"the reading of the last minute's check", true, time.Minute, sinceLast},
		{"a reading at the oldest that still counts", true, processorStale, sinceLast},
		{"a reading from before a quiet half hour", true, 30 * time.Minute, watchAMoment},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := howToReadProcessors(test.hasEarlier, test.age)

			// Assert
			if got != test.want {
				t.Errorf("howToReadProcessors = %d, want %d", got, test.want)
			}
		})
	}
}
