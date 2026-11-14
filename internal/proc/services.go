package proc

import (
	"fmt"
	"slices"
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

// PageServers lists the running Scrawl servers: node running lavish-axi's
// server script with its server command.
func PageServers() ([]ServiceProcess, error) {
	running, err := runningProcesses()
	return slices.DeleteFunc(running, func(process ServiceProcess) bool {
		return ServiceOf(process.ExeBase, process.Arguments) != PageServer
	}), err
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

// RunningServicesAmong maps each of members, the processes of a job, that is
// a machine service, or runs under one that is among members too, to that
// service. A service outside them makes none of them one: what a job holds
// is the job's, and the gate daemon above an agent's goblins gate prepush is no
// part of the job a check of it runs in, so it must not keep the check
// from being ended.
func RunningServicesAmong(members []Entry) (map[int]Service, error) {
	running, err := runningProcesses()
	if err != nil {
		return nil, err
	}
	return servicesAmong(running, members), nil
}

// servicesAmong is ServicesOf over the processes of running that are among
// members, each the process that started when its member did.
func servicesAmong(running []ServiceProcess, members []Entry) map[int]Service {
	started := make(map[int]time.Time, len(members))
	for _, member := range members {
		started[member.PID] = member.Start
	}
	var among []ServiceProcess
	for _, process := range running {
		if start, isMember := started[process.PID]; isMember && start.Equal(process.Start) {
			among = append(among, process)
		}
	}
	return ServicesOf(among)
}

// ProcessTree lists pid and every process under it, pid first, with the
// machine service each is or runs under, judged only by a service among the
// tree's own processes, as a job's are. A service above the tree is none of
// its processes': the Scrawl server above a goblins reap --force must not keep
// the tree it was asked to end. A recorded parent counts only when it
// started before the process, since Windows gives an ended parent's ID to
// the next process.
func ProcessTree(pid int) ([]ServiceProcess, map[int]Service, error) {
	running, err := runningProcesses()
	if err != nil {
		return nil, nil, err
	}
	return treeOf(running, pid)
}

// treeOf is ProcessTree over running, the machine's processes as they were
// read.
func treeOf(running []ServiceProcess, pid int) ([]ServiceProcess, map[int]Service, error) {
	byPID := map[int]ServiceProcess{}
	children := map[int][]ServiceProcess{}
	for _, process := range running {
		byPID[process.PID] = process
	}
	for _, process := range running {
		if parent, exists := byPID[process.ParentPID]; exists && process.ParentPID != process.PID && !parent.Start.After(process.Start) {
			children[parent.PID] = append(children[parent.PID], process)
		}
	}
	root, exists := byPID[pid]
	if !exists {
		return nil, nil, fmt.Errorf("proc: process %d is not running", pid)
	}
	tree := []ServiceProcess{root}
	seen := map[int]bool{root.PID: true}
	for index := 0; index < len(tree); index++ {
		for _, child := range children[tree[index].PID] {
			if !seen[child.PID] {
				seen[child.PID] = true
				tree = append(tree, child)
			}
		}
	}
	members := make([]Entry, 0, len(tree))
	for _, process := range tree {
		members = append(members, Entry{PID: process.PID, Start: process.Start})
	}
	return tree, servicesAmong(running, members), nil
}
