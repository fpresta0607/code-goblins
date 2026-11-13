package nativehook

import (
	"strings"
	"testing"
)

// A path that would need quoting cannot be written in an unquoted hook
// command, so no Codex launch is built with hooks that would not run.
func TestCodexArgumentsRefuseAPathThatNeedsQuoting(t *testing.T) {
	for name, arguments := range map[string]func(executable, home, state string) ([]string, error){"the CEO's": CodexArguments, "a goblin's": CodexGoblinArguments} {
		t.Run(name, func(t *testing.T) {
			_, err := arguments(`C:\Program Files\ceo\goblins.exe`, `C:\dev\code-goblins`, `C:\dev\code-goblins\state`)

			if err == nil || !strings.Contains(err.Error(), "without quoting") {
				t.Fatalf("err = %v, want the path that needs quoting refused", err)
			}
		})
	}
}
