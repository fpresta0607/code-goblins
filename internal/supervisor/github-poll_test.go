package supervisor

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type pollResponseRunner struct {
	result execx.Result
	calls  []execx.Request
	ended  func()
}

func (runner *pollResponseRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	runner.calls = append(runner.calls, request)
	if runner.ended != nil {
		runner.ended()
	}
	return runner.result, nil
}

func TestGitHubPollRunnerPreservesBodiesWhileObservingAllowance(t *testing.T) {
	const repo = "C:/dev/project"
	started := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		body string
		args []string
	}{
		{name: "raw GraphQL JSON", body: `{"data":{"repository":{"nameWithOwner":"owner/project"}}}`, args: []string{"api", "graphql", "-f", "query=example"}},
		{name: "jq file lines", body: "frontend/dialog.ts\ninternal/tickets/github.go\n", args: []string{"api", "repos/owner/project/pulls/1/files?per_page=100&page=1", "--jq", ".[].filename"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			now := started
			commands := &pollResponseRunner{result: execx.Result{Stdout: []byte("HTTP/2.0 200 OK\r\nX-RateLimit-Remaining: 0\r\nRetry-After: 600\r\n\r\n" + testCase.body)}, ended: func() { now = started.Add(40 * time.Second) }}
			state := &fleetWakes{}
			runner := githubPollRunner{commands: commands, state: state, repo: repo, now: func() time.Time { return now }}
			request := execx.Request{Name: "gh", Args: testCase.args}

			result, err := runner.Run(context.Background(), request)

			if err != nil || result.ExitCode != 0 || string(result.Stdout) != testCase.body {
				t.Fatalf("caller body = %q, exit %d, error %v, want exact %q", result.Stdout, result.ExitCode, err, testCase.body)
			}
			if len(commands.calls) != 1 || !slices.Contains(commands.calls[0].Args, "--include") || slices.Contains(request.Args, "--include") {
				t.Fatalf("response headers were not requested independently of caller args: %+v", commands.calls)
			}
			if until := state.BackOff[repo]; !until.Equal(started.Add(640 * time.Second)) {
				t.Fatalf("backoff = %s, want ten minutes after response receipt", until)
			}
			if _, err := runner.Run(context.Background(), request); err == nil || len(commands.calls) != 1 {
				t.Fatalf("exhausted response did not stop the next page: calls %d, error %v", len(commands.calls), err)
			}
			other := githubPollRunner{commands: commands, state: state, repo: "C:/dev/other", now: func() time.Time { return now }}
			if _, err := other.Run(context.Background(), request); err != nil || len(commands.calls) != 2 {
				t.Fatalf("one repository blocked another: calls %d, error %v", len(commands.calls), err)
			}
		})
	}
}

func TestGitHubPollRunnerKeepsExplicitIncludeOutput(t *testing.T) {
	output := "HTTP/2.0 200 OK\r\nX-RateLimit-Remaining: 100\r\n\r\n{}"
	commands := &pollResponseRunner{result: execx.Result{Stdout: []byte(output)}}
	runner := githubPollRunner{commands: commands, state: &fleetWakes{}, repo: "repo", now: func() time.Time { return time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC) }}
	request := execx.Request{Name: "gh", Args: []string{"api", "graphql", "--include"}}

	result, err := runner.Run(context.Background(), request)

	if err != nil || string(result.Stdout) != output || !slices.Equal(commands.calls[0].Args, request.Args) {
		t.Fatalf("explicit header consumer changed: output %q, calls %v, error %v", result.Stdout, commands.calls, err)
	}
}

func TestGitHubPollRunnerLeavesNonAPICommandsUnchanged(t *testing.T) {
	for _, request := range []execx.Request{{Name: "git", Args: []string{"status"}}, {Name: "gh", Args: []string{"pr", "list", "--json", "number"}}} {
		t.Run(fmt.Sprint(request.Name, request.Args), func(t *testing.T) {
			commands := &pollResponseRunner{result: execx.Result{Stdout: []byte("[]")}}
			runner := githubPollRunner{commands: commands, state: &fleetWakes{}, repo: "repo", now: time.Now}
			result, err := runner.Run(context.Background(), request)
			if err != nil || string(result.Stdout) != "[]" || !slices.Equal(commands.calls[0].Args, request.Args) {
				t.Fatalf("non-API command changed: output %q, calls %v, error %v", result.Stdout, commands.calls, err)
			}
		})
	}
}

func TestGitHubPollRunnerDoesNotPassMalformedInjectedHeadersAsJSON(t *testing.T) {
	commands := &pollResponseRunner{result: execx.Result{Stdout: []byte("HTTP/2.0 200 OK\r\nnot a header\r\n\r\n{}")}}
	runner := githubPollRunner{commands: commands, state: &fleetWakes{}, repo: "repo", now: time.Now}

	_, err := runner.Run(context.Background(), execx.Request{Name: "gh", Args: []string{"api", "graphql"}})

	if err == nil {
		t.Fatal("malformed injected response headers were passed to a JSON consumer")
	}
}
