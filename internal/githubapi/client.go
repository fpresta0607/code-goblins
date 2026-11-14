// Package githubapi asks GitHub's REST and GraphQL API from inside the
// process, as the account gh is signed in with, for the reads the board
// repeats. Each gh start costs gh, a console host and two tzutil.exe, and on
// 2026-11-03 the board's CI poll alone started gh 686 times in 95 minutes
// (#1610). It reads github.com repositories only, and is the one reading of
// which of them a Git remote names (remote.go).
package githubapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// BaseURL is the root of GitHub's API for github.com.
const BaseURL = "https://api.github.com"

// maxAnswer is the most of one answer the client reads: a workflow job's log
// is the largest answer read, and its checkout lines come first.
const maxAnswer = 64 << 20

// tokenWithin bounds the read of the token gh is signed in with.
const tokenWithin = 30 * time.Second

// refusedWait is how long after GitHub refused the token gh is signed in
// with the client waits before it starts gh auth token again: gh prints a
// revoked or expired token again until it is signed in again, and a board
// that read it anew on each call started gh 65 times in 7.5 minutes (#2003).
const refusedWait = time.Minute

// Client sends requests to GitHub's API. It is used through a pointer, since
// it keeps the token gh is signed in with between calls. Its token is
// GH_TOKEN, else GITHUB_TOKEN, from the environment, as gh reads them for
// github.com, else the one gh auth token prints, read once and kept for the
// client's life until InvalidateCLIToken drops it or GitHub refuses it with
// 401. After a refusal the client reads it again from gh once refusedWait has
// passed, or at once after InvalidateCLIToken, and the calls in between fail
// with a CredentialError and send nothing. A token never reaches a log, an
// error or a file.
type Client struct {
	// BaseURL is where requests go: BaseURL, or a test's own server.
	BaseURL string
	HTTP    *http.Client
	// Commands runs gh auth token when the environment holds no token.
	Commands execx.Runner
	// Getenv reads the environment.
	Getenv func(string) string
	guard  Guard
	token  *keptToken
	// now reads the clock that times the wait after a refusal.
	now func() time.Time
}

// keptToken is the token gh auth token printed, shared by a client and every
// guarded copy of it.
type keptToken struct {
	mu    sync.Mutex
	value string
	// refusedAt is when GitHub last refused the token gh printed, zero
	// since consent.
	refusedAt time.Time
}

// CredentialError means gh could not supply the selected login, or GitHub
// refused the one it supplied less than refusedWait ago.
type CredentialError struct{ cause error }

func (e *CredentialError) Error() string { return e.cause.Error() }
func (e *CredentialError) Unwrap() error { return e.cause }

// New is a client of github.com's API that reads gh's token through
// commands.
func New(commands execx.Runner) *Client {
	return &Client{BaseURL: BaseURL, HTTP: &http.Client{}, Commands: commands, Getenv: os.Getenv, token: &keptToken{}, now: time.Now}
}

// Guard holds requests back while its caller waits GitHub out, and hears
// every answer, whose headers carry the allowance left.
type Guard interface {
	// Admit refuses a request with the error it returns.
	Admit() error
	// Heard hears the status and headers of each answer GitHub gave.
	Heard(status int, header http.Header)
}

// Guarded is the client with every request it sends passed through guard. It
// keeps the same token as the client it was made from.
func (c *Client) Guarded(guard Guard) *Client {
	guarded := *c
	guarded.guard = guard
	return &guarded
}

// Error is an answer GitHub gave with a status other than success: the
// request, the status, and GitHub's own message.
type Error struct {
	Method  string
	Path    string
	Status  int
	Message string
	// Answer is the body GitHub answered with, bounded.
	Answer []byte
	Header http.Header
}

func (e *Error) Error() string {
	return fmt.Sprintf("GitHub answered %s %s with HTTP %d: %s", e.Method, e.Path, e.Status, e.Message)
}

// StatusOf is the HTTP status of the answer that refused err's request, and 0
// when err is no such answer.
func StatusOf(err error) int {
	var refused *Error
	if errors.As(err, &refused) {
		return refused.Status
	}
	return 0
}

// GraphQLError is GitHub's GraphQL API refusing part or all of a query, in
// its own words.
type GraphQLError struct {
	Messages []string
}

func (e *GraphQLError) Error() string {
	return "GitHub answered " + strings.Join(e.Messages, "; ")
}

// Get reads path, relative to the API's root and with its query, into into.
func (c *Client) Get(ctx context.Context, path string, into any) error {
	answer, err := c.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(answer, into); err != nil {
		return fmt.Errorf("GitHub's answer to GET %s could not be read: %w", path, err)
	}
	return nil
}

// GraphQL asks query with variables and reads the data GitHub answered with
// into into. GitHub's own refusal of any of it is a *GraphQLError, returned
// after whatever data it did give is read.
func (c *Client) GraphQL(ctx context.Context, query string, variables map[string]any, into any) error {
	answer, err := c.Do(ctx, http.MethodPost, "graphql", map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	var response struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(answer, &response); err != nil {
		return fmt.Errorf("GitHub's answer to a GraphQL query could not be read: %w", err)
	}
	if len(response.Data) > 0 && string(response.Data) != "null" {
		if err := json.Unmarshal(response.Data, into); err != nil {
			return fmt.Errorf("GitHub's answer to a GraphQL query could not be read: %w", err)
		}
	}
	if len(response.Errors) > 0 {
		refused := &GraphQLError{}
		for _, problem := range response.Errors {
			refused.Messages = append(refused.Messages, problem.Message)
		}
		return refused
	}
	return nil
}

// Do sends method to path, relative to the API's root and with its query,
// with body as JSON unless it is nil, and returns what GitHub answered.
func (c *Client) Do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var content []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		content = encoded
	}
	token, isKept, err := c.readToken(ctx)
	if err != nil {
		return nil, &CredentialError{cause: err}
	}
	answer, err := c.send(ctx, token, method, path, content)
	if isKept && StatusOf(err) == http.StatusUnauthorized {
		c.dropToken(token)
	}
	return answer, err
}

func (c *Client) send(ctx context.Context, token, method, path string, content []byte) ([]byte, error) {
	if c.guard != nil {
		if err := c.guard.Admit(); err != nil {
			return nil, err
		}
	}
	path = strings.TrimPrefix(path, "/")
	var reader io.Reader
	if content != nil {
		reader = bytes.NewReader(content)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.BaseURL, "/")+"/"+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "code-goblins")
	if content != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("ask GitHub %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	if c.guard != nil {
		c.guard.Heard(response.StatusCode, response.Header)
	}
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxAnswer+1))
	if err != nil {
		return nil, fmt.Errorf("read GitHub's answer to %s %s: %w", method, path, err)
	}
	if len(answer) > maxAnswer {
		return nil, fmt.Errorf("GitHub's answer to %s %s is longer than %d bytes", method, path, maxAnswer)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, &Error{Method: method, Path: path, Status: response.StatusCode, Message: message(response.Status, answer), Answer: bounded(answer, 4096), Header: response.Header.Clone()}
	}
	return answer, nil
}

// message is GitHub's own message in a refusal, else the status line.
func message(status string, answer []byte) string {
	var refusal struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(answer, &refusal) == nil && strings.TrimSpace(refusal.Message) != "" {
		return string(bounded([]byte(strings.TrimSpace(refusal.Message)), 300))
	}
	return status
}

func bounded(text []byte, limit int) []byte {
	if len(text) > limit {
		return text[:limit]
	}
	return text
}

// readToken is the token to send, and whether it is the one kept from gh.
func (c *Client) readToken(ctx context.Context) (token string, isKept bool, err error) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if value := strings.TrimSpace(c.Getenv(name)); value != "" {
			return value, false, nil
		}
	}
	c.token.mu.Lock()
	defer c.token.mu.Unlock()
	if c.token.value != "" {
		return c.token.value, true, nil
	}
	if again := c.token.refusedAt.Add(refusedWait); c.now().Before(again) {
		return "", false, fmt.Errorf("GitHub refused the token gh is signed in with; gh is asked for it again after %s, or sign gh in again with gh auth login", again.UTC().Format(time.RFC3339))
	}
	ctx, cancel := context.WithTimeout(ctx, tokenWithin)
	defer cancel()
	result, err := c.Commands.Run(ctx, execx.Request{Name: "gh", Args: []string{"auth", "token", "--hostname", "github.com"}})
	if err != nil {
		return "", false, fmt.Errorf("read the token gh is signed in with: %w", err)
	}
	if result.ExitCode != 0 {
		return "", false, fmt.Errorf("read the token gh is signed in with: gh auth token exited %d: %s", result.ExitCode, bounded([]byte(strings.TrimSpace(string(result.Stderr))), 300))
	}
	value := strings.TrimSpace(string(result.Stdout))
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return "", false, errors.New("read the token gh is signed in with: gh auth token printed no token; sign gh in with gh auth login")
	}
	c.token.value = value
	return value, true, nil
}

// dropToken forgets the kept token GitHub refused and starts the wait before
// gh is asked again, unless another call has read a newer one since.
func (c *Client) dropToken(refused string) {
	c.token.mu.Lock()
	defer c.token.mu.Unlock()
	if c.token.value == refused {
		c.token.value = ""
		c.token.refusedAt = c.now()
	}
}

// CredentialSource names the selected credential, never its value.
func (c *Client) CredentialSource() string {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if strings.TrimSpace(c.Getenv(name)) != "" {
			return name
		}
	}
	return "gh"
}

// InvalidateCLIToken makes every guarded copy re-read gh's persisted login
// after user consent, or after the user retries a billing reading GitHub
// refused, at once even after a refusal. Environment credentials retain their
// precedence.
func (c *Client) InvalidateCLIToken() {
	c.token.mu.Lock()
	defer c.token.mu.Unlock()
	c.token.value = ""
	c.token.refusedAt = time.Time{}
}
