//go:build !windows

package proc

// WorkingDirectory refuses: reading another process's working directory is
// not ported yet.
func WorkingDirectory(pid int) (string, error) {
	return "", notPorted("reading a process's working directory")
}

// CommandLine refuses: reading another process's command line is not ported
// yet.
func CommandLine(pid int) (string, error) {
	return "", notPorted("reading a process's command line")
}

// Arguments refuses, as CommandLine does.
func Arguments(pid int) ([]string, error) {
	return nil, notPorted("reading a process's arguments")
}

// SplitCommandLine refuses: a Windows command line is split by Windows' own
// rules, which this system does not have.
func SplitCommandLine(line string) ([]string, error) {
	return nil, notPorted("splitting a Windows command line")
}
