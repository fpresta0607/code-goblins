//go:build !windows

package voice

import "os"

func killWorker(process *os.Process) error {
	return process.Kill()
}
