package execx

import (
	"fmt"
	"strings"
)

// Ended is the error of a run its context ended: the context's error, with
// the processes of its tree (StartTree) that were ended with it.
type Ended struct {
	Err       error
	Processes []EndedProcess
}

// EndedProcess is one process a tree's end ended, by its image's name and
// its pid.
type EndedProcess struct {
	Name string
	PID  int
}

func (e *Ended) Error() string {
	if len(e.Processes) == 0 {
		return e.Err.Error()
	}
	return e.Err.Error() + ", which ended " + Names(e.Processes)
}

func (e *Ended) Unwrap() error { return e.Err }

// Names lists processes ended, each as its name and pid.
func Names(processes []EndedProcess) string {
	names := make([]string, len(processes))
	for index, process := range processes {
		names[index] = fmt.Sprintf("%s pid %d", process.Name, process.PID)
	}
	return strings.Join(names, ", ")
}
