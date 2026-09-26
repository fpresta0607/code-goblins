package spawn

import (
	"strings"
	"testing"
)

// The environment a native goblin starts from is the one Windows builds for a
// new process of this user from their configuration, whatever this process
// carries: a variable set only here, as a Claude Code session sets its
// markers, is not in it.
func TestTheUsersEnvironmentHoldsNothingOnlyThisProcessCarries(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "only-in-this-process")
	t.Setenv("CFO_ONLY_IN_THIS_PROCESS", "1")

	env, err := (Service{}).userEnvironment()

	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		names[strings.ToUpper(name)] = true
	}
	for _, configured := range []string{"PATH", "SYSTEMROOT", "USERPROFILE"} {
		if !names[configured] {
			t.Errorf("the user's environment lacks %s: %d entries", configured, len(env))
		}
	}
	for _, inherited := range []string{"CLAUDE_CODE_SESSION_ID", "CFO_ONLY_IN_THIS_PROCESS"} {
		if names[inherited] {
			t.Errorf("the user's environment carries %s, which only this process has", inherited)
		}
	}
}
