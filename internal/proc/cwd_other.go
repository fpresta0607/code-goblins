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

// Parameters refuses, as CommandLine does.
func Parameters(pid int) (string, []string, error) {
	return "", nil, notPorted("reading a process's working directory and arguments")
}

// ParametersAndEnvironment refuses, as CommandLine does.
func ParametersAndEnvironment(pid int) (string, []string, []string, error) {
	return "", nil, nil, notPorted("reading a process's working directory, arguments and environment")
}

// SplitCommandLine refuses: a Windows command line is split by Windows' own
// rules, which this system does not have.
func SplitCommandLine(line string) ([]string, error) {
	return nil, notPorted("splitting a Windows command line")
}
