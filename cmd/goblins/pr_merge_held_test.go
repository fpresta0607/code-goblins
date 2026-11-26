package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/githubtest"
	"github.com/fpresta0607/code-goblins/internal/home"
)

const (
	heldRecord    = `{"project": "r", "delivery": {"pull_requests_land_held": true}}`
	notHeldRecord = `{"project": "r"}`
)

// heldCheckout is a checkout under the projects root: its origin, and the
// record the home holds for it, none when empty.
type heldCheckout struct {
	origin, record string
}

// heldProjects lays out a projects root holding checkouts, each a folder with
// a .git, and a home data directory holding their records. It returns the data
// directory, the root and the origins by checkout folder, as git reads them.
func heldProjects(t *testing.T, checkouts map[string]heldCheckout) (data, root string, origins map[string]string) {
	t.Helper()
	data, root, origins = t.TempDir(), t.TempDir(), map[string]string{}
	for folder, checkout := range checkouts {
		dir := filepath.Join(root, folder)
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		origins[dir] = checkout.origin
		if checkout.record == "" {
			continue
		}
		records := filepath.Join(data, "projects", folder)
		if err := os.MkdirAll(records, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(records, "project.json"), []byte(checkout.record), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return data, root, origins
}

// Issue 2724: goblins pr merge lands a pull request of a project whose
// record says its pull requests land held only once it carried the hold
// label, read from its labels and their history; a record that cannot be read
// merges nothing. The record is that of each checkout whose origin is the pull
// request's repository, however its folder is named and its origin written.
func TestPRMergeRefusesAHeldProjectsPullRequestThatNeverCarriedHold(t *testing.T) {
	const origin = "https://github.com/o/r.git"
	for name, test := range map[string]struct {
		checkouts        map[string]heldCheckout
		labels, labelled []string
		exit             int
		says             string
	}{
		"one that never carried hold":               {checkouts: map[string]heldCheckout{"r": {origin, heldRecord}}, exit: 1, says: "gh pr edit " + queuedPRURL + " --add-label hold"},
		"one that carries hold":                     {checkouts: map[string]heldCheckout{"r": {origin, heldRecord}}, labels: []string{"hold"}, labelled: []string{"hold"}},
		"one its owner took hold off":               {checkouts: map[string]heldCheckout{"r": {origin, heldRecord}}, labelled: []string{"docs", "Hold"}},
		"one in a project that does not ask":        {checkouts: map[string]heldCheckout{"r": {origin, notHeldRecord}}},
		"one whose checkout has no record":          {checkouts: map[string]heldCheckout{"r": {origin: origin}}},
		"one with no checkout of its repository":    {checkouts: map[string]heldCheckout{"r": {"https://github.com/o/other.git", heldRecord}}},
		"one whose project record is unreadable":    {checkouts: map[string]heldCheckout{"r": {origin, `{"project": "r", "delivery": {"pull_requests_land_held": "yes"}}`}}, exit: 1, says: "project manifest"},
		"one whose checkout is named unlike it":     {checkouts: map[string]heldCheckout{"widgets": {"git@github.com:O/R", heldRecord}}, exit: 1, says: "gh pr edit " + queuedPRURL + " --add-label hold"},
		"one of whose two checkouts' records holds": {checkouts: map[string]heldCheckout{"r": {origin, notHeldRecord}, "r-second": {"https://github.com/O/r", heldRecord}}, exit: 1, says: "gh pr edit " + queuedPRURL + " --add-label hold"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			data, root, origins := heldProjects(t, test.checkouts)
			runner := &prMergeRunner{labels: test.labels, labelled: test.labelled, origins: origins}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runPRMerge([]string{queuedPRURL}, &stdout, &stderr, runner, githubtest.Client(runner), nil, data, root)

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

// goblins pr merge reads the held declaration from the records of the home it
// runs in and the checkouts of the machine's projects root.
func TestPRMergeCommandRefusesAHeldProjectsPullRequestThatNeverCarriedHold(t *testing.T) {
	// Arrange
	data, root, origins := heldProjects(t, map[string]heldCheckout{"r": {"https://github.com/o/r.git", heldRecord}})
	runner := &prMergeRunner{localOid: prMergeHeadOID, origins: origins}
	runtime := commandRuntime{
		githubAPI:    githubtest.Client(runner),
		resolveHome:  func() (home.Home, error) { return home.Home{Data: data, State: t.TempDir()}, nil },
		projectsRoot: func() (string, error) { return root, nil },
	}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runPR("merge", []string{queuedPRURL}, &stdout, &stderr, runner, runtime)

	// Assert
	if calls := mergeCalls(runner); exit != 1 || len(calls) != 0 {
		t.Fatalf("exit=%d gh pr merge calls %q, want exit 1 and no merge; stderr=%s", exit, calls, stderr.String())
	}
	if want := "gh pr edit " + queuedPRURL + " --add-label hold"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr %q does not say %q", stderr.String(), want)
	}
}
