package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/githubtest"
)

// Issue 2724: goblins pr merge lands a pull request of a project whose
// record says its pull requests land held only once it carried the hold
// label, read from its labels and their history; a record that cannot be read
// merges nothing.
func TestPRMergeRefusesAHeldProjectsPullRequestThatNeverCarriedHold(t *testing.T) {
	const held = `{"project": "r", "delivery": {"pull_requests_land_held": true}}`
	for name, test := range map[string]struct {
		record           string
		labels, labelled []string
		exit             int
		says             string
	}{
		"one that never carried hold":            {record: held, exit: 1, says: "gh pr edit " + queuedPRURL + " --add-label hold"},
		"one that carries hold":                  {record: held, labels: []string{"hold"}, labelled: []string{"hold"}},
		"one its owner took hold off":            {record: held, labelled: []string{"docs", "Hold"}},
		"one in a project that does not ask":     {record: `{"project": "r"}`},
		"one in a repository with no record":     {},
		"one whose project record is unreadable": {record: `{"project": "r", "delivery": {"pull_requests_land_held": "yes"}}`, exit: 1, says: "project manifest"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			data := t.TempDir()
			if test.record != "" {
				dir := filepath.Join(data, "projects", "r")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "project.json"), []byte(test.record), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runner := &prMergeRunner{labels: test.labels, labelled: test.labelled}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runPRMerge([]string{queuedPRURL}, &stdout, &stderr, runner, githubtest.Client(runner), nil, data)

			// Assert
			calls := mergeCalls(runner)
			if exit != test.exit || (len(calls) == 0) != (test.exit != 0) {
				t.Fatalf("exit=%d gh pr merge calls %q, want exit %d; stderr=%s", exit, calls, test.exit, stderr.String())
			}
			if !strings.Contains(stderr.String(), test.says) {
				t.Errorf("stderr %q does not say %q", stderr.String(), test.says)
			}
		})
	}
}
