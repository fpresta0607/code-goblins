package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/githubtest"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// unownedGitHub answers the listing's reads: what each pull request names,
// and the open issues of acme/widgets.
type unownedGitHub struct {
	pulls  map[string]map[string]any
	issues []map[string]any
	calls  []string
}

func (g *unownedGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.calls = append(g.calls, r.Method+" "+r.URL.RequestURI())
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/graphql":
		_, variables, err := githubtest.Query(r)
		if err != nil {
			githubtest.Refuse(w, http.StatusBadRequest, err.Error())
			return
		}
		data := map[string]any{}
		for name, address := range variables {
			data["pull"+strings.TrimPrefix(name, "url")] = g.pulls[address.(string)]
		}
		githubtest.Answer(w, data)
	case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/issues" && r.URL.Query().Get("page") == "1":
		githubtest.Write(w, http.StatusOK, g.issues)
	default:
		githubtest.Refuse(w, http.StatusNotFound, "Not Found")
	}
}

func openIssue(number int, title string, labels ...string) map[string]any {
	listed := []map[string]any{}
	for _, label := range labels {
		listed = append(listed, map[string]any{"name": label})
	}
	return map[string]any{"number": number, "title": title, "html_url": "https://github.com/acme/widgets/issues/" + strconv.Itoa(number), "state": "open", "labels": listed}
}

func writeFixtureFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// unownedHome is a home with one project, widgets, whose issues go on
// acme/widgets: a live task whose brief names issue 1 and whose pull request
// closes issue 4, a queued row that names issue 2, and a finished task whose
// pull request is part of issue 3.
func unownedHome(t *testing.T, github *unownedGitHub) (commandRuntime, home.Home) {
	t.Helper()
	root := t.TempDir()
	checkout := filepath.Join(root, "widgets")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	h := home.Home{Root: base, Data: filepath.Join(base, "data"), State: filepath.Join(base, "state")}
	if err := os.MkdirAll(filepath.Join(h.Data, "projects", "widgets"), 0o700); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(h.State, "tasktmp", "live-task", "brief.md")
	writeFixtureFile(t, brief, "# Brief live-task\n\n## Task\n\nFix https://github.com/acme/widgets/issues/1.\n")
	if err := state.WriteMeta(state.TaskMetaPath(h.State, "live-task"), map[string]string{"project": checkout, "brief": brief, "backend": "native"}); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, "live-task", "done: PR https://github.com/acme/widgets/pull/51"); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(h.Data, "backlog.md"), "# Backlog\n\n## Queued\n- [ ] queued-task - Widgets: sort the list, issue 2 (repo: widgets)\n\n## Done\n")
	if err := state.WriteOutcome(h.State, state.Outcome{ID: "done-task", Project: checkout, Phase: "done", PR: "https://github.com/acme/widgets/pull/50", Evidence: "recorded pull request"}); err != nil {
		t.Fatal(err)
	}
	return commandRuntime{
		resolveHome:  func() (home.Home, error) { return h, nil },
		projectsRoot: func() (string, error) { return root, nil },
		repositoryOf: func(context.Context, string) (string, error) { return "acme/widgets", nil },
		githubAPI:    githubtest.Client(github),
	}, h
}

func TestIssueUnownedLeavesOutRootsAndIssuesALiveOrQueuedTaskNames(t *testing.T) {
	// Arrange
	github := &unownedGitHub{
		pulls: map[string]map[string]any{
			"https://github.com/acme/widgets/pull/50": {"url": "https://github.com/acme/widgets/pull/50", "body": "Part of #3", "closingIssuesReferences": map[string]any{"nodes": []any{}}},
			"https://github.com/acme/widgets/pull/51": {"url": "https://github.com/acme/widgets/pull/51", "body": "", "closingIssuesReferences": map[string]any{"nodes": []any{map[string]any{"number": 4, "repository": map[string]any{"nameWithOwner": "acme/widgets"}}}}},
		},
		issues: []map[string]any{
			openIssue(1, "Named by the live brief"),
			openIssue(2, "Named by the queued row"),
			openIssue(3, "Named only by a finished pull request"),
			openIssue(4, "Closed by the live pull request"),
			openIssue(5, "A root", "ops-root"),
			openIssue(6, "Named by no task"),
			{"number": 7, "title": "A pull request", "html_url": "https://github.com/acme/widgets/pull/7", "state": "open", "pull_request": map[string]any{}},
		},
	}
	runtime, _ := unownedHome(t, github)
	var stdout, stderr bytes.Buffer

	// Act
	exit := runIssue([]string{"unowned"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q, want 0", exit, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"acme/widgets (widgets): 6 open, 1 ops-root, 3 named by live or queued tasks, 1 named only by finished or parked tasks, 1 named by no task",
		"    #3 Named only by a finished pull request (done-task) https://github.com/acme/widgets/issues/3",
		"    #6 Named by no task https://github.com/acme/widgets/issues/6",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{"#1 ", "#2 ", "#4 ", "#5 ", "#7 "} {
		if strings.Contains(output, unwanted) {
			t.Errorf("output lists %q, which it leaves out:\n%s", unwanted, output)
		}
	}
	for _, call := range github.calls {
		if !strings.HasPrefix(call, "POST /graphql") && !strings.HasPrefix(call, "GET /repos/acme/widgets/issues") {
			t.Errorf("the listing made the request %s, want only reads of pull requests and open issues", call)
		}
	}
}

func TestIssueUnownedSaysWhatItCouldNotRead(t *testing.T) {
	// Arrange
	github := &unownedGitHub{issues: []map[string]any{openIssue(6, "Named by no task")}}
	runtime, _ := unownedHome(t, github)
	var stdout, stderr bytes.Buffer

	// Act
	exit := runIssue([]string{"unowned"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q, want 0", exit, stderr.String())
	}
	if want := "GitHub shows no pull request at 2 of the tasks' addresses"; !strings.Contains(stdout.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, stdout.String())
	}
}

func TestIssueUnownedRefusesUnknownArguments(t *testing.T) {
	// Arrange
	var stdout, stderr bytes.Buffer

	// Act
	exit := runIssue([]string{"unowned", "--all"}, &stdout, &stderr, commandRuntime{})

	// Assert
	if exit != 2 || !strings.Contains(stderr.String(), issueUnownedUsage) {
		t.Fatalf("exit=%d stderr=%q, want 2 with the usage", exit, stderr.String())
	}
}
