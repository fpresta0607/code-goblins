package proc

import (
	"strings"
	"time"
)

// Service is a machine-wide service a goblin may start for its work but that
// serves the whole machine, so a goblin's teardown never ends it: Docker
// Desktop with everything it runs, its engine's WSL processes included, the
// no-mistakes daemon every gate shares, with the gate agents it runs, and
// the Scrawl server that keeps every goblin's review page.
type Service int

const (
	NoService Service = iota
	DockerDesktop
	GateDaemon
	PageServer
)

// pageServerScripts are the scripts lavish-axi runs its server from, as an
// installed package and from its source.
var pageServerScripts = []string{"/lavish-axi/dist/server.mjs", "/lavish-axi/bin/lavish-axi-server.js"}

// ServiceOf names the machine service a program is, by its executable and
// arguments: Docker Desktop and its com.docker.* processes, no-mistakes
// running its daemon command, and node running lavish-axi's server script
// with its server command.
func ServiceOf(exeBase string, arguments []string) Service {
	name := strings.TrimSuffix(strings.ToLower(exeBase), ".exe")
	switch {
	case name == "docker desktop" || strings.HasPrefix(name, "com.docker."):
		return DockerDesktop
	case name == "no-mistakes" && len(arguments) > 1 && strings.EqualFold(arguments[1], "daemon"):
		return GateDaemon
	case name == "node" && len(arguments) > 2 && arguments[2] == "server":
		script := strings.ToLower(strings.ReplaceAll(arguments[1], `\`, "/"))
		for _, known := range pageServerScripts {
			if strings.HasSuffix(script, known) {
				return PageServer
			}
		}
	}
	return NoService
}

// ServiceProcess is a process as ServicesOf reads it.
type ServiceProcess struct {
	PID       int
	ParentPID int
	ExeBase   string
	Arguments []string
	Start     time.Time
}

// ServicesOf maps the ID of each process that is a machine service, or runs
// under one, to that service. A recorded parent counts only when it started
// before the process, since Windows gives an ended parent's ID to the next
// process.
func ServicesOf(processes []ServiceProcess) map[int]Service {
	byPID := make(map[int]ServiceProcess, len(processes))
	for _, process := range processes {
		byPID[process.PID] = process
	}
	services := map[int]Service{}
	for _, process := range processes {
		chain := []int{}
		service := NoService
		for current, seen := process, map[int]bool{}; !seen[current.PID]; {
			seen[current.PID] = true
			if known, isKnown := services[current.PID]; isKnown {
				service = known
				break
			}
			chain = append(chain, current.PID)
			if service = ServiceOf(current.ExeBase, current.Arguments); service != NoService {
				break
			}
			parent, exists := byPID[current.ParentPID]
			if !exists || current.ParentPID == current.PID || parent.Start.After(current.Start) {
				break
			}
			current = parent
		}
		for _, pid := range chain {
			services[pid] = service
		}
	}
	for pid, service := range services {
		if service == NoService {
			delete(services, pid)
		}
	}
	return services
}
