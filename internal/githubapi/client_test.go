package githubapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// tokenRunner stands in for gh auth token: it prints the next of tokens on
// each run, the last one again once they run out, and fails with failure
// when that is set.
type tokenRunner struct {
	mu      sync.Mutex
	tokens  []string
	failure string
	runs    []execx.Request
}

func (r *tokenRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs = append(r.runs, request)
	if r.failure != "" {
		return execx.Result{ExitCode: 1, Stderr: []byte(r.failure)}, nil
	}
	token := r.tokens[min(len(r.runs), len(r.tokens))-1]
	return execx.Result{Stdout: []byte(token + "\n")}, nil
}

// testClient is a client of server's API that reads environment and runs
// commands for gh auth token.
func testClient(server *httptest.Server, environment map[string]string, commands execx.Runner) *Client {
	client := New(commands)
	client.BaseURL = server.URL
	client.HTTP = server.Client()
	client.Getenv = func(name string) string { return environment[name] }
	return client
}

// The token is GH_TOKEN, else GITHUB_TOKEN, as gh reads them for github.com,
// and gh is asked for one only when neither is set, once for the client's
// life.
func TestTheTokenComesFromTheEnvironmentBeforeGh(t *testing.T) {
	for _, test := range []struct {
		name        string
		environment map[string]string
		want        string
		wantRuns    int
	}{
		{"GH_TOKEN first", map[string]string{"GH_TOKEN": "from-gh-token", "GITHUB_TOKEN": "from-github-token"}, "from-gh-token", 0},
		{"GITHUB_TOKEN next", map[string]string{"GITHUB_TOKEN": "from-github-token"}, "from-github-token", 0},
		{"a blank one is none", map[string]string{"GH_TOKEN": "  "}, "from-gh", 1},
		{"gh's own last, read once", nil, "from-gh", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			var sent []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent = append(sent, r.Header.Get("Authorization"))
				_, _ = io.WriteString(w, `{"login":"o"}`)
			}))
			defer server.Close()
			runner := &tokenRunner{tokens: []string{"from-gh"}}
			client := testClient(server, test.environment, runner)

			// Act
			var account struct {
				Login string `json:"login"`
			}
			for range 2 {
				if err := client.Get(context.Background(), "user", &account); err != nil {
					t.Fatal(err)
				}
			}

			// Assert
			if account.Login != "o" || len(sent) != 2 || sent[0] != "Bearer "+test.want || sent[1] != sent[0] {
				t.Fatalf("sent %q and read %q, want two requests with Bearer %s", sent, account.Login, test.want)
			}
			if len(runner.runs) != test.wantRuns {
				t.Fatalf("gh ran %d times (%v), want %d", len(runner.runs), runner.runs, test.wantRuns)
			}
			if test.wantRuns > 0 && strings.Join(runner.runs[0].Args, " ") != "auth token --hostname github.com" {
				t.Fatalf("gh ran %v, want gh auth token --hostname github.com", runner.runs[0].Args)
			}
		})
	}
}

// A 401 drops the token kept from gh, so the next call reads it again, once;
// a token from the environment is the environment's and is never dropped.
func TestARefusedTokenIsReadAgainOnceByTheNextCall(t *testing.T) {
	// Arrange
	var sent []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	runner := &tokenRunner{tokens: []string{"stale", "fresh"}}
	client := testClient(server, nil, runner)
	var answer struct{}

	// Act
	refused := client.Get(context.Background(), "user", &answer)
	first := client.Get(context.Background(), "user", &answer)
	second := client.Get(context.Background(), "user", &answer)

	// Assert
	if StatusOf(refused) != http.StatusUnauthorized || strings.Contains(refused.Error(), "stale") {
		t.Fatalf("the refused call = %v, want a 401 that names no token", refused)
	}
	if first != nil || second != nil {
		t.Fatalf("the calls after the 401 = %v, %v, want both answered", first, second)
	}
	if len(runner.runs) != 2 || strings.Join(sent, ",") != "Bearer stale,Bearer fresh,Bearer fresh" {
		t.Fatalf("gh ran %d times and the requests sent %q, want the token read again once", len(runner.runs), sent)
	}

	// Arrange
	environmental := testClient(server, map[string]string{"GH_TOKEN": "stale"}, runner)

	// Act
	err := environmental.Get(context.Background(), "user", &answer)

	// Assert
	if StatusOf(err) != http.StatusUnauthorized || len(runner.runs) != 2 {
		t.Fatalf("a refused GH_TOKEN = %v after %d gh runs, want the 401 and no gh run", err, len(runner.runs))
	}
}

// A client gh cannot give a token sends nothing, and says why without what
// gh printed on its standard output.
func TestNoTokenSendsNothing(t *testing.T) {
	// Arrange
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	client := testClient(server, nil, &tokenRunner{failure: "no oauth token found for github.com"})

	// Act
	err := client.Get(context.Background(), "user", &struct{}{})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "gh auth token exited 1: no oauth token found") || requests != 0 {
		t.Fatalf("err = %v after %d requests, want gh's refusal and nothing sent", err, requests)
	}
}

// A refusal keeps its request, status and GitHub's own message.
func TestARefusalNamesTheRequestTheStatusAndGitHubsMessage(t *testing.T) {
	// Arrange
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found","documentation_url":"https://docs.github.com"}`)
	}))
	defer server.Close()
	client := testClient(server, map[string]string{"GH_TOKEN": "secret-token"}, nil)

	// Act
	err := client.Get(context.Background(), "/repos/o/r/actions/runs/7", &struct{}{})

	// Assert
	var refused *Error
	if !errors.As(err, &refused) || refused.Status != http.StatusNotFound || refused.Method != "GET" || refused.Path != "repos/o/r/actions/runs/7" || refused.Message != "Not Found" {
		t.Fatalf("err = %#v, want the GET of the run refused with 404 Not Found", err)
	}
	if want := "GitHub answered GET repos/o/r/actions/runs/7 with HTTP 404: Not Found"; err.Error() != want || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("err = %q, want %q", err, want)
	}
}

// A GraphQL answer with errors still gives the data GitHub did read, and
// returns GitHub's own words.
func TestGraphQLReadsTheDataAndGitHubsErrors(t *testing.T) {
	for _, test := range []struct {
		name      string
		answer    string
		wantLogin string
		wantErr   string
	}{
		{"answered", `{"data":{"viewer":{"login":"o"}}}`, "o", ""},
		{"answered in part", `{"data":{"viewer":{"login":"o"}},"errors":[{"message":"Could not resolve to a Repository"},{"message":"one more"}]}`, "o", "GitHub answered Could not resolve to a Repository; one more"},
		{"refused", `{"data":null,"errors":[{"message":"API rate limit exceeded"}]}`, "", "GitHub answered API rate limit exceeded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			var body string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				read, _ := io.ReadAll(r.Body)
				body = r.Method + " " + r.URL.Path + " " + string(read)
				_, _ = io.WriteString(w, test.answer)
			}))
			defer server.Close()
			client := testClient(server, map[string]string{"GH_TOKEN": "t"}, nil)

			// Act
			var data struct {
				Viewer struct {
					Login string `json:"login"`
				} `json:"viewer"`
			}
			err := client.GraphQL(context.Background(), "query($n:Int!){viewer{login}}", map[string]any{"n": 3}, &data)

			// Assert
			if body != `POST /graphql {"query":"query($n:Int!){viewer{login}}","variables":{"n":3}}` {
				t.Fatalf("sent %s, want the query and its variables posted to /graphql", body)
			}
			var refused *GraphQLError
			if test.wantErr == "" && err != nil || test.wantErr != "" && (!errors.As(err, &refused) || err.Error() != test.wantErr) {
				t.Fatalf("err = %v, want %q", err, test.wantErr)
			}
			if data.Viewer.Login != test.wantLogin {
				t.Fatalf("read login %q, want %q", data.Viewer.Login, test.wantLogin)
			}
		})
	}
}

// recordingGuard admits requests until refusal is set, and records the
// answers it hears.
type recordingGuard struct {
	refusal error
	heard   []string
}

func (g *recordingGuard) Admit() error { return g.refusal }

func (g *recordingGuard) Heard(status int, header http.Header) {
	g.heard = append(g.heard, http.StatusText(status)+" "+header.Get("X-RateLimit-Remaining"))
}

// A guarded client asks its guard before each request and tells it each
// answer's status and headers; one it refuses is never sent. The guarded
// copy keeps the token its client read.
func TestAGuardHoldsRequestsBackAndHearsEveryAnswer(t *testing.T) {
	// Arrange
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"API rate limit exceeded"}`)
	}))
	defer server.Close()
	runner := &tokenRunner{tokens: []string{"kept"}}
	client := testClient(server, nil, runner)
	guard := &recordingGuard{}
	guarded := client.Guarded(guard)

	// Act
	limited := guarded.Get(context.Background(), "user", &struct{}{})
	guard.refusal = errors.New("waiting out the refusal")
	held := guarded.Get(context.Background(), "user", &struct{}{})
	unguarded := client.Get(context.Background(), "user", &struct{}{})

	// Assert
	if StatusOf(limited) != http.StatusForbidden || held != guard.refusal || StatusOf(unguarded) != http.StatusForbidden {
		t.Fatalf("answers = %v, %v, %v, want the 403, the guard's refusal, then the unguarded client's 403", limited, held, unguarded)
	}
	if requests != 2 || strings.Join(guard.heard, ",") != "Forbidden 0" || len(runner.runs) != 1 {
		t.Fatalf("%d requests sent, guard heard %q, gh ran %d times: want the held one unsent, one answer heard and one token read", requests, guard.heard, len(runner.runs))
	}
}

// Do sends a body as JSON with its method, and returns the answer as it is.
func TestDoSendsTheBodyAsJSON(t *testing.T) {
	// Arrange
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, _ := io.ReadAll(r.Body)
		got = r.Method + " " + r.URL.RequestURI() + " " + r.Header.Get("Content-Type") + " " + string(read)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "raw answer")
	}))
	defer server.Close()
	client := testClient(server, map[string]string{"GH_TOKEN": "t"}, nil)

	// Act
	answer, err := client.Do(context.Background(), http.MethodPost, "repos/o/r/issues?x=1", map[string]any{"title": "t", "labels": []string{"a"}})

	// Assert
	if err != nil || string(answer) != "raw answer" {
		t.Fatalf("Do = %q, %v, want the raw answer", answer, err)
	}
	if want := `POST /repos/o/r/issues?x=1 application/json {"labels":["a"],"title":"t"}`; got != want {
		t.Fatalf("sent %s, want %s", got, want)
	}
}
