package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/state"
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
	reads    []string
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
	fields := map[string]string{}
	for i := 1; i < len(req.Args); i++ {
		switch req.Args[i] {
		case "--method":
			i++
			method = req.Args[i]
		case "-f":
			i++
			key, value, _ := strings.Cut(req.Args[i], "=")
			fields[key] = value
		default:
			path = req.Args[i]
		}
	}
	switch {
	case method == "GET" && strings.Contains(path, "/issues?labels=ops-root"):
		f.reads = append(f.reads, path)
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
			url := "https://github.com/" + strings.TrimPrefix(path, "repos/") + "/42"
			opened, err := json.Marshal([]struct {
				Number int    `json:"number"`
				Title  string `json:"title"`
				Body   string `json:"body"`
				URL    string `json:"html_url"`
			}{{42, fields["title"], fields["body"], url}})
			if err != nil {
				return execx.Result{}, err
			}
			f.open = string(opened)
			return execx.Result{Stdout: []byte(fmt.Sprintf(`{"number":42,"html_url":%q}`, url))}, nil
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

func writeIssueManifest(t *testing.T, h home.Home, checkout, text string) {
	t.Helper()
	path := project.Path(h.Data, filepath.Base(checkout))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRootFilingUsesTheProjectIssueRepository(t *testing.T) {
	for _, configuration := range []struct{ name, manifest, repository string }{
		{"no record", "", "acme/widgets"},
		{"unset", `{"project":"widgets"}`, "acme/widgets"},
		{"explicit", `{"project":"widgets","issue_repository":"acme/ops"}`, "acme/ops"},
	} {
		t.Run(configuration.name, func(t *testing.T) {
			// Arrange
			gh := &issueGH{private: map[string]bool{"acme/ops": true}}
			runtime, h := issueRuntime(t, gh)
			if configuration.manifest != "" {
				writeIssueManifest(t, h, "widgets", configuration.manifest)
			}
			var stdout, stderr strings.Builder

			// Act: file manually, then see the same test through the supervisor.
			code := runIssue([]string{"file", "--project", "widgets", "--title", "native host ends in TestNativeHost", "--body-file", writeBody(t, "`TestNativeHost` in `cmd/cfo` fails before the host serves.")}, &stdout, &stderr, runtime)
			if code != 0 {
				t.Fatalf("manual filing exit = %d: %s", code, stderr.String())
			}
			filed, err := fileRootFrom(runtime, false)(context.Background(), "widgets", tickets.FailedOnceRoot("go (cmd-cfo): github.com/acme/widgets/cmd/cfo TestNativeHost", "run 9"))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			prefix := "repos/" + configuration.repository
			want := []string{prefix + "/labels", prefix + "/issues", prefix + "/issues/42/comments"}
			if !filed.IsRecurrence || !slices.Equal(gh.writes, want) {
				t.Fatalf("filed = %+v, writes = %v, want %v", filed, gh.writes, want)
			}
			if filed.URL != "https://github.com/"+configuration.repository+"/issues/42" {
				t.Fatalf("filed URL = %s", filed.URL)
			}
			for _, read := range gh.reads {
				if !strings.HasPrefix(read, prefix+"/issues?") {
					t.Fatalf("read open issues from %s, want %s", read, configuration.repository)
				}
			}
		})
	}
}

func TestRootFilingRefusesAnInvalidProjectRecord(t *testing.T) {
	for _, manifest := range []string{`{"project":"widgets","issue_repository":"acme/ops/extra"}`, `{"project":"widgets","issue_repository":42}`, `{"project":"widgets",`} {
		t.Run(manifest, func(t *testing.T) {
			// Arrange
			gh := &issueGH{}
			runtime, h := issueRuntime(t, gh)
			writeIssueManifest(t, h, "widgets", manifest)

			// Act
			_, err := fileRootFrom(runtime, false)(context.Background(), "widgets", tickets.Root{Title: "a root"})
			var stdout, stderr strings.Builder
			code := runIssue([]string{"file", "--project", "widgets", "--title", "a root"}, &stdout, &stderr, runtime)

			// Assert
			if err == nil || code != 1 || len(gh.writes) != 0 || len(gh.reads) != 0 {
				t.Fatalf("error = %v, reads = %v, writes = %v, want refusal before filing", err, gh.reads, gh.writes)
			}
		})
	}
}

func TestRootFilingKeepsProjectsDistinctInASharedIssueRepository(t *testing.T) {
	for _, root := range []tickets.Root{
		{Title: "native host ends in TestNativeHost", Body: "TestNativeHost in cmd/cfo fails."},
		{Title: "workflow fails", Key: "ci:build"},
	} {
		t.Run(root.Title, func(t *testing.T) {
			// Arrange
			gh := &issueGH{private: map[string]bool{"acme/ops": true}}
			runtime, h := issueRuntime(t, gh)
			for _, name := range []string{"widgets", "secret-project"} {
				writeIssueManifest(t, h, name, fmt.Sprintf(`{"project":%q,"issue_repository":"acme/ops"}`, name))
			}
			if _, err := fileRoot(context.Background(), runtime, "widgets", root, time.Now()); err != nil {
				t.Fatal(err)
			}
			gh.writes = nil
			if root.Key == "" {
				root = tickets.FailedOnceRoot("go (cmd-cfo): github.com/acme/secret-project/cmd/cfo TestNativeHost", "run 9")
			}

			// Act
			filed, err := fileRootFrom(runtime, false)(context.Background(), "secret-project", root)

			// Assert
			want := []string{"repos/acme/ops/labels", "repos/acme/ops/issues"}
			if err != nil || filed.IsRecurrence || !slices.Equal(gh.writes, want) {
				t.Fatalf("filed = %+v, error = %v, writes = %v, want a separate root", filed, err, gh.writes)
			}
			gh.writes = nil
			filed, err = fileRootFrom(runtime, false)(context.Background(), "secret-project", root)
			if err != nil || !filed.IsRecurrence || !slices.Equal(gh.writes, []string{"repos/acme/ops/issues/42/comments"}) {
				t.Fatalf("filed = %+v, error = %v, writes = %v, want the same project's recurrence", filed, err, gh.writes)
			}
		})
	}
}

func TestRootFilingAllowsItsPublicCheckoutOutsideTheProjectsRoot(t *testing.T) {
	// Arrange
	gh := &issueGH{}
	runtime, h := issueRuntime(t, gh)
	projectsRoot, err := runtime.projectsRoot()
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(projectsRoot, "widgets")
	runtime.projectsRoot = func() (string, error) { return "", nil }
	writeIssueManifest(t, h, checkout, `{"project":"widgets"}`)

	// Act
	_, err = fileRoot(context.Background(), runtime, checkout, tickets.Root{Title: "widgets has a root"}, time.Now())

	// Assert
	if err != nil || len(gh.writes) != 2 {
		t.Fatalf("error = %v, writes = %v, want the known public checkout's issue", err, gh.writes)
	}
}

func TestRootFilingScreensTheSelectedIssueRepository(t *testing.T) {
	for _, configuration := range []struct {
		name, repository, body, refusal string
		isPrivate, hasNoIssues          bool
	}{
		{name: "private destination", repository: "acme/ops", isPrivate: true, body: `Seen in secret-project at C:\dev\widgets.`},
		{name: "public destination", repository: "acme/ops", body: `Seen at C:\dev\widgets.`, refusal: "which is public"},
		{name: "private project", repository: "acme/ops", body: "Seen in secret-project.", refusal: "it names secret-project"},
		{name: "teammate", repository: "acme/ops", body: "Jane Doe saw it.", refusal: "it names Jane Doe"},
		{name: "private source project", repository: "acme/ops", body: "Seen in widgets.", refusal: "it names widgets"},
		{name: "private source with same destination name", repository: "acme-org/widgets", body: "Seen in widgets.", refusal: "it names widgets"},
		{name: "credential", repository: "acme/ops", isPrivate: true, body: "Sent made-up-value-0451", refusal: "it holds a credential value"},
		{name: "issues disabled", repository: "acme/ops", hasNoIssues: true, body: "a root", refusal: "has issues turned off"},
		{name: "another owner", repository: "stranger/ops", isPrivate: true, body: "a root", refusal: "its owner stranger is neither acme"},
	} {
		t.Run(configuration.name, func(t *testing.T) {
			// Arrange
			t.Setenv("WIDGETS_API_TOKEN", "made-up-value-0451")
			gh := &issueGH{
				private:  map[string]bool{"acme/widgets": true, "acme/secret-project": true, configuration.repository: configuration.isPrivate},
				noIssues: map[string]bool{configuration.repository: configuration.hasNoIssues},
			}
			runtime, h := issueRuntime(t, gh)
			if err := os.WriteFile(filepath.Join(h.Root, "config", "fleet.json"), []byte(`{"github_owners":["acme-org"]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			writeIssueManifest(t, h, "widgets", fmt.Sprintf(`{"project":"widgets","issue_repository":%q}`, configuration.repository))
			if err := tickets.RememberTeammates(h.State, []tickets.Actor{{Login: "jdoe", Name: "Jane Doe"}}); err != nil {
				t.Fatal(err)
			}

			// Act
			_, err := fileRoot(context.Background(), runtime, "widgets", tickets.Root{Title: "a root", Body: configuration.body}, time.Now())

			// Assert
			if configuration.refusal == "" {
				if err != nil || len(gh.writes) != 2 {
					t.Fatalf("error = %v, writes = %v, want private issue filed", err, gh.writes)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), configuration.refusal) || len(gh.writes) != 0 {
				t.Fatalf("error = %v, writes = %v, want refusal %q", err, gh.writes, configuration.refusal)
			}
			if strings.Contains(err.Error(), "made-up-value-0451") {
				t.Fatal("refusal exposed a credential")
			}
		})
	}
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

func TestIssueFileNotesTheGoblinTaskThatFiledANewRoot(t *testing.T) {
	cases := []struct {
		name, gate, open string
		want             []string
	}{
		{name: "a new issue", want: []string{"https://github.com/acme/widgets/issues/42"}},
		{name: "a step under a gate", gate: "1"},
		{name: "a sighting of an open issue", open: `[{"number":7,"title":"follow-up","html_url":"https://github.com/acme/widgets/issues/7","body":"filed by hand"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &issueGH{open: tc.open}
			runtime, h := issueRuntime(t, gh)
			taskTmp := filepath.Join(h.State, "tasktmp", "widgets-fix")
			if err := os.MkdirAll(taskTmp, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(state.TaskMetaPath(h.State, "widgets-fix"), []byte("tasktmp="+taskTmp+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CFO_TASK_ID", "widgets-fix")
			t.Setenv("NO_MISTAKES_GATE", tc.gate)
			var stdout, stderr strings.Builder

			// Act
			code := runIssue([]string{"file", "--project", "widgets", "--title", "follow-up"}, &stdout, &stderr, runtime)

			// Assert
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
			}
			filed, err := tickets.FiledRoots(taskTmp)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(filed, tc.want) {
				t.Fatalf("noted as filed by the task = %v, want %v", filed, tc.want)
			}
		})
	}
}

func TestIssueFileFromATaskTheHomeHasNoRecordOfNotesNothing(t *testing.T) {
	// Arrange
	gh := &issueGH{}
	runtime, h := issueRuntime(t, gh)
	t.Setenv("CFO_TASK_ID", "elsewhere")
	t.Setenv("NO_MISTAKES_GATE", "")
	var stdout, stderr strings.Builder

	// Act
	code := runIssue([]string{"file", "--project", "widgets", "--title", "follow-up"}, &stdout, &stderr, runtime)

	// Assert
	if code != 0 || !strings.Contains(stdout.String(), "filed #42") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(h.State, "tasktmp")); !os.IsNotExist(err) {
		t.Fatalf("a task temporary directory was made for a task with no record: %v", err)
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

func TestIssueFileSharesAManualTestRootWithAutomaticSightings(t *testing.T) {
	for _, isOpen := range []bool{true, false} {
		t.Run(fmt.Sprintf("manual root open=%t", isOpen), func(t *testing.T) {
			// Arrange: file by hand through the command and keep the issue GitHub
			// would return, including its prose marker, for the automatic sighting.
			gh := &issueGH{}
			runtime, _ := issueRuntime(t, gh)
			var stdout, stderr strings.Builder
			code := runIssue([]string{"file", "--project", "widgets", "--title", "native host ends before it serves in TestNativeHost", "--body-file", writeBody(t, "`TestNativeHost` in `cmd/cfo` fails before the host serves.")}, &stdout, &stderr, runtime)
			if code != 0 {
				t.Fatalf("manual filing exit = %d: %s", code, stderr.String())
			}
			if !strings.Contains(gh.open, "ops-root: native host ends before it serves in testnativehost") {
				t.Fatalf("manual issue = %s, want its prose root marker", gh.open)
			}
			if !isOpen {
				// GitHub's state=open listing excludes the issue once it closes.
				gh.open = "[]"
			}
			gh.writes = nil

			// Act
			root := tickets.FailedOnceRoot("go (cmd-cfo): github.com/acme/widgets/cmd/cfo TestNativeHost", "run 9")
			filed, err := fileRoot(context.Background(), runtime, "widgets", root, time.Now())

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			want := "repos/acme/widgets/labels repos/acme/widgets/issues"
			if isOpen {
				want = "repos/acme/widgets/issues/42/comments"
			}
			if filed.IsRecurrence != isOpen || strings.Join(gh.writes, " ") != want {
				t.Fatalf("filed = %+v, writes = %v, want %s", filed, gh.writes, want)
			}
		})
	}
}

func TestIssueFileHelpNamesTheAutomaticTestRootFormat(t *testing.T) {
	// Arrange
	var stdout, stderr strings.Builder

	// Act
	runIssue([]string{"file", "--help"}, &stdout, &stderr, commandRuntime{})

	// Assert
	if !strings.Contains(stderr.String(), "test:<package> <name>") {
		t.Fatalf("help = %q, want the automatic test identity", stderr.String())
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
			want:    []string{"refused: the issue would go on upstream/widgets, and its owner upstream is neither acme, the account gh works as, nor an owner config/fleet.json names", "the fleet owns acme/widgets, which remote fork names"},
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
