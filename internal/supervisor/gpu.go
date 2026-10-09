package supervisor

import (
	"regexp"
	"strconv"
)

// GPU is the machine's graphics adapters and how busy each was over the
// reading.
type GPU struct {
	Adapters []GPUAdapter `json:"adapters"`
}

// GPUAdapter is one graphics adapter: the share of its busiest engine that
// was in use, from 0 to 1, and the program using most of that engine, "" when
// none is or it has ended.
type GPUAdapter struct {
	Name    string  `json:"name"`
	Busy    float64 `json:"busy"`
	Busiest string  `json:"busiest,omitempty"`
}

// gpuAdapter is an adapter as Windows names it, with the number its engines'
// counters carry.
type gpuAdapter struct {
	luid uint64
	name string
}

// gpuEngineUse is the percent of one engine of one adapter that one program
// used, under the name Windows gives that counter.
type gpuEngineUse struct {
	instance string
	percent  float64
}

// gpuEngineName takes apart the name of one program's use of one engine, such
// as pid_7692_luid_0x00000000_0x00014D79_phys_0_eng_0_engtype_3D: the
// program, the two halves of the adapter's number, high then low, and the
// engine.
var gpuEngineName = regexp.MustCompile(`^pid_(\d+)_luid_0x([0-9A-Fa-f]{1,8})_0x([0-9A-Fa-f]{1,8})_phys_\d+_eng_(\d+)_engtype_`)

// readGPU makes of every program's use of every engine how busy each of
// adapters was: an adapter is as busy as its busiest engine, which every
// program using it adds to, never past full, and names the program using
// most of that engine. A use of an adapter not among adapters, as of
// Windows' software renderer, counts for nothing.
func readGPU(adapters []gpuAdapter, uses []gpuEngineUse, program func(pid uint32) string) GPU {
	type engine struct {
		luid   uint64
		number uint64
	}
	type use struct {
		engine engine
		pid    uint32
	}
	engines := map[engine]float64{}
	programs := map[use]float64{}
	for _, one := range uses {
		match := gpuEngineName.FindStringSubmatch(one.instance)
		if match == nil {
			continue
		}
		pid, pidErr := strconv.ParseUint(match[1], 10, 32)
		high, highErr := strconv.ParseUint(match[2], 16, 32)
		low, lowErr := strconv.ParseUint(match[3], 16, 32)
		number, numberErr := strconv.ParseUint(match[4], 10, 32)
		if pidErr != nil || highErr != nil || lowErr != nil || numberErr != nil {
			continue
		}
		at := engine{luid: high<<32 | low, number: number}
		engines[at] += one.percent
		programs[use{engine: at, pid: uint32(pid)}] += one.percent
	}
	gpu := GPU{Adapters: make([]GPUAdapter, 0, len(adapters))}
	for _, adapter := range adapters {
		reading := GPUAdapter{Name: adapter.name}
		busiest, percent := engine{}, 0.0
		for at, used := range engines {
			if at.luid == adapter.luid && used > percent {
				busiest, percent = at, used
			}
		}
		reading.Busy = min(percent, 100) / 100
		most := 0.0
		for one, used := range programs {
			if one.engine == busiest && percent > 0 && used > most {
				most, reading.Busiest = used, program(one.pid)
			}
		}
		gpu.Adapters = append(gpu.Adapters, reading)
	}
	return gpu
}

// Free is the share of the busiest adapter that was not in use.
func (g GPU) Free() float64 {
	busiest := 0.0
	for _, adapter := range g.Adapters {
		busiest = max(busiest, adapter.Busy)
	}
	return 1 - busiest
}
