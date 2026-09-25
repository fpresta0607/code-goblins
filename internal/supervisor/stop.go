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
// that already ended from stopping the next one.
type stopRequest struct {
	PID int `json:"pid"`
}

func stopRequestPath(stateDir string) string {
	return filepath.Join(stateDir, "serve.stop")
}

// RequestStop asks the supervisor running as pid to stop. It shuts down as
// it would on Ctrl-C, within a few seconds, and removes its board record.
func RequestStop(stateDir string, pid int) error {
	data, err := json.Marshal(stopRequest{PID: pid})
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

// stopRequested reports whether a stop request names this process. A request
// naming any other pid is left over from a supervisor that already ended,
// since only one runs at a time, so it is removed.
func stopRequested(stateDir string) bool {
	data, err := os.ReadFile(stopRequestPath(stateDir))
	if err != nil {
		return false
	}
	var request stopRequest
	_ = os.Remove(stopRequestPath(stateDir))
	return json.Unmarshal(data, &request) == nil && request.PID == os.Getpid()
}
