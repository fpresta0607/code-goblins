package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// cfo run-request needs a command file, and a request that no supervisor is
// there to take records nothing; the supervisor proves the CFO itself.
func TestRunRequestCommandRefusesBeforeRecordingAnything(t *testing.T) {
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(dir, "command.ps1")
	if err := os.WriteFile(command, []byte("Write-Output hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}
	for name, c := range map[string]struct {
		args []string
		exit int
		says string
	}{
		"no command file":                   {[]string{"--id", "install-tool", "--title", "Install it", "--shell", "powershell"}, 2, "--command-file is required"},
		"a stray argument":                  {[]string{"--id", "install-tool", "--title", "Install it", "--shell", "powershell", "--command-file", command, "extra"}, 2, ""},
		"no supervisor":                     {[]string{"--id", "install-tool", "--title", "Install it", "--shell", "powershell", "--command-file", command}, 1, "the supervisor is not running"},
		"a withdrawal without a reason":     {[]string{"--withdraw", "install-tool"}, 2, "--withdraw needs --reason"},
		"a withdrawal with an item's flags": {[]string{"--withdraw", "install-tool", "--reason", "not needed", "--command-file", command}, 2, "--withdraw takes only --reason"},
		"a reason without a withdrawal":     {[]string{"--id", "install-tool", "--title", "Install it", "--shell", "powershell", "--command-file", command, "--reason", "not needed"}, 2, "--reason goes with --withdraw"},
		"a withdrawal with no supervisor":   {[]string{"--withdraw", "install-tool", "--reason", "not needed"}, 1, "the supervisor is not running"},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runRunRequest(c.args, &stdout, &stderr, runtime); exit != c.exit || !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%s: exit=%d stderr=%q, want %d naming %q", name, exit, stderr.String(), c.exit, c.says)
		}
	}
	for _, sub := range []string{"runs-inbox", "runs"} {
		if entries, err := os.ReadDir(filepath.Join(h.State, sub)); err == nil && len(entries) != 0 {
			t.Fatalf("a refused request left %d entries in %s", len(entries), sub)
		}
	}
}
