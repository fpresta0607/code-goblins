package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

const rootRepository = "acme/widgets"

// rootGitHub answers the gh api requests root filing makes the way GitHub
// would: open holds the open ops-root issues, each as GitHub lists it, and
// every request is recorded.
type rootGitHub struct {
	open        []map[string]any
	isLabelMade bool
	listFails   bool
	calls       []apiCall
}

func (f *rootGitHub) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	call := apiCall{Fields: map[string][]string{}}
	for i := 1; i < len(req.Args); i++ {
		switch req.Args[i] {
		case "--method":
			i++
			call.Method = req.Args[i]
		case "-f":
			i++
			key, value, _ := strings.Cut(req.Args[i], "=")
			call.Fields[key] = append(call.Fields[key], value)
		default:
			call.Path = req.Args[i]
		}
	}
	f.calls = append(f.calls, call)
	switch {
	case call.Method == "GET" && strings.HasPrefix(call.Path, "repos/"+rootRepository+"/issues?labels=ops-root&state=open"):
		if f.listFails {
			return execx.Result{ExitCode: 1, Stderr: []byte("gh: Bad credentials (HTTP 401)")}, nil
		}
		out, _ := json.Marshal(f.open)
		return execx.Result{Stdout: out}, nil
	case call.Method == "POST" && call.Path == "repos/"+rootRepository+"/labels":
		if f.isLabelMade {
			return execx.Result{ExitCode: 1, Stdout: []byte(`{"message":"Validation Failed","errors":[{"resource":"Label","code":"already_exists","field":"name"}]}`), Stderr: []byte("gh: Validation Failed (HTTP 422)")}, nil
		}
		f.isLabelMade = true
		return execx.Result{Stdout: []byte(`{}`)}, nil
	case call.Method == "POST" && call.Path == "repos/"+rootRepository+"/issues":
		return execx.Result{Stdout: []byte(`{"number":42,"html_url":"https://github.com/` + rootRepository + `/issues/42"}`)}, nil
	case call.Method == "POST" && strings.HasSuffix(call.Path, "/comments"):
		return execx.Result{Stdout: []byte(`{"id":1}`)}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("gh: Not Found (HTTP 404)")}, nil
}

func (f *rootGitHub) writes() []apiCall {
	var writes []apiCall
	for _, call := range f.calls {
		if call.Method != "GET" {
			writes = append(writes, call)
		}
	}
	return writes
}

var rootNow = time.Date(2026, 10, 14, 18, 45, 0, 0, time.UTC)

func TestFileRootOpensALabelledIssueWithItsMarkerAndEvidence(t *testing.T) {
	// Arrange
	fake := &rootGitHub{}
	root := Root{Title: "conpty test fails under load", Body: "## What happens\nIt fails.", Key: "test:internal/conpty TestInputLeftUnread", Evidence: "run 7 of acme/widgets"}

	// Act
	filed, err := GitHub{Commands: fake}.FileRoot(context.Background(), rootRepository, rootRepository, root, rootNow)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if filed.Number != 42 || filed.IsRecurrence {
		t.Fatalf("filed = %+v, want new issue 42", filed)
	}
	writes := fake.writes()
	if len(writes) != 2 || writes[0].Path != "repos/"+rootRepository+"/labels" || writes[1].Path != "repos/"+rootRepository+"/issues" {
		t.Fatalf("writes = %v, want the label then the issue", writes)
	}
	issue := writes[1].Fields
	if issue["labels[]"][0] != RootLabel || issue["title"][0] != root.Title {
		t.Fatalf("issue fields = %v", issue)
	}
	body := issue["body"][0]
	for _, want := range []string{"## What happens\nIt fails.", "## Evidence\n\n2026-10-14 18:45Z: run 7 of acme/widgets", "<!-- ops-root: test:internal/conpty testinputleftunread -->"} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q lacks %q", body, want)
		}
	}
}

func TestFileRootCommentsOnTheOpenIssueOfTheSameRoot(t *testing.T) {
	cases := []struct {
		name string
		open map[string]any
		root Root
	}{
		{
			name: "marker names the same key",
			open: map[string]any{"number": 7, "title": "an older wording", "html_url": "https://github.com/acme/widgets/issues/7", "body": "text\n\n<!-- ops-root: test:internal/conpty testx -->\n"},
			root: Root{Title: "conpty flakes", Key: "test:internal/conpty TestX", Evidence: "run 9"},
		},
		{
			name: "filed by hand with the same title",
			open: map[string]any{"number": 7, "title": "A merge train may not start", "html_url": "https://github.com/acme/widgets/issues/7", "body": "filed by hand"},
			root: Root{Title: "a merge train  may not start", Evidence: "run 9"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			fake := &rootGitHub{open: []map[string]any{tc.open}}

			// Act
			filed, err := GitHub{Commands: fake}.FileRoot(context.Background(), rootRepository, rootRepository, tc.root, rootNow)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if filed.Number != 7 || !filed.IsRecurrence {
				t.Fatalf("filed = %+v, want a comment on 7", filed)
			}
			writes := fake.writes()
			if len(writes) != 1 || writes[0].Path != "repos/"+rootRepository+"/issues/7/comments" {
				t.Fatalf("writes = %v, want one comment on 7", writes)
			}
			if got := writes[0].Fields["body"][0]; got != "Seen again on 2026-10-14 18:45Z.\n\nrun 9" {
				t.Fatalf("comment = %q", got)
			}
		})
	}
}

func TestFileRootOpensANewIssueWhenTheOpenOnesHaveOtherRoots(t *testing.T) {
	// Arrange: a marked issue whose title matches is another root, and a pull
	// request GitHub lists among issues is no issue.
	fake := &rootGitHub{isLabelMade: true, open: []map[string]any{
		{"number": 7, "title": "conpty flakes", "body": "<!-- ops-root: test:internal/other testy -->"},
		{"number": 8, "title": "conpty flakes", "body": "", "pull_request": map[string]any{"url": "x"}},
	}}

	// Act
	filed, err := GitHub{Commands: fake}.FileRoot(context.Background(), rootRepository, rootRepository, Root{Title: "conpty flakes", Key: "test:internal/conpty TestX"}, rootNow)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if filed.Number != 42 || filed.IsRecurrence {
		t.Fatalf("filed = %+v, want new issue 42 beside an existing label", filed)
	}
}

func TestFileRootMatchesFailedOnceSightingsToManualTestRoots(t *testing.T) {
	cases := []struct {
		name, title, body, key string
		isRecurrence           bool
	}{
		{
			name:         "a prose key with the repository relative package",
			title:        "native host ends before it serves in TestNativeHost",
			body:         "`TestNativeHost` in `cmd/cfo` fails before the host serves.",
			key:          "native host ends before it serves in testnativehost",
			isRecurrence: true,
		},
		{
			name:         "an unmarked issue with the full package",
			title:        "TestNativeHost fails under load",
			body:         "TestNativeHost in github.com/acme/widgets/cmd/cfo.",
			isRecurrence: true,
		},
		{
			name:         "the package before the test",
			title:        "cmd/cfo TestNativeHost fails under load",
			isRecurrence: true,
		},
		{
			name:         "the package in parentheses",
			title:        "TestNativeHost (cmd/cfo) fails under load",
			isRecurrence: true,
		},
		{
			name:         "in also introduces prose that is not a package",
			title:        "TestNativeHost in CI fails",
			body:         "`TestNativeHost` in `cmd/cfo` fails before the host serves.",
			isRecurrence: true,
		},
		{
			name:  "an incidental package mention cannot change the tests package",
			title: "TestNativeHost in internal/other fails",
			body:  "The failure started after cmd/cfo changed.",
		},
		{
			name:  "conflicting identities are ambiguous",
			title: "TestNativeHost in internal/other fails",
			body:  "Compare TestNativeHost in cmd/cfo, which passes.",
		},
		{
			name:  "an explicit test key wins over incidental mentions",
			title: "TestNativeHost fails under load",
			body:  "`TestNativeHost` in `cmd/cfo` also failed.",
			key:   "test:github.com/acme/widgets/internal/other testnativehost",
		},
		{
			name:  "a longer test name is unrelated",
			title: "TestNativeHostRestart fails under load",
			body:  "The package is `cmd/cfo`.",
		},
		{
			name:  "a subtest is distinct",
			title: "TestNativeHost/restart fails under load",
			body:  "The package is `cmd/cfo`.",
		},
		{
			name:  "a longer package is unrelated",
			title: "TestNativeHost fails under load",
			body:  "The package is `cmd/cfo-extra`.",
		},
		{
			name:  "the same name in a different package is unrelated",
			title: "TestNativeHost fails under load",
			body:  "The package is `internal/other`.",
		},
		{
			name:  "the same package suffix in another module is unrelated",
			title: "TestNativeHost fails under load",
			body:  "The package is `github.com/other/widgets/cmd/cfo`.",
		},
		{
			name:  "a name without a package is insufficient",
			title: "TestNativeHost fails under load",
			body:  "The host ends before it serves.",
		},
		{
			name:  "a different root mentioning the test as evidence",
			title: "TestOtherHost fails under load",
			body:  "Unlike `TestNativeHost` in `cmd/cfo`, this test fails twice.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			body := tc.body
			if tc.key != "" {
				body += "\n\n<!-- ops-root: " + tc.key + " -->"
			}
			fake := &rootGitHub{open: []map[string]any{{"number": 7, "title": tc.title, "body": body, "html_url": "https://github.com/acme/widgets/issues/7"}}}
			root := FailedOnceRoot("go (cmd-cfo): github.com/acme/widgets/cmd/cfo TestNativeHost", "run 9")

			// Act
			filed, err := GitHub{Commands: fake}.FileRoot(context.Background(), rootRepository, rootRepository, root, rootNow)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if filed.IsRecurrence != tc.isRecurrence {
				t.Fatalf("filed = %+v, want recurrence %t", filed, tc.isRecurrence)
			}
			writes := fake.writes()
			if tc.isRecurrence {
				if filed.Number != 7 || len(writes) != 1 || writes[0].Path != "repos/"+rootRepository+"/issues/7/comments" {
					t.Fatalf("filed = %+v, writes = %v, want one comment on 7", filed, writes)
				}
				if got := writes[0].Fields["body"][0]; got != "Seen again on 2026-10-14 18:45Z.\n\nrun 9" {
					t.Fatalf("comment = %q", got)
				}
			} else if filed.Number != 42 || len(writes) != 2 || writes[1].Path != "repos/"+rootRepository+"/issues" {
				t.Fatalf("filed = %+v, writes = %v, want a new issue", filed, writes)
			}
		})
	}
}

func TestFileRootFilesNothingWhenTheOpenIssuesCannotBeRead(t *testing.T) {
	// Arrange
	fake := &rootGitHub{listFails: true}

	// Act
	_, err := GitHub{Commands: fake}.FileRoot(context.Background(), rootRepository, rootRepository, Root{Title: "x"}, rootNow)

	// Assert
	if err == nil {
		t.Fatal("FileRoot filed without reading the open issues")
	}
	if writes := fake.writes(); len(writes) != 0 {
		t.Fatalf("writes = %v, want none", writes)
	}
}

func TestOpenRootsRefusesMoreThanItCanRead(t *testing.T) {
	// Arrange
	full := make([]map[string]any, 100)
	for i := range full {
		full[i] = map[string]any{"number": i + 1, "title": fmt.Sprint(i)}
	}
	fake := &rootGitHub{open: full}

	// Act
	_, err := GitHub{Commands: fake}.OpenRoots(context.Background(), rootRepository)

	// Assert
	if err == nil {
		t.Fatal("OpenRoots answered with a partial list")
	}
}

func TestOpenRootsReadsAssigneesLabelsAndAuthorStanding(t *testing.T) {
	// Arrange
	fake := &rootGitHub{open: []map[string]any{{"number": 3, "title": "t", "body": "b\n<!-- ops-root: k -->", "author_association": "OWNER", "assignees": []map[string]any{{"login": "someone"}}, "labels": []map[string]any{{"name": "ops-root"}}}}}

	// Act
	open, err := GitHub{Commands: fake}.OpenRoots(context.Background(), rootRepository)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := OpenRoot{Number: 3, Title: "t", Body: "b\n<!-- ops-root: k -->", Key: "k", Assignees: []string{"someone"}, Labels: []string{"ops-root"}, Association: "OWNER"}
	if fmt.Sprint(open) != fmt.Sprint([]OpenRoot{want}) {
		t.Fatalf("open = %+v, want %+v", open, want)
	}
}

func TestReadRepositoryReadsWhoseItIsAndWhetherItTakesIssues(t *testing.T) {
	cases := []struct {
		name, answer string
		want         Repository
		isErr        bool
	}{
		{name: "a public fork with issues turned off", answer: `{"owner":{"login":"Acme"},"private":false,"has_issues":false,"fork":true}`, want: Repository{Owner: "Acme", IsPublic: true}},
		{name: "a private repository that takes issues", answer: `{"owner":{"login":"acme"},"private":true,"has_issues":true}`, want: Repository{Owner: "acme", HasIssues: true}},
		{name: "an answer that says nothing of issues", answer: `{"owner":{"login":"acme"},"private":false}`, isErr: true},
		{name: "an answer that names no owner", answer: `{"private":false,"has_issues":true}`, isErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			fake := &fakeGitHub{rest: map[string]string{"repos/" + rootRepository: tc.answer}}

			// Act
			got, err := GitHub{Commands: fake}.ReadRepository(context.Background(), rootRepository)

			// Assert
			if tc.isErr {
				if err == nil {
					t.Fatalf("ReadRepository = %+v, want an error", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("ReadRepository = %+v, %v, want %+v", got, err, tc.want)
			}
		})
	}
}

func TestRootKeyFallsBackToTheTitleAndCannotEndItsMarker(t *testing.T) {
	cases := map[string]Root{
		"a train  may not start":    {Title: "A Train may not\tstart"},
		"test:pkg testx":            {Title: "ignored", Key: " test:pkg  TestX "},
		"error:a --broken marker--": {Key: "error:a <--broken marker-->"},
	}
	for want, root := range cases {
		if got := root.RootKey(); got != strings.Join(strings.Fields(want), " ") {
			t.Errorf("RootKey(%+v) = %q, want %q", root, got, want)
		}
	}
}

// The screen's key fixtures are made up and built from pieces, so no
// literal that has a key's shape sits in this public repository: a GitHub
// token's prefix before filler, the AWS documentation's example access key
// ID, and a PEM header with no key under it.
var (
	fakeGitHubToken = "ghp" + "_" + strings.Repeat("x1", 15)
	fakeAWSKeyID    = "AKIA" + "IOSFODNN7" + "EXAMPLE"
	fakePEMHeader   = "-----BEGIN " + "RSA PRIVATE" + " KEY-----"
)

func TestScreenRefusesWhatAPublicIssueMayNotSay(t *testing.T) {
	screen := Screen{IsPublic: true, Private: []string{"PocketPiggies", "Franklin-Properties"}, People: []string{"jdoe", "Jane Doe"}, Account: "fpresta0607", Secrets: []string{"s3cr3t-value-from-env"}}
	cases := []struct {
		text, want string
	}{
		{"seen in pocket-piggies today", "it names PocketPiggies, whose repository is not public"},
		{"the Franklin Properties gate", "it names Franklin-Properties, whose repository is not public"},
		{"Jane Doe's pull request", "it names Jane Doe, a teammate"},
		{"asked @someone about it", "it mentions @someone"},
		{"log at C:\\dev\\x\\out.log", "it names a path on this machine (C:\\...)"},
		{"log at D:/CodeGoblins/x", "it names a path on this machine (D:/...)"},
		{"ran from /c/dev/repo", "it names a path on this machine (/c/...)"},
		{"in ~/.codex/worktrees", "it names a path on this machine (~/...)"},
		{"share \\\\server\\files\\x", "it names a path on this machine (\\\\server\\...)"},
		{"token=" + fakeGitHubToken, "it holds what has the shape of a key"},
		{"key " + fakeAWSKeyID, "it holds what has the shape of a key"},
		{fakePEMHeader, "it holds a private key"},
		{"the value s3cr3t-value-from-env leaked", "it holds a credential value this terminal carries"},
	}
	for _, tc := range cases {
		if got := screen.Refusal(tc.text); got != tc.want {
			t.Errorf("Refusal(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestScreenPassesWhatIssuesAboutCodeSay(t *testing.T) {
	screen := Screen{IsPublic: true, Private: []string{"PocketPiggies", "web"}, People: []string{"Jane Doe"}, Account: "fpresta0607"}
	texts := []string{
		"TestInputLeftUnread in internal/conpty failed 2 of 5 at d1faafe41c2b9e0f3a7d5c4b8e6f1a2b3c4d5e6f",
		"see https://github.com/fpresta0607/code-goblins/actions/runs/123456789 and @fpresta0607",
		"GET /users/42 answers 500; mail jane@example.com; and/or a=b",
		"pocketpiggiesx and webhooks are other words",
		"re_enable_the_wake_path_for_goblins stays a name",
	}
	for _, text := range texts {
		if got := screen.Refusal(text); got != "" {
			t.Errorf("Refusal(%q) = %q, want none", text, got)
		}
	}
}

func TestScreenOnAPrivateRepositoryRefusesOnlySecrets(t *testing.T) {
	screen := Screen{Private: []string{"PocketPiggies"}, People: []string{"Jane Doe"}}
	if got := screen.Refusal("PocketPiggies at C:\\dev with Jane Doe"); got != "" {
		t.Errorf("private repository refused %q", got)
	}
	if got := screen.Refusal(fakeGitHubToken); got == "" {
		t.Error("private repository took a key")
	}
}
