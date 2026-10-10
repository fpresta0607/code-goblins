package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// stopRequest asks one supervisor, named by its pid, to stop. goblins stop
// writes it because a supervisor started in the background has no terminal
// for a Ctrl-C to reach, and the pid keeps a request meant for a supervisor
// that already ended from stopping the next one. Successor, when a request
// names one, is the process restarting the supervisor, an update or an
// install, which is handed the watcher lock as this supervisor ends.
type stopRequest struct {
	PID       int `json:"pid"`
	Successor int `json:"successor,omitempty"`
}

func stopRequestPath(stateDir string) string {
	return filepath.Join(stateDir, "serve.stop")
}

// RequestStop asks the supervisor running as pid to stop. It shuts down as
// it would on Ctrl-C, within a few seconds, and removes its board record.
func RequestStop(stateDir string, pid int) error {
	return requestStop(stateDir, stopRequest{PID: pid})
}

// RequestStopFor is RequestStop by the process successor, which restarts the
// supervisor: as the supervisor ends it hands successor the watcher lock
// rather than releasing it, so the lock is never free between the two for
// another supervisor or a watcher to take. A supervisor from before this,
// which reads the pid alone, releases the lock as it always did.
func RequestStopFor(stateDir string, pid, successor int) error {
	return requestStop(stateDir, stopRequest{PID: pid, Successor: successor})
}

func requestStop(stateDir string, request stopRequest) error {
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(stopRequestPath(stateDir), data)
}

// clearStopRequest removes any request left from before this supervisor
// started. goblins stop writes one only after reading a pid from the board
// record, which serve writes after Start, so none can be meant for it yet,
// whatever pid it reuses.
func clearStopRequest(stateDir string) {
	_ = os.Remove(stopRequestPath(stateDir))
}

// stopRequested reports whether a stop request names this process, and the
// successor it names, if any. A request naming any other pid is left over
// from a supervisor that already ended, since only one runs at a time, so it
// is removed.
func stopRequested(stateDir string) (successor int, isRequested bool) {
	data, err := fsx.ReadFile(stopRequestPath(stateDir))
	if err != nil {
		return 0, false
	}
	var request stopRequest
	_ = os.Remove(stopRequestPath(stateDir))
	if json.Unmarshal(data, &request) != nil || request.PID != os.Getpid() {
		return 0, false
	}
	return request.Successor, true
}
