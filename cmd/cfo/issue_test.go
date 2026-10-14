package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// issueGH answers the gh api requests cfo issue file makes the way GitHub
// would. private names the repositories that are not public; open is the
// JSON listing of the open ops-root issues. Every write is recorded.
type issueGH struct {
	private map[string]bool
	open    string
	writes  []string
}

func (f *issueGH) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	method, path := "GET", ""
	for i := 1; i < len(req.Args); i++ {
		switch req.Args[i] {
		case "--method":
			i++
			method = req.Args[i]
		case "-f":
			i++
		default:
			path = req.Args[i]
		}
	}
	switch {
	case method == "GET" && strings.Contains(path, "/issues?labels=ops-root"):
		open := f.open
		if open == "" {
			open = "[]"
		}
		return execx.Result{Stdout: []byte(open)}, nil
	case method == "GET" && strings.HasPrefix(path, "repos/"):
		if f.private[strings.TrimPrefix(path, "repos/")] {
			return execx.Result{Stdout: []byte(`{"private":true}`)}, nil
		}
		return execx.Result{Stdout: []byte(`{"private":false}`)}, nil
	case method == "POST":
		f.writes = append(f.writes, path)
		if strings.HasSuffix(path, "/issues") {
			return execx.Result{Stdout: []byte(`{"number":42,"html_url":"https://github.com/acme/widgets/issues/42"}`)}, nil
		}
		return execx.Result{Stdout: []byte(`{}`)}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("gh: Not Found (HTTP 404)")}, nil
}

// issueRuntime is a home whose projects root holds widgets, the project an
// issue is filed for, and secret-project, a private one, each a checkout
// whose origin is acme/<its name>.
func issueRuntime(t *testing.T, gh *issueGH) (commandRuntime, string) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"widgets", "secret-project"} {
		if err := os.MkdirAll(filepath.Join(root, name, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	h := home.Home{Data: filepath.Join(t.TempDir(), "data"), State: filepath.Join(t.TempDir(), "state")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	return commandRuntime{
		resolveHome:  func() (home.Home, error) { return h, nil },
		projectsRoot: func() (string, error) { return root, nil },
		repositoryOf: func(_ context.Context, checkout string) (string, error) {
			return "acme/" + filepath.Base(checkout), nil
		},
		github: gh,
	}, h.State
}

func writeBody(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIssueFileFilesANewRootOnTheProjectsRepository(t *testing.T) {
	// Arrange
	gh := &issueGH{}
	runtime, _ := issueRuntime(t, gh)
	var stdout, stderr strings.Builder

	// Act
	code := runIssue([]string{"file", "--project", "widgets", "--title", "conpty test fails under load", "--body-file", writeBody(t, "## What happens\nIt fails."), "--root", "test:internal/conpty TestX", "--evidence", "run 7"}, &stdout, &stderr, runtime)

	// Assert
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if want := "filed #42 https://github.com/acme/widgets/issues/42 with the ops-root label"; !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if strings.Join(gh.writes, " ") != "repos/acme/widgets/labels repos/acme/widgets/issues" {
		t.Fatalf("writes = %v", gh.writes)
	}
}

func TestIssueFileCommentsOnTheOpenIssueOfTheSameRoot(t *testing.T) {
	// Arrange
	gh := &issueGH{open: `[{"number":7,"title":"conpty test fails under load","html_url":"https://github.com/acme/widgets/issues/7","body":"filed by hand"}]`}
	runtime, _ := issueRuntime(t, gh)
	var stdout, stderr strings.Builder

	// Act
	code := runIssue([]string{"file", "--project", "widgets", "--title", "conpty test fails under load", "--evidence", "run 8"}, &stdout, &stderr, runtime)

	// Assert
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "commented on #7") || strings.Join(gh.writes, " ") != "repos/acme/widgets/issues/7/comments" {
		t.Fatalf("stdout = %q, writes = %v, want one comment on 7", stdout.String(), gh.writes)
	}
}

func TestIssueFileRefusesWhatAPublicIssueMayNotSay(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"a private project", "Seen first in secret-project.", "it names secret-project, whose repository is not public"},
		{"a teammate", "Jane Doe saw it first.", "it names Jane Doe, a teammate"},
		{"a local path", "The log is at C:\\dev\\widgets\\out.log.", "it names a path on this machine"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &issueGH{private: map[string]bool{"acme/secret-project": true}}
			runtime, stateDir := issueRuntime(t, gh)
			if err := tickets.RememberTeammates(stateDir, []tickets.Actor{{Login: "jdoe", Name: "Jane Doe"}}); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr strings.Builder

			// Act
			code := runIssue([]string{"file", "--project", "widgets", "--title", "a root", "--body-file", writeBody(t, tc.body)}, &stdout, &stderr, runtime)

			// Assert
			if code != 1 || !strings.Contains(stderr.String(), "refused: the issue would go on acme/widgets, which is public, and "+tc.want) {
				t.Fatalf("exit = %d, stderr = %q, want a refusal saying %q", code, stderr.String(), tc.want)
			}
			if len(gh.writes) != 0 {
				t.Fatalf("writes = %v, want none", gh.writes)
			}
		})
	}
}

func TestIssueFileNamesAPublicProjectAndAPrivateRepositorysOwnText(t *testing.T) {
	cases := []struct {
		name    string
		private map[string]bool
		body    string
	}{
		{"another project whose repository is public", nil, "Seen first in secret-project."},
		{"a private repository's own issue", map[string]bool{"acme/widgets": true}, "Seen in secret-project at C:\\dev\\widgets."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &issueGH{private: tc.private}
			runtime, _ := issueRuntime(t, gh)
			var stdout, stderr strings.Builder

			// Act
			code := runIssue([]string{"file", "--project", "widgets", "--title", "a root", "--body-file", writeBody(t, tc.body)}, &stdout, &stderr, runtime)

			// Assert
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
			}
		})
	}
}

func TestIssueFileRefusesACredentialValueThisTerminalCarries(t *testing.T) {
	// Arrange
	t.Setenv("WIDGETS_API_TOKEN", "made-up-value-0451")
	gh := &issueGH{private: map[string]bool{"acme/widgets": true}}
	runtime, _ := issueRuntime(t, gh)
	var stdout, stderr strings.Builder

	// Act
	code := runIssue([]string{"file", "--project", "widgets", "--title", "a root", "--evidence", "the call sent made-up-value-0451"}, &stdout, &stderr, runtime)

	// Assert
	if code != 1 || !strings.Contains(stderr.String(), "it holds a credential value this terminal carries") || strings.Contains(stderr.String(), "made-up-value-0451") {
		t.Fatalf("exit = %d, stderr = %q, want a refusal that never repeats the value", code, stderr.String())
	}
}

func TestIssueFileRefusesAMissingProjectOrTitle(t *testing.T) {
	for _, args := range [][]string{{}, {"open"}, {"file", "--title", "x"}, {"file", "--project", "widgets"}, {"file", "--project", "widgets", "--title", " "}} {
		var stdout, stderr strings.Builder
		if code := runIssue(args, &stdout, &stderr, commandRuntime{}); code != 2 {
			t.Errorf("runIssue(%q) = %d, want 2", args, code)
		}
	}
}
