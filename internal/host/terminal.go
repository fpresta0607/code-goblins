package host

import (
	"errors"
	"strings"
)

// IDVariable tells the program in a host's terminal which terminal it runs
// in, so it can register as reachable through that host.
const IDVariable = "GOBLINS_HOST_ID"

// ProofVariable carries a value the host makes for its terminal alone, which
// every process in the terminal inherits and the host's record keeps only as
// a digest, so a process can prove it runs in the terminal when its chain of
// parents stops short of the program.
const ProofVariable = "GOBLINS_HOST_PROOF"

// Spec is one terminal for a host to run.
type Spec struct {
	ID string
	// Args is the command and its arguments; the caller resolves Args[0] to
	// a program CreateProcess can run.
	Args       []string
	Dir        string
	Cols, Rows int
	// ShouldLeaveCoresForApps keeps the terminal's process and everything it
	// starts off the performance cores the fleet's work leaves to the
	// Overlord's own apps (processor.MachineFleetThreads).
	ShouldLeaveCoresForApps bool
	// IsStream runs the command on pipes rather than in a terminal, for a
	// program that speaks one JSON object a line both ways, as Claude Code
	// does with stream-json; Cols and Rows are not used.
	IsStream bool
}

// Event is what the host sent: terminal output, the terminal's size from
// this point in its output on, whether key presses sit unread in its input,
// or the terminal's end with its exit code.
type Event struct {
	Output []byte
	// Cols and Rows are set for a size.
	Cols, Rows int
	// Unread is set when the host says key presses have sat unread in the
	// terminal's input, true, or that its program has read them, false.
	Unread *bool
	Exited bool
	Code   uint32
}

// ErrNoTurnEvidence requires a terminal restart before the board accepts
// input whose resulting turn it must distinguish from a command's picker.
var ErrNoTurnEvidence = errors.New("Restart this terminal to enable Chat turn tracking.")

// ErrNoDelivery is a host that cannot confirm what it types: one started by an
// older ceo. Nothing was typed into its terminal.
var ErrNoDelivery = errors.New("host: this terminal's host was started by an older goblins and cannot confirm what it types; nothing was typed")

// StreamEvent is a line of the session, or its program's end with its exit
// code.
type StreamEvent struct {
	Line     StreamLine
	IsExited bool
	Code     uint32
}

// ScreenTail is a screen's last lines rows, or all of them for lines 0 or
// less, without the trailing blanks of each row or the blank rows below the
// last one written.
func ScreenTail(screen []string, lines int) string {
	rows := make([]string, len(screen))
	for i, row := range screen {
		rows[i] = strings.TrimRight(row, " ")
	}
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	if lines > 0 && lines < len(rows) {
		rows = rows[len(rows)-lines:]
	}
	if len(rows) == 0 {
		return ""
	}
	return strings.Join(rows, "\n") + "\n"
}
