package services

import (
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// Desktop is the Windows desktop as an engine start sees it: the processes
// running, the arguments one was started with, and the windows one shows.
type Desktop struct {
	Processes    func() ([]proc.Entry, error)
	Arguments    func(pid int) ([]string, error)
	CloseWindows func(pid int) (int, error)
	// Every is how often a start looks for the dashboard window, and Grace
	// how long after the engine answers it keeps looking.
	Every time.Duration
	Grace time.Duration
}

// WindowsDesktop is this machine's desktop.
var WindowsDesktop = Desktop{
	Processes:    proc.Processes,
	Arguments:    proc.Arguments,
	CloseWindows: proc.CloseWindows,
	Every:        50 * time.Millisecond,
	Grace:        30 * time.Second,
}
