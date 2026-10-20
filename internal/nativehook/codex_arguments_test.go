package nativehook

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// A Codex session the fleet starts takes its native hooks as -c overrides of
// its own configuration, one for every event Install hooks, each running cfo
// directly with no quote cmd or PowerShell would read, and with the hooks
// feature on; nothing is written to the user's hooks.json.
func TestCodexArgumentsGiveOneSessionItsNativeHooks(t *testing.T) {
	// Arrange
	executable, home, state := `C:\dev\code-goblins\bin\cfo.exe`, `C:\dev\code-goblins`, `C:\dev\code-goblins\state`

	// Act
	args, err := CodexArguments(executable, home, state)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args[:2], []string{"--enable", "hooks"}) {
		t.Fatalf("args start %q, want the hooks feature on", args[:2])
	}
	command := "C:/dev/code-goblins/bin/cfo.exe native-hook codex --home C:/dev/code-goblins --state C:/dev/code-goblins/state"
	var events []string
	for i := 2; i < len(args); i += 2 {
		event, value, found := strings.Cut(strings.TrimPrefix(args[i+1], "hooks."), "=")
		if args[i] != "-c" || !found || value != "[{hooks=[{type='command',command='"+command+"',timeout="+strconv.Itoa(TimeoutSeconds)+"}]}]" {
			t.Errorf("args %q %q, want a -c override running %q", args[i], args[i+1], command)
		}
		if strings.ContainsAny(args[i+1], "\"&|<>^%!()") {
			t.Errorf("override %q holds a character cmd would read", args[i+1])
		}
		events = append(events, event)
	}
	if !slices.Equal(events, codexEvents) {
		t.Fatalf("events = %q, want %q", events, codexEvents)
	}
}

// A path that would need quoting cannot be written in an unquoted hook
// command, so no Codex launch is built with hooks that would not run.
func TestCodexArgumentsRefuseAPathThatNeedsQuoting(t *testing.T) {
	_, err := CodexArguments(`C:\Program Files\cfo\cfo.exe`, `C:\dev\code-goblins`, `C:\dev\code-goblins\state`)

	if err == nil || !strings.Contains(err.Error(), "without quoting") {
		t.Fatalf("err = %v, want the path that needs quoting refused", err)
	}
}
