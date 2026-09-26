package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// cfo deliver needs an ID, a title and a file, and a delivery from a process
// that is not the registered CFO or the task's goblin records nothing.
func TestDeliverCommandRefusesBeforeRecordingAnything(t *testing.T) {
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "plan.pdf")
	if err := os.WriteFile(file, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}
	for name, c := range map[string]struct {
		args []string
		exit int
		says string
	}{
		"no file":               {[]string{"--id", "plan-document-1", "--title", "Plan"}, 2, "--file is required"},
		"no title":              {[]string{"--id", "plan-document-1", "--file", file}, 2, "--title is required"},
		"no ID":                 {[]string{"--title", "Plan", "--file", file}, 2, "--id is required"},
		"a stray argument":      {[]string{"--id", "plan-document-1", "--title", "Plan", "--file", file, "extra"}, 2, ""},
		"a task with no goblin": {[]string{"--id", "plan-document-1", "--title", "Plan", "--file", file, "--task", "g1"}, 1, "no live record"},
		"from no CFO":           {[]string{"--id", "plan-document-1", "--title", "Plan", "--file", file}, 1, "not registered"},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runDeliver(c.args, &stdout, &stderr, runtime); exit != c.exit || !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%s: exit=%d stderr=%q, want %d naming %q", name, exit, stderr.String(), c.exit, c.says)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(h.State, "reviews-inbox")); err == nil && len(entries) != 0 {
		t.Fatalf("a refused delivery left %d inbox records", len(entries))
	}
	if _, err := os.Stat(filepath.Join(h.State, "reviews")); !os.IsNotExist(err) {
		t.Fatalf("a refused delivery copied the document: %v", err)
	}
}
