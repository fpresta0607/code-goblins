//go:build !windows

package proc

// ProcessTree refuses: reading a process tree is not ported yet.
func ProcessTree(pid int) ([]ServiceProcess, map[int]Service, error) {
	return nil, nil, notPorted("reading a process tree")
}
