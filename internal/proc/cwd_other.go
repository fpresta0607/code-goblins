//go:build !windows

package proc

// CommandLine refuses: reading another process's command line is not ported
// yet.
func CommandLine(pid int) (string, error) {
	return "", notPorted("reading a process's command line")
}

// SplitCommandLine refuses: a Windows command line is split by Windows' own
// rules, which this system does not have.
func SplitCommandLine(line string) ([]string, error) {
	return nil, notPorted("splitting a Windows command line")
}
