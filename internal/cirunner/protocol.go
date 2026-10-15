package cirunner

import "errors"

// PipeName is where the service answers the fleet.
const PipeName = `\\.\pipe\code-goblins-ci-runner`

// The kinds of request the service answers: the job that waits, a run of one
// job for as long as the asking client stays connected, and what runs now.
const (
	AskPending = "pending"
	AskRun     = "run"
	AskStatus  = "status"
)

// The kinds of reply. A run's progress lines come before its one done
// reply, and every other request has one reply of its own kind, or an error.
const (
	ReplyPending  = "pending"
	ReplyProgress = "progress"
	ReplyDone     = "done"
	ReplyStatus   = "status"
	ReplyError    = "error"
)

// Request is one line a client sends the service, as JSON.
type Request struct {
	Kind string `json:"kind"`
}

// Reply is one line the service sends a client, as JSON.
type Reply struct {
	Kind    string   `json:"kind"`
	Pending *Pending `json:"pending,omitempty"`
	Line    string   `json:"line,omitempty"`
	Outcome Outcome  `json:"outcome,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// ErrNotServing is a pipe no service answers on.
var ErrNotServing = errors.New("cirunner: the CI runner service is not running")
