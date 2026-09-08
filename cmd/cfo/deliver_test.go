package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every goblin reads its brief before it writes a commit, so the brief is
// where a standing authorship rule has to live: a goblin that never sees it
// signs the fleet's history with an author that does not exist.
func TestBriefScaffoldCarriesTheCommitAuthorshipRule(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, "projects", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if exit := runBrief([]string{"t1", "--project", "projects/demo"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("runBrief exit=%d stderr=%s", exit, stderr.String())
	}

	body, err := os.ReadFile(strings.TrimSpace(stdout.String()))
	if err != nil {
		t.Fatal(err)
	}
	brief := string(body)
	for _, want := range []string{
		"## Commits",
		"Never name an AI product",
		"Co-Authored-By",
		"pull request body",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief scaffold is missing %q:\n%s", want, brief)
		}
	}
}
