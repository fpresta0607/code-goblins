package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// issueGH answers the gh api and git requests cfo issue file makes the way
// GitHub and a checkout would. account is the account gh works as, acme
// unless set; private names the repositories that are not public, and
// noIssues those with issues turned off; remotes names, by a checkout's
// folder, each of its remotes and its address, and a checkout it does not
// name has only origin, acme/<folder>; open is the JSON listing of the open
// ops-root issues. Every write is recorded.
type issueGH struct {
	account  string
	private  map[string]bool
	noIssues map[string]bool
	remotes  map[string]map[string]string
	open     string
	writes   []string
}

func (f *issueGH) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	if req.Name == "git" {
		return f.git(req.Args), nil
	}
	if slices.Equal(req.Args, []string{"api", "user", "--jq", ".login"}) {
		account := f.account
		if account == "" {
			account = "acme"
		}
		return execx.Result{Stdout: []byte(account + "\n")}, nil
	}
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
		repository := strings.TrimPrefix(path, "repos/")
		owner, _, _ := strings.Cut(repository, "/")
		return execx.Result{Stdout: []byte(fmt.Sprintf(`{"owner":{"login":%q},"private":%t,"has_issues":%t}`, owner, f.private[repository], !f.noIssues[repository]))}, nil
	case method == "POST":
		f.writes = append(f.writes, path)
		if strings.HasSuffix(path, "/issues") {
			return execx.Result{Stdout: []byte(`{"number":42,"html_url":"https://github.com/acme/widgets/issues/42"}`)}, nil
		}
		return execx.Result{Stdout: []byte(`{}`)}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("gh: Not Found (HTTP 404)")}, nil
}

// git answers git -C <checkout> remote get-url origin and git -C <checkout>
// remote -v as git would, in the order of the remotes' names.
func (f *issueGH) git(args []string) execx.Result {
	folder := filepath.Base(args[1])
	remotes := f.remotes[folder]
	if remotes == nil {
		remotes = map[string]string{"origin": "https://github.com/acme/" + folder + ".git"}
	}
	switch strings.Join(args[2:], " ") {
	case "remote get-url origin":
		return execx.Result{Stdout: []byte(remotes["origin"] + "\n")}
	case "remote -v":
		var listing strings.Builder
		for _, name := range slices.Sorted(maps.Keys(remotes)) {
			fmt.Fprintf(&listing, "%s\t%s (fetch)\n%s\t%s (push)\n", name, remotes[name], name, remotes[name])
		}
		return execx.Result{Stdout: []byte(listing.String())}
	}
	return execx.Result{ExitCode: 129, Stderr: []byte("git: an unexpected request: " + strings.Join(args, " "))}
}

// issueRuntime is a home whose projects root holds widgets, the project an
// issue is filed for, and secret-project, a private one, each a checkout
// whose remotes gh's fake names, through the path cfo reads origin by.
func issueRuntime(t *testing.T, gh *issueGH) (commandRuntime, home.Home) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"widgets", "secret-project"} {
		if err := os.MkdirAll(filepath.Join(root, name, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	h := home.Home{Root: t.TempDir(), Data: filepath.Join(t.TempDir(), "data"), State: filepath.Join(t.TempDir(), "state")}
	for _, dir := range []string{h.State, filepath.Join(h.Root, "config")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return commandRuntime{
		resolveHome:  func() (home.Home, error) { return h, nil },
		projectsRoot: func() (string, error) { return root, nil },
		repositoryOf: tickets.GitHub{Commands: gh}.RepositoryOf,
		github:       gh,
	}, h
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

func TestIssueFileFilesOnARepositoryTheFleetOwns(t *testing.T) {
	cases := []struct {
		name, account, fleetJSON string
	}{
		{"the account gh works as, in another case", "ACME", ""},
		{"an organization config/fleet.json names", "jdoe", `{"github_owners": ["Acme"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &issueGH{account: tc.account}
			runtime, h := issueRuntime(t, gh)
			if tc.fleetJSON != "" {
				if err := os.WriteFile(filepath.Join(h.Root, "config", "fleet.json"), []byte(tc.fleetJSON), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr strings.Builder

			// Act
			code := runIssue([]string{"file", "--project", "widgets", "--title", "a root"}, &stdout, &stderr, runtime)

			// Assert
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
			}
			if strings.Join(gh.writes, " ") != "repos/acme/widgets/labels repos/acme/widgets/issues" {
				t.Fatalf("writes = %v, want the label and the issue on acme/widgets", gh.writes)
			}
		})
	}
}

func TestIssueFileRefusesARepositoryTheFleetDoesNotOwn(t *testing.T) {
	cases := []struct {
		name      string
		remotes   map[string]string
		fleetJSON string
		want      []string
	}{
		{
			name:    "a fork whose origin is its parent",
			remotes: map[string]string{"origin": "https://github.com/upstream/widgets.git", "fork": "https://github.com/acme/widgets.git", "gate": `C:\Users\someone\.no-mistakes\repos\f958ac41fb39.git`},
			want:    []string{"refused: the issue would go on upstream/widgets, which origin names, and its owner upstream is neither acme, the account gh works as, nor an owner config/fleet.json names", "the fleet owns acme/widgets, which remote fork names"},
		},
		{
			name:      "a fork of an organization config/fleet.json names",
			remotes:   map[string]string{"origin": "git@github.com:upstream/widgets.git", "fork": "https://github.com/acme/widgets.git", "team": "https://github.com/acme-org/widgets"},
			fleetJSON: `{"github_owners": ["Acme-Org"]}`,
			want:      []string{"refused: the issue would go on upstream/widgets", "the fleet owns acme/widgets, which remote fork names, and acme-org/widgets, which remote team names"},
		},
		{
			name:    "a clone of a stranger's repository",
			remotes: map[string]string{"origin": "https://github.com/upstream/widgets.git"},
			want:    []string{"refused: the issue would go on upstream/widgets", "no remote of the checkout names a repository the fleet owns"},
		},
		{
			name:      "a stranger's repository while config/fleet.json cannot be read",
			remotes:   map[string]string{"origin": "https://github.com/upstream/widgets.git"},
			fleetJSON: `{"github_owners": ["not an owner"]}`,
			want:      []string{"upstream/widgets is not acme's", "config/fleet.json", "github_owners"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &issueGH{remotes: map[string]map[string]string{"widgets": tc.remotes}}
			runtime, h := issueRuntime(t, gh)
			if tc.fleetJSON != "" {
				if err := os.WriteFile(filepath.Join(h.Root, "config", "fleet.json"), []byte(tc.fleetJSON), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr strings.Builder

			// Act
			code := runIssue([]string{"file", "--project", "widgets", "--title", "a root", "--body-file", writeBody(t, "## What happens\nIt fails.")}, &stdout, &stderr, runtime)

			// Assert
			if code != 1 {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q, want a refusal", code, stdout.String(), stderr.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("stderr = %q, want it to say %q", stderr.String(), want)
				}
			}
			if len(gh.writes) != 0 {
				t.Fatalf("writes = %v, want none", gh.writes)
			}
		})
	}
}

func TestIssueFileRefusesARepositoryWithIssuesTurnedOff(t *testing.T) {
	// Arrange
	gh := &issueGH{noIssues: map[string]bool{"acme/widgets": true}}
	runtime, _ := issueRuntime(t, gh)
	var stdout, stderr strings.Builder

	// Act
	code := runIssue([]string{"file", "--project", "widgets", "--title", "a root"}, &stdout, &stderr, runtime)

	// Assert
	if code != 1 || !strings.Contains(stderr.String(), "refused: acme/widgets has issues turned off") {
		t.Fatalf("exit = %d, stderr = %q, want a refusal saying its issues are turned off", code, stderr.String())
	}
	if len(gh.writes) != 0 {
		t.Fatalf("writes = %v, want none", gh.writes)
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
			runtime, h := issueRuntime(t, gh)
			if err := tickets.RememberTeammates(h.State, []tickets.Actor{{Login: "jdoe", Name: "Jane Doe"}}); err != nil {
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
