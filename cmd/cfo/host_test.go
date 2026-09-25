package main

import (
	"bytes"
	"strings"
	"testing"
)

// cfo host needs a state directory and a command to run, and starts nothing
// without them.
func TestHostRefusesWithoutAStateDirectoryOrCommand(t *testing.T) {
	for name, args := range map[string][]string{
		"no state directory": {"host", "--id", "g1", "--", "cmd.exe"},
		"no command":         {"host", "--state", t.TempDir(), "--id", "g1"},
	} {
		var stdout, stderr bytes.Buffer

		exit := runWithRuntime(args, &stdout, &stderr, commandRuntime{})

		if exit != 1 || !strings.Contains(stderr.String(), "--state and a command after -- are required") {
			t.Errorf("%s: exit=%d stderr=%q, want the refusal", name, exit, stderr.String())
		}
	}
}
