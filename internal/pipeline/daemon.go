package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// DaemonState is whether the gate daemon runs, as its pid file and process
// show it.
type DaemonState string

const (
	DaemonAlive   DaemonState = "alive"
	DaemonStopped DaemonState = "stopped"
	// DaemonUnknown is a pid file or process that could not be read, which
	// is never taken for a stopped daemon.
	DaemonUnknown DaemonState = "unknown"
)

// Daemon is the gate daemon as this machine shows it, never as a reply
// deadline does: a daemon serving runs on a loaded machine misses a short
// reply while it is alive and working.
type Daemon struct {
	State DaemonState
	PID   int
	// LiveRuns is how many runs the daemon serves, and -1 when its database
	// could not be read.
	LiveRuns int
}

// Daemon reads the daemon's pid file, the process it names and the runs its
// database holds open. It sends the daemon nothing, so it adds no load to a
// busy one.
func (r Reader) Daemon(ctx context.Context) (Daemon, error) {
	data, err := fsx.ReadFileContext(ctx, filepath.Join(r.Root, "daemon.pid"))
	if errors.Is(err, os.ErrNotExist) {
		return Daemon{State: DaemonStopped}, nil
	}
	if err != nil {
		return Daemon{State: DaemonUnknown}, err
	}
	var record struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(data, &record); err != nil || record.PID <= 0 {
		return Daemon{State: DaemonUnknown}, errors.New("pipeline: the gate daemon's pid file names no process")
	}
	daemon := Daemon{PID: record.PID, LiveRuns: -1}
	switch readAgentProcessStatus(record.PID) {
	case agentProcessAlive:
		daemon.State = DaemonAlive
	case agentProcessDead:
		daemon.State = DaemonStopped
		return daemon, nil
	default:
		daemon.State = DaemonUnknown
	}
	runs, err := r.activeRuns(ctx)
	if err != nil {
		return daemon, err
	}
	daemon.LiveRuns = len(runs)
	return daemon, nil
}

// daemonHealthMissed is no-mistakes' refusal when the daemon missed the
// short health reply it is asked before any request, so the request it
// guards never reached the daemon and repeating it changes nothing twice.
const daemonHealthMissed = "start daemon: read response: daemon health did not reply within"

// IsDaemonHealthMissed reports whether a native command stopped at the
// health reply before its request, the one reply miss safe to repeat.
func IsDaemonHealthMissed(result execx.Result) bool {
	return slices.ContainsFunc(nativeErrors(result), func(message string) bool {
		return strings.HasPrefix(message, daemonHealthMissed)
	})
}

// IsDaemonReplyMissed reports whether a native command stopped because the
// daemon missed a reply deadline, before or after its request.
func IsDaemonReplyMissed(result execx.Result) bool {
	return slices.ContainsFunc(nativeErrors(result), func(message string) bool {
		return strings.Contains(message, "read response: daemon ") && strings.Contains(message, " did not reply within ")
	})
}

// nativeErrors is what a failed native command gave as its own error: the
// error field of the document it prints on stdout, then the same line on
// stderr, never a phrase a document quotes elsewhere.
func nativeErrors(result execx.Result) []string {
	if result.ExitCode == 0 {
		return nil
	}
	var messages []string
	for _, line := range strings.Split(string(result.Stdout)+"\n"+string(result.Stderr), "\n") {
		if message, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "error: "); ok {
			messages = append(messages, strings.TrimPrefix(message, `"`))
		}
	}
	return messages
}
