//go:build linux || darwin

package proc

// runningProcesses reads every running process. Only no-mistakes and node
// processes have their arguments read, since only the gate daemon and the
// Scrawl server are known by them.
func runningProcesses() ([]ServiceProcess, error) {
	processes, err := Processes()
	if err != nil {
		return nil, err
	}
	running := make([]ServiceProcess, 0, len(processes))
	for _, process := range processes {
		entry := ServiceProcess{PID: process.PID, ParentPID: process.ParentPID, ExeBase: process.ExeBase, Start: process.Start}
		if process.ExeBase == "no-mistakes" || process.ExeBase == "node" {
			entry.Arguments, _ = Arguments(process.PID)
		}
		running = append(running, entry)
	}
	return running, nil
}
