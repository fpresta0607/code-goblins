package cleanup

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// fakeClaude is the argument that makes this test binary a stand-in Claude
// Code in a native terminal, showing the screen the next argument names.
const fakeClaude = "cleanup-fake-claude"

// fakeClaudeScreens are Claude Code's screens as a native terminal holds
// them: waiting at its composer, and in a turn.
var fakeClaudeScreens = map[string][]string{
	"idle":    {"> ", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
	"working": {"✽ Reticulating… (3s · esc to interrupt)", "", "> ", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
}

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == fakeClaude {
		fmt.Print("\x1b[2J\x1b[H" + strings.Join(fakeClaudeScreens[os.Args[2]], "\r\n"))
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	if len(os.Args) > 4 && os.Args[1] == lockHolder {
		runLockHolder(os.Args[2:])
		return
	}
	os.Exit(m.Run())
}
