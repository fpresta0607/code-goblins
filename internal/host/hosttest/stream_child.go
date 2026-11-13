package hosttest

import (
	"fmt"
	"io"
	"os"
)

// StreamChild is the argument that makes a test binary, whose TestMain runs
// RunStreamChild first, a stand-in Claude Code on stream-json pipes: it
// writes each argument after it as a line and reads its input until it
// closes.
const StreamChild = "hosttest-stream-child"

// RunStreamChild runs this process as the stand-in StreamChild names, and
// exits, when its arguments name one.
func RunStreamChild() {
	if len(os.Args) < 2 || os.Args[1] != StreamChild {
		return
	}
	for _, line := range os.Args[2:] {
		fmt.Println(line)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}
