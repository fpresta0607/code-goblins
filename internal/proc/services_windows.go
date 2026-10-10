package proc

import "fmt"

// RunningServices maps the ID of every running process that is a machine
// service, or runs under one, to that service.
func RunningServices() (map[int]Service, error) {
	running, err := runningProcesses()
	if err != nil {
		return nil, err
	}
	return ServicesOf(running), nil
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
