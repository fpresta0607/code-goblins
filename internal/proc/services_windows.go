package proc

import (
	"fmt"
	"time"
)

// RunningServicesAmong maps each of members, the processes of a job, that is
// a machine service, or runs under one that is among members too, to that
// service. A service outside them makes none of them one: what a job holds
// is the job's, and the gate daemon above an agent's cfo gate prepush is no
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
// machine service each is or runs under. A recorded parent counts only when
// it started before the process, since Windows gives an ended parent's ID to
// the next process.
func ProcessTree(pid int) ([]ServiceProcess, map[int]Service, error) {
	running, err := runningProcesses()
	if err != nil {
		return nil, nil, err
	}
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
	return tree, ServicesOf(running), nil
}

// runningProcesses reads every running process this one may open. Only
// no-mistakes and node processes have their arguments read, since only the
// gate daemon and the Scrawl server are known by them.
func runningProcesses() ([]ServiceProcess, error) {
	processes, err := snapshotProcesses()
	if err != nil {
		return nil, err
	}
	running := make([]ServiceProcess, 0, len(processes))
	for pid, process := range processes {
		start, alive := StartTime(int(pid))
		if !alive {
			continue
		}
		entry := ServiceProcess{PID: int(pid), ParentPID: int(process.parentPID), ExeBase: process.exeBase, Start: start}
		if base := baseNoExe(process.exeBase); base == "no-mistakes" || base == "node" {
			entry.Arguments, _ = Arguments(int(pid))
		}
		running = append(running, entry)
	}
	return running, nil
}
