package hosttest

import (
	"fmt"
	"io"
	"os"
	"time"
)

// StreamChild is the argument that makes a test binary, whose TestMain runs
// RunStreamChild first, a stand-in Claude Code on stream-json pipes: it
// writes each argument after it as a line and reads its input until it
// closes.
const StreamChild = "hosttest-stream-child"

// ClosesInput, as the first argument after StreamChild, makes the stand-in
// close its input before it writes its lines and then run on reading
// nothing, as a program that stopped reading does, until its host ends it.
const ClosesInput = "hosttest-closes-input"

// RunStreamChild runs this process as the stand-in StreamChild names, and
// exits, when its arguments name one.
func RunStreamChild() {
	if len(os.Args) < 2 || os.Args[1] != StreamChild {
		return
	}
	lines := os.Args[2:]
	isInputClosed := len(lines) > 0 && lines[0] == ClosesInput
	if isInputClosed {
		_ = os.Stdin.Close()
		lines = lines[1:]
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	if isInputClosed {
		time.Sleep(time.Hour)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}
