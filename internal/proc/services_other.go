//go:build !windows

package proc

// RunningServicesAmong refuses: finding the machine's services among
// processes is not ported yet.
func RunningServicesAmong(members []Entry) (map[int]Service, error) {
	return nil, notPorted("finding the machine's services")
}

// ProcessTree refuses: reading a process tree is not ported yet.
func ProcessTree(pid int) ([]ServiceProcess, map[int]Service, error) {
	return nil, nil, notPorted("reading a process tree")
}
