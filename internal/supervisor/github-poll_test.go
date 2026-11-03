package supervisor

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/githubtest"
)

// allowanceGitHub answers every read of the account with status and header,
// and ended runs as each answer is given, as the time a read takes passes.
type allowanceGitHub struct {
	status int
	header http.Header
	reads  int
	ended  func()
}

func (g *allowanceGitHub) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	g.reads++
	for name, values := range g.header {
		w.Header()[http.CanonicalHeaderKey(name)] = values
	}
	if g.ended != nil {
		g.ended()
	}
	if g.status != http.StatusOK {
		githubtest.Refuse(w, g.status, "refused")
		return
	}
	githubtest.Write(w, http.StatusOK, map[string]string{"login": "o"})
}

// Each answer GitHub gives holds the repository's next reads as its status
// and its rate limit headers say, from the moment the answer arrived, and
// holds no other repository's.
func TestGitHubHoldWaitsOutWhatEachAnswerSays(t *testing.T) {
	started := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		status int
		header http.Header
		want   time.Duration
	}{
		{"spent allowance with Retry-After", http.StatusOK, http.Header{"X-RateLimit-Remaining": {"0"}, "Retry-After": {"600"}}, 640 * time.Second},
		{"spent allowance until its reset", http.StatusOK, http.Header{"X-RateLimit-Remaining": {"0"}, "X-RateLimit-Reset": {"1791122400"}}, 4 * time.Hour},
		{"403 with no header", http.StatusForbidden, nil, 40*time.Second + time.Hour},
		{"429 with a Retry-After date", http.StatusTooManyRequests, http.Header{"Retry-After": {"Sun, 04 Oct 2026 10:30:00 GMT"}}, 30 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			now := started
			github := &allowanceGitHub{status: test.status, header: test.header, ended: func() { now = started.Add(40 * time.Second) }}
			client := githubtest.Client(github)
			state := &fleetWakes{}
			clock := func() time.Time { return now }
			hold := githubHold{state: state, repo: "C:/dev/project", now: clock}
			var account struct {
				Login string `json:"login"`
			}

			// Act
			_ = client.Guarded(hold).Get(context.Background(), "user", &account)

			// Assert
			if until := state.BackOff["C:/dev/project"]; !until.Equal(started.Add(test.want)) {
				t.Fatalf("backoff = %s, want %s after the read started", until, test.want)
			}
			if err := client.Guarded(hold).Get(context.Background(), "user", &account); err == nil || github.reads != 1 {
				t.Fatalf("the held repository read GitHub again: reads %d, error %v", github.reads, err)
			}
			github.status, github.header = http.StatusOK, nil
			other := githubHold{state: state, repo: "C:/dev/other", now: clock}
			if err := client.Guarded(other).Get(context.Background(), "user", &account); err != nil || github.reads != 2 || account.Login != "o" {
				t.Fatalf("one repository held another: reads %d, account %q, error %v", github.reads, account.Login, err)
			}
		})
	}
}

// An answer that neither refuses nor spends the allowance holds nothing.
func TestGitHubHoldLetsAnOrdinaryAnswerThrough(t *testing.T) {
	// Arrange
	github := &allowanceGitHub{status: http.StatusOK, header: http.Header{"X-RateLimit-Remaining": {"4999"}}}
	state := &fleetWakes{}
	hold := githubHold{state: state, repo: "C:/dev/project", now: time.Now}
	client := githubtest.Client(github).Guarded(hold)
	var account struct {
		Login string `json:"login"`
	}

	// Act
	for range 2 {
		if err := client.Get(context.Background(), "user", &account); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	if github.reads != 2 || len(state.BackOff) != 0 {
		t.Fatalf("reads %d, backoff %+v, want both read and nothing held", github.reads, state.BackOff)
	}
}

type pollResponseRunner struct {
	result execx.Result
	calls  []execx.Request
}

func (runner *pollResponseRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	runner.calls = append(runner.calls, request)
	return runner.result, nil
}

// What a merge train still writes through gh holds the repository when gh
// says GitHub refused it, as a refused read of the API does, and not
// otherwise.
func TestGitHubPollRunnerHoldsTheRepositoryAfterARefusedWrite(t *testing.T) {
	for _, test := range []struct {
		name, stderr string
		isHeld       bool
	}{
		{"forbidden", "gh: Resource not accessible by integration (HTTP 403)", true},
		{"too many requests", "gh: HTTP 429: too many requests", true},
		{"spent allowance", "API rate limit exceeded for user ID 1", true},
		{"not found", "gh: Not Found (HTTP 404)", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			commands := &pollResponseRunner{result: execx.Result{ExitCode: 1, Stderr: []byte(test.stderr)}}
			runner := githubPollRunner{commands: commands, hold: githubHold{state: &fleetWakes{}, repo: "C:/dev/project", now: time.Now}}
			merge := execx.Request{Name: "gh", Args: []string{"pr", "merge", "https://github.com/o/r/pull/1"}}

			// Act
			_, _ = runner.Run(context.Background(), merge)
			_, err := runner.Run(context.Background(), merge)

			// Assert
			if isHeld := err != nil && len(commands.calls) == 1; isHeld != test.isHeld {
				t.Fatalf("gh ran %d times, second error %v, want held %t", len(commands.calls), err, test.isHeld)
			}
		})
	}
}

// A command that is not gh runs whatever GitHub said.
func TestGitHubPollRunnerLeavesOtherCommandsToRun(t *testing.T) {
	// Arrange
	commands := &pollResponseRunner{result: execx.Result{Stdout: []byte("main\n")}}
	state := &fleetWakes{BackOff: map[string]time.Time{"C:/dev/project": time.Now().Add(time.Hour)}}
	runner := githubPollRunner{commands: commands, hold: githubHold{state: state, repo: "C:/dev/project", now: time.Now}}

	// Act
	result, err := runner.Run(context.Background(), execx.Request{Name: "git", Args: []string{"branch", "--show-current"}})

	// Assert
	if err != nil || string(result.Stdout) != "main\n" || len(commands.calls) != 1 {
		t.Fatalf("git under a hold = %q, %v after %d calls, want it run", result.Stdout, err, len(commands.calls))
	}
}

// recordingRunner runs each command with commands, recording it.
type recordingRunner struct {
	commands execx.Runner
	requests []execx.Request
}

func (r *recordingRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	return r.commands.Run(ctx, request)
}

// The CI poll of a GitHub repository reads its pull requests' checks, its
// main's push runs and their failed jobs from GitHub's API in process, and
// starts no gh: on 2026-10-30 the poll started gh 686 times in an hour
// (#1610).
func TestTheCIPollOfAGitHubRepositoryStartsNoGh(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.pulls, forge.runs, forge.jobs = "feat/wakes", pendingChecks, redGoRun, `{"jobs":[{"name":"test","conclusion":"failure"}]}`
	commands := &recordingRunner{commands: forge}
	service.Options.CI = commands
	service.Options.GitHub = githubtest.Client(forge)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	// Act
	woke := pollForge(t, service, h, &now)
	forge.pulls = passedChecks
	woke = append(woke, pollForge(t, service, h, &now)...)

	// Assert
	for _, request := range commands.requests {
		if request.Name == "gh" {
			t.Fatalf("the CI poll started gh %v", request.Args)
		}
	}
	if len(commands.requests) == 0 || forge.githubCalls == 0 || forge.listCalls != 2 {
		t.Fatalf("commands %d, GitHub reads %d, pull request listings %d, want the poll to read the checkout and GitHub", len(commands.requests), forge.githubCalls, forge.listCalls)
	}
	var details []string
	for _, record := range woke {
		details = append(details, record.Detail)
	}
	for _, want := range []string{"PR #209 (feat/wakes)", "all 3 passed", "main's push CI is red", "run 36740825611"} {
		if !strings.Contains(strings.Join(details, "\n"), want) {
			t.Errorf("wakes %q lack %q, which only the stand-in GitHub answered", details, want)
		}
	}
}
