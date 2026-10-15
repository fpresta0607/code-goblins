// Package cirunner is the desktop CI runner's service: it runs one GitHub
// Actions job at a time for one repository, in a virtual machine put back to
// a clean checkpoint before every job, as the heavy line's turns allow.
package cirunner

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RunnerPrefix starts the name of every runner the service registers, and the
// service deletes only runners named so.
const RunnerPrefix = "cg-desktop-"

// Labels are the desktop runner's labels. A job is the service's when it asks
// for cg-desktop and for nothing else the runner lacks.
var Labels = []string{"self-hosted", "linux", "x64", "cg-desktop"}

// allowedCalls are every GitHub call the service may make, as method and path
// pattern. {owner} and {repo} are the repository the service is set up for,
// {installation} its App's installation, and {id} a number. Nothing else is
// ever asked for.
var allowedCalls = []string{
	"POST /app/installations/{installation}/access_tokens",
	"GET /repos/{owner}/{repo}",
	"GET /repos/{owner}/{repo}/actions/runs",
	"GET /repos/{owner}/{repo}/actions/runs/{id}/jobs",
	"POST /repos/{owner}/{repo}/actions/runners/generate-jitconfig",
	"GET /repos/{owner}/{repo}/actions/runners",
	"GET /repos/{owner}/{repo}/actions/runners/{id}",
	"DELETE /repos/{owner}/{repo}/actions/runners/{id}",
}

// maxAnswer is the most of any one answer from GitHub the service reads.
const maxAnswer = 1 << 20

// GitHub is the service's GitHub App, installed on the one repository whose
// jobs the service runs. It is used through a pointer, as it keeps the
// installation's token between calls.
type GitHub struct {
	Client         *http.Client
	Base           string
	AppID          int64
	InstallationID int64
	Key            *rsa.PrivateKey
	Owner          string
	Repo           string
	Now            func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// Runner is a runner registered for one job, with the configuration the VM's
// runner starts from.
type Runner struct {
	ID     int64
	Name   string
	Config string
}

// Pending is the desktop job that has waited longest for a runner, when one
// waits.
type Pending struct {
	IsWaiting bool
	RunID     int64
	Job       string
	Queued    time.Time
}

// statusError is an answer from GitHub other than success.
type statusError struct {
	call   string
	status int
}

func (e statusError) Error() string {
	return fmt.Sprintf("cirunner: GitHub answered %s with HTTP %d", e.call, e.status)
}

// QueuedJob finds the desktop job queued longest, among the newest runs in
// progress or queued.
func (g *GitHub) QueuedJob(ctx context.Context) (Pending, error) {
	var oldest Pending
	for _, status := range []string{"in_progress", "queued"} {
		var runs struct {
			Runs []struct {
				ID int64 `json:"id"`
			} `json:"workflow_runs"`
		}
		if err := g.do(ctx, http.MethodGet, g.repoPath("/actions/runs"), url.Values{"status": {status}, "per_page": {"20"}}, nil, &runs); err != nil {
			return Pending{}, err
		}
		for _, run := range runs.Runs {
			var jobs struct {
				Jobs []struct {
					Name    string    `json:"name"`
					Status  string    `json:"status"`
					Labels  []string  `json:"labels"`
					Created time.Time `json:"created_at"`
				} `json:"jobs"`
			}
			if err := g.do(ctx, http.MethodGet, g.repoPath(fmt.Sprintf("/actions/runs/%d/jobs", run.ID)), url.Values{"filter": {"latest"}, "per_page": {"100"}}, nil, &jobs); err != nil {
				return Pending{}, err
			}
			for _, job := range jobs.Jobs {
				if job.Status != "queued" || !isDesktopJob(job.Labels) {
					continue
				}
				if !oldest.IsWaiting || job.Created.Before(oldest.Queued) {
					oldest = Pending{IsWaiting: true, RunID: run.ID, Job: printable(job.Name), Queued: job.Created}
				}
			}
		}
	}
	return oldest, nil
}

// maxJobName is the most of a job's name the service passes on.
const maxJobName = 100

// printable is a job's name as the service passes it on: its printable
// ASCII alone, cut at maxJobName. A workflow names its jobs, and the fleet
// puts the name in the line it shows and in its command line.
func printable(name string) string {
	kept := make([]byte, 0, min(len(name), maxJobName))
	for i := 0; i < len(name) && len(kept) < maxJobName; i++ {
		if name[i] >= ' ' && name[i] <= '~' {
			kept = append(kept, name[i])
		}
	}
	return string(kept)
}

// isDesktopJob says whether a job asking for labels is the service's: it asks
// for cg-desktop, and for nothing the desktop runner lacks, so the runner can
// take it.
func isDesktopJob(labels []string) bool {
	isDesktop := false
	for _, label := range labels {
		if !slices.ContainsFunc(Labels, func(own string) bool { return strings.EqualFold(own, label) }) {
			return false
		}
		isDesktop = isDesktop || strings.EqualFold(label, "cg-desktop")
	}
	return isDesktop
}

// RegisterRunner registers a runner named name for one job on the desktop
// labels.
func (g *GitHub) RegisterRunner(ctx context.Context, name string) (Runner, error) {
	var answer struct {
		Runner struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"runner"`
		Config string `json:"encoded_jit_config"`
	}
	request := map[string]any{"name": name, "runner_group_id": 1, "labels": Labels, "work_folder": "_work"}
	if err := g.do(ctx, http.MethodPost, g.repoPath("/actions/runners/generate-jitconfig"), nil, request, &answer); err != nil {
		return Runner{}, err
	}
	if answer.Runner.ID == 0 || answer.Config == "" {
		return Runner{}, errors.New("cirunner: GitHub registered no runner with a configuration")
	}
	return Runner{ID: answer.Runner.ID, Name: answer.Runner.Name, Config: answer.Config}, nil
}

// IsPrivate says whether the service's repository is private. A
// self-hosted runner must never serve a public repository, whose pull
// requests anyone may open.
func (g *GitHub) IsPrivate(ctx context.Context) (bool, error) {
	var answer struct {
		Private *bool `json:"private"`
	}
	if err := g.do(ctx, http.MethodGet, g.repoPath(""), nil, nil, &answer); err != nil {
		return false, err
	}
	if answer.Private == nil {
		return false, errors.New("cirunner: GitHub did not say whether the repository is private")
	}
	return *answer.Private, nil
}

// RunnerState says whether the runner id is running a job and whether it is
// online.
func (g *GitHub) RunnerState(ctx context.Context, id int64) (isBusy, isOnline bool, err error) {
	var answer struct {
		Status string `json:"status"`
		Busy   bool   `json:"busy"`
	}
	if err := g.do(ctx, http.MethodGet, g.repoPath(fmt.Sprintf("/actions/runners/%d", id)), nil, nil, &answer); err != nil {
		return false, false, err
	}
	return answer.Busy, answer.Status == "online", nil
}

// DeleteRunner deletes the runner id. A runner already gone is deleted.
func (g *GitHub) DeleteRunner(ctx context.Context, id int64) error {
	err := g.do(ctx, http.MethodDelete, g.repoPath(fmt.Sprintf("/actions/runners/%d", id)), nil, nil, nil)
	if status := (statusError{}); errors.As(err, &status) && status.status == http.StatusNotFound {
		return nil
	}
	return err
}

// maxRunnerPages bounds the pages of the repository's runners, a hundred
// each, the service reads to find its own.
const maxRunnerPages = 50

// DeleteOwnRunners deletes every runner the service registered, as named with
// RunnerPrefix, and no other, on every page of the repository's runners.
func (g *GitHub) DeleteOwnRunners(ctx context.Context) error {
	var own []int64
	for page := 1; page <= maxRunnerPages; page++ {
		var answer struct {
			Runners []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"runners"`
		}
		if err := g.do(ctx, http.MethodGet, g.repoPath("/actions/runners"), url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}, nil, &answer); err != nil {
			return err
		}
		for _, runner := range answer.Runners {
			if strings.HasPrefix(runner.Name, RunnerPrefix) {
				own = append(own, runner.ID)
			}
		}
		if len(answer.Runners) < 100 {
			break
		}
	}
	var failures error
	for _, id := range own {
		failures = errors.Join(failures, g.DeleteRunner(ctx, id))
	}
	return failures
}

func (g *GitHub) repoPath(path string) string {
	return "/repos/" + g.Owner + "/" + g.Repo + path
}

// do makes a pinned call with the installation's token, and refuses any
// other before anything is sent.
func (g *GitHub) do(ctx context.Context, method, path string, query url.Values, body, into any) error {
	if _, isPinned := g.pinned(method + " " + path); !isPinned {
		return fmt.Errorf("cirunner: %s %s is not among the service's pinned GitHub calls", method, path)
	}
	token, err := g.installationToken(ctx)
	if err != nil {
		return err
	}
	return g.send(ctx, token, method, path, query, body, into)
}

// installationToken is the installation's token for the repository alone,
// asked for anew five minutes before it expires.
func (g *GitHub) installationToken(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && g.Now().Before(g.expires.Add(-5*time.Minute)) {
		return g.token, nil
	}
	appToken, err := g.appToken()
	if err != nil {
		return "", err
	}
	var answer struct {
		Token   string    `json:"token"`
		Expires time.Time `json:"expires_at"`
	}
	scope := map[string]any{"repositories": []string{g.Repo}, "permissions": map[string]string{"administration": "write", "actions": "read"}}
	if err := g.send(ctx, appToken, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", g.InstallationID), nil, scope, &answer); err != nil {
		return "", err
	}
	if answer.Token == "" {
		return "", errors.New("cirunner: GitHub gave the installation no token")
	}
	g.token, g.expires = answer.Token, answer.Expires
	return g.token, nil
}

// appToken is the App's own token, which GitHub takes for ten minutes at
// most, issued a minute early for a clock behind GitHub's.
func (g *GitHub) appToken() (string, error) {
	now := g.Now().Unix()
	claims, err := json.Marshal(map[string]int64{"iat": now - 60, "exp": now + 540, "iss": g.AppID})
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, g.Key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("cirunner: sign the App's token: %w", err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// send makes a call do pinned, or the installation's token's, with token,
// and decodes the answer into into, unless into is nil. It follows no
// redirect, so no answer carries a call past the pins.
func (g *GitHub) send(ctx context.Context, token, method, path string, query url.Values, body, into any) error {
	call := method + " " + path
	target := g.Base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var content io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		content = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, content)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "code-goblins-ci-runner")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := *g.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("cirunner: %s: %w", call, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
	default:
		return statusError{call: call, status: response.StatusCode}
	}
	if into == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxAnswer)).Decode(into); err != nil {
		return fmt.Errorf("cirunner: GitHub's answer to %s could not be read: %w", call, err)
	}
	return nil
}

// pinned is the pattern among allowedCalls that call, a method and a path,
// matches, when one does.
func (g *GitHub) pinned(call string) (string, bool) {
	method, path, _ := strings.Cut(call, " ")
	asked := strings.Split(path, "/")
	for _, allowed := range allowedCalls {
		allowedMethod, pattern, _ := strings.Cut(allowed, " ")
		parts := strings.Split(pattern, "/")
		if allowedMethod == method && len(parts) == len(asked) && g.matches(parts, asked) {
			return allowed, true
		}
	}
	return "", false
}

func (g *GitHub) matches(pattern, asked []string) bool {
	for i, part := range pattern {
		switch part {
		case "{owner}":
			if asked[i] != g.Owner {
				return false
			}
		case "{repo}":
			if asked[i] != g.Repo {
				return false
			}
		case "{installation}":
			if asked[i] != fmt.Sprint(g.InstallationID) {
				return false
			}
		case "{id}":
			if asked[i] == "" || strings.Trim(asked[i], "0123456789") != "" {
				return false
			}
		default:
			if asked[i] != part {
				return false
			}
		}
	}
	return true
}
