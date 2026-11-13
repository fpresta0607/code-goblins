package proc

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
