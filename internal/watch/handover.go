package watch

import (
	"context"
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

// HandoverAckName is the watcher's answer to a serve's request: it names the
// watcher and the serve it is yielding to. A watcher answers as soon as it
// reads the request and stops the cycle it is in, but a monitor scan, an
// orphan sweep or a filing pass can take a while to wind down.
const HandoverAckName = "serve.handover.ack"

// HandoverWait bounds how long a watcher holds off taking the lock for a serve
// that asked for it and how long that serve waits for the lock to come free.
var HandoverWait = 30 * time.Second

// HandoverAckWait bounds how much longer a serve waits, past HandoverWait,
// for a watcher that answered its request to let the lock go, before it ends
// that watcher as one that never answered.
var HandoverAckWait = 5 * time.Minute

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

// handoverAck names the watcher yielding the lock, by pid, start time and
// host, and the serve it yields it to, by pid and start time.
type handoverAck struct {
	WatcherPID      int       `json:"watcher_pid"`
	WatcherStart    time.Time `json:"watcher_start"`
	WatcherHostname string    `json:"watcher_hostname"`
	ServePID        int       `json:"serve_pid"`
	ServeStart      time.Time `json:"serve_start"`
}

func handoverPath(stateDir string) string {
	return filepath.Join(stateDir, HandoverName)
}

func handoverAckPath(stateDir string) string {
	return filepath.Join(stateDir, HandoverAckName)
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
		if ack, ok := readHandoverAck(stateDir); ok && ack.ServePID == os.Getpid() && ack.ServeStart.Equal(start) {
			_ = os.Remove(handoverAckPath(stateDir))
		}
	}, nil
}

// HandoverAcknowledged reports whether holder, the watcher holding the lock,
// answered a serve's request: it reads the request and is yielding the lock,
// only winding its cycle down. Which serve the answer names does not matter:
// two serves asking at once each have their request answered in turn.
func HandoverAcknowledged(stateDir string, holder lock.Info) bool {
	ack, ok := readHandoverAck(stateDir)
	return ok && ack.WatcherPID == holder.PID && ack.WatcherStart.Equal(holder.Start) && ack.WatcherHostname == holder.Hostname
}

func readHandoverAck(stateDir string) (handoverAck, bool) {
	data, err := os.ReadFile(handoverAckPath(stateDir))
	if err != nil {
		return handoverAck{}, false
	}
	var ack handoverAck
	if json.Unmarshal(data, &ack) != nil {
		return handoverAck{}, false
	}
	return ack, true
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
	_, ok := pendingHandover(stateDir)
	return ok
}

func pendingHandover(stateDir string) (handoverRequest, bool) {
	request, ok := readHandover(stateDir)
	return request, ok && request.PID != os.Getpid() && liveRequester(request)
}

// yieldOnRequest answers each live serve's request until released closes,
// once the watcher has let the lock go, and cancels the watcher's cycle, so a
// slow scan, sweep or filing pass gives the lock up at once rather than when
// it ends. A request from a serve the answer does not name, one started at
// the same moment or after another gave up, or a request whose answer is
// gone, is answered again: a serve that finds no answer naming it ends the
// watcher as one from before the handover.
func yieldOnRequest(released <-chan struct{}, cancel context.CancelFunc, stateDir string) {
	start, hasStart := proc.StartTime(os.Getpid())
	hostname, hostErr := os.Hostname()
	ticker := time.NewTicker(handoverPoll)
	defer ticker.Stop()
	for {
		select {
		case <-released:
			return
		case <-ticker.C:
		}
		request, ok := pendingHandover(stateDir)
		if !ok {
			continue
		}
		ack, answered := readHandoverAck(stateDir)
		answered = answered && ack.WatcherPID == os.Getpid() && ack.ServePID == request.PID && ack.ServeStart.Equal(request.Start)
		if !answered && hasStart && hostErr == nil {
			if data, err := json.Marshal(handoverAck{WatcherPID: os.Getpid(), WatcherStart: start, WatcherHostname: hostname, ServePID: request.PID, ServeStart: request.Start}); err == nil {
				_ = fsx.AtomicWriteFile(handoverAckPath(stateDir), data)
			}
		}
		cancel()
	}
}

// awaitHandover holds off while a live process waits for the lock, up to
// HandoverWait, so a watcher never takes the lock from under a serve that
// asked for it.
func awaitHandover(stateDir string) {
	for deadline := time.Now().Add(HandoverWait); HandoverPending(stateDir) && time.Now().Before(deadline); {
		time.Sleep(handoverPoll)
	}
}
