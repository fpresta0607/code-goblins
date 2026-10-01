package watch

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// HandoverName is the request cfo serve writes in the state directory when it
// starts while a watcher the Stop hook hosts, or a cfo watch, holds the
// watcher lock. On 2026-10-01 an install stopped serve, the CFO's Stop hook
// took the lock in the gap, and every new serve refused to start until the
// hook's window ended, leaving the board down. The watcher yields the lock to
// the process the request names, and no watcher takes the lock while that
// process waits for it.
const HandoverName = "serve.handover"

// HandoverWait bounds how long a watcher holds off taking the lock for a serve
// that asked for it and how long that serve waits for the lock to come free.
var HandoverWait = 30 * time.Second

// handoverPoll is how often a waiting watcher or serve looks again.
const handoverPoll = 100 * time.Millisecond

// handoverRequest names the process asking for the lock by its pid and start
// time, so a request from a serve that has since ended, or a pid reused by
// another program, is never honoured.
type handoverRequest struct {
	PID      int       `json:"pid"`
	Start    time.Time `json:"start"`
	Hostname string    `json:"hostname"`
}

func handoverPath(stateDir string) string {
	return filepath.Join(stateDir, HandoverName)
}

// RequestHandover asks the watcher holding the lock to yield it to this
// process, and returns the function that withdraws the request, which the
// caller runs once it holds the lock or stops waiting for it.
func RequestHandover(stateDir string) (func(), error) {
	start, ok := proc.StartTime(os.Getpid())
	if !ok {
		return nil, errors.New("watch: read this process's start time for a handover request")
	}
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(handoverRequest{PID: os.Getpid(), Start: start.UTC(), Hostname: hostname})
	if err != nil {
		return nil, err
	}
	if err := fsx.AtomicWriteFile(handoverPath(stateDir), data); err != nil {
		return nil, err
	}
	return func() {
		if request, ok := readHandover(stateDir); ok && request.PID == os.Getpid() {
			_ = os.Remove(handoverPath(stateDir))
		}
	}, nil
}

func readHandover(stateDir string) (handoverRequest, bool) {
	data, err := os.ReadFile(handoverPath(stateDir))
	if err != nil {
		return handoverRequest{}, false
	}
	var request handoverRequest
	if json.Unmarshal(data, &request) != nil {
		return handoverRequest{}, false
	}
	return request, true
}

// liveRequester reports whether the process a request names still runs.
func liveRequester(request handoverRequest) bool {
	requester := lock.Info{PID: request.PID, Start: request.Start, Hostname: request.Hostname}
	return requester.VerifiedAlive()
}

// HandoverPending reports whether a live process other than this one has
// asked for the lock: a serve waiting to take over from a watcher.
func HandoverPending(stateDir string) bool {
	request, ok := readHandover(stateDir)
	return ok && request.PID != os.Getpid() && liveRequester(request)
}

// awaitHandover holds off while a live process waits for the lock, up to
// HandoverWait, so a watcher never takes the lock from under a serve that
// asked for it.
func awaitHandover(stateDir string) {
	for deadline := time.Now().Add(HandoverWait); HandoverPending(stateDir) && time.Now().Before(deadline); {
		time.Sleep(handoverPoll)
	}
}
