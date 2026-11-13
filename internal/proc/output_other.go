//go:build !windows

package proc

import "time"

// OutputFile refuses: naming the file a process writes is not ported yet.
func OutputFile(pid int, started time.Time) (string, error) {
	return "", notPorted("naming a process's output file")
}
