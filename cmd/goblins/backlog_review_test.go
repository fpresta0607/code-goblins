package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/githubtest"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// workStatesAPI answers the states query of acme/widgets as GitHub would: each
// number it holds a state for is an issue in that state, and any other is
// null. It records each request it is made.
type workStatesAPI struct {
	states   map[int]string
	requests []string
}

func (api *workStatesAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	api.requests = append(api.requests, r.Method+" "+r.URL.Path)
	_, variables, err := githubtest.Query(r)
	if err != nil {
		githubtest.Refuse(w, http.StatusBadRequest, err.Error())
		return
	}
	fields := map[string]any{}
	for name, value := range variables {
		if alias, isNumber := strings.CutPrefix(name, "n"); isNumber && name != "name" {
			if state := api.states[int(value.(float64))]; state != "" {
				fields["n"+alias] = map[string]any{"__typename": "Issue", "state": state}
			} else {
				fields["n"+alias] = nil
			}
		}
	}
	githubtest.Answer(w, map[string]any{"repository": fields})
}

// backlogReviewRuntime is a home whose backlog is text and whose one project,
// widgets, goes on acme/widgets, with the API that answers for it.
func backlogReviewRuntime(t *testing.T, backlog string, api *workStatesAPI) commandRuntime {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "widgets", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	h := home.Home{Root: base, Data: filepath.Join(base, "data"), State: filepath.Join(base, "state")}
	writeFixtureFile(t, filepath.Join(h.Data, "backlog.md"), backlog)
	return commandRuntime{
		resolveHome:  func() (home.Home, error) { return h, nil },
		projectsRoot: func() (string, error) { return root, nil },
		repositoryOf: func(context.Context, string) (string, error) { return "acme/widgets", nil },
		githubAPI:    githubtest.Client(api),
	}
}

func TestBacklogReviewPrintsEachKindWithItsEvidenceAndProposal(t *testing.T) {
	// Arrange
	api := &workStatesAPI{states: map[int]string{11: "CLOSED", 30: "OPEN"}}
	runtime := backlogReviewRuntime(t, `# Backlog

## Queued
- [ ] done-row - Ship the list, issue 11 (repo: widgets)
- [ ] his-step - Approve the rollout (repo: widgets, kind: his) blocked-by: until 2025-01-01T00:00Z
- [ ] fold-a - Sort the list, issue 30 (repo: widgets)
- [ ] fold-b - Sort the list again, issue 30 (repo: widgets)

## Parked
- [ ] parked-old - Set aside (repo: widgets)
  2020-01-01 parked for the Overlord's call
`, api)
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"backlog", "review"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q, want 0 and nothing", exit, stderr.String())
	}
	want := `done already: done-row
  evidence: acme/widgets#11 closed
  proposed: mark the row done, with this evidence
overtaken: his-step
  evidence: until 2025-01-01 passed
  proposed: close the step, or say what it still waits on
fold: fold-a, fold-b
  evidence: fold-a and fold-b both name acme/widgets#30
  proposed: fold fold-b into fold-a
stale: parked-old
  evidence: last activity 2020-01-01
  proposed: ask the owner once whether to resume, fold or retire it
`
	if stdout.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", stdout.String(), want)
	}
	if len(api.requests) != 1 || api.requests[0] != "POST /graphql" {
		t.Errorf("requests = %q, want one states query", api.requests)
	}
}

func TestBacklogReviewSaysWhatItCouldNotReadAndWhenThereIsNothing(t *testing.T) {
	tests := []struct {
		name    string
		backlog string
		want    string
	}{
		{"nothing to review", "# Backlog\n\n## Queued\n- [ ] fresh - Sort the list, issue 30 (repo: widgets)\n", "nothing to review\n"},
		{"a project that cannot be found", "# Backlog\n\n## Queued\n- [ ] lost - Find it (repo: missing)\n", "not read: row lost is not read: project missing is not read: "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			runtime := backlogReviewRuntime(t, test.backlog, &workStatesAPI{states: map[int]string{30: "OPEN"}})
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime([]string{"backlog", "review"}, &stdout, &stderr, runtime)

			// Assert
			if exit != 0 || !strings.HasPrefix(stdout.String(), test.want) {
				t.Errorf("exit=%d stdout=%q stderr=%q, want 0 and output starting %q", exit, stdout.String(), stderr.String(), test.want)
			}
		})
	}
}

func TestBacklogReviewRefusesArguments(t *testing.T) {
	// Arrange
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"backlog", "review", "--all"}, &stdout, &stderr, commandRuntime{})

	// Assert
	if exit != 2 || stderr.String() != backlogUsage {
		t.Fatalf("exit=%d stderr=%q, want 2 with the usage", exit, stderr.String())
	}
}
