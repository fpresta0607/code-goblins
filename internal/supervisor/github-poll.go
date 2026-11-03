package supervisor

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// githubHold holds a repository's reads of GitHub back while it waits out a
// refusal or an exhausted allowance, which each answer GitHub gives the
// repository's reads may start: the hold refused the train's read of the
// account the fleet works as on 2026-10-17 (#1085) and the overlap read's
// page (#1160). It guards both GitHub's API (githubapi.Guard) and the gh the
// poll still starts for what a merge train writes (githubPollRunner).
type githubHold struct {
	state *fleetWakes
	repo  string
	now   func() time.Time
}

// Admit refuses a read while the repository waits GitHub out.
func (h githubHold) Admit() error {
	if h.now().Before(h.state.BackOff[h.repo]) {
		return fmt.Errorf("GitHub reads for %s wait out a refusal or exhausted allowance", h.repo)
	}
	return nil
}

// Heard holds the repository's reads back after GitHub refused one, 403 or
// 429, or said its allowance is spent.
func (h githubHold) Heard(status int, header http.Header) {
	h.hold(githubBackOff(status == http.StatusForbidden || status == http.StatusTooManyRequests, header, h.now()))
}

func (h githubHold) hold(until time.Time) {
	if until.IsZero() {
		return
	}
	if h.state.BackOff == nil {
		h.state.BackOff = map[string]time.Time{}
	}
	h.state.BackOff[h.repo] = until
}

// githubPollRunner runs the poll's commands, holding gh back as the hold
// says, and holds the repository's reads back once gh says GitHub refused it.
type githubPollRunner struct {
	commands execx.Runner
	hold     githubHold
}

func (r githubPollRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "gh" {
		return r.commands.Run(ctx, request)
	}
	if err := r.hold.Admit(); err != nil {
		return execx.Result{}, err
	}
	result, err := r.commands.Run(ctx, request)
	isRefused := githubRefusal.Match(result.Stderr) || strings.Contains(strings.ToLower(string(result.Stderr)), "rate limit")
	r.hold.hold(githubBackOff(isRefused, http.Header{}, r.hold.now()))
	return result, err
}

// githubRefusal is gh saying GitHub answered 403 or 429.
var githubRefusal = regexp.MustCompile(`(?i)\bHTTP(?:/[0-9.]+)?\s+(403|429)\b`)

// githubBackOff is when a repository's reads may go to GitHub again after an
// answer that was refused, or whose header says the allowance is spent: the
// reset the header names, or the later Retry-After, else an hour from now.
// It is zero for any other answer.
func githubBackOff(isRefused bool, header http.Header, now time.Time) time.Time {
	isExhausted := header.Get("X-RateLimit-Remaining") == "0"
	if !isRefused && !isExhausted {
		return time.Time{}
	}
	var until time.Time
	if reset, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil && time.Unix(reset, 0).After(now) {
		until = time.Unix(reset, 0)
	}
	if retry := header.Get("Retry-After"); retry != "" {
		var retryAt time.Time
		if seconds, err := strconv.ParseInt(retry, 10, 32); err == nil && seconds > 0 {
			retryAt = now.Add(time.Duration(seconds) * time.Second)
		} else if stamp, err := http.ParseTime(retry); err == nil {
			retryAt = stamp
		}
		if retryAt.After(until) && retryAt.After(now) {
			until = retryAt
		}
	}
	if until.IsZero() {
		until = now.Add(time.Hour)
	}
	return until
}
