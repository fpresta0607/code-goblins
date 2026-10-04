package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type githubPollRunner struct {
	commands execx.Runner
	state    *fleetWakes
	repo     string
	now      func() time.Time
}

func (r githubPollRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "gh" && r.now().Before(r.state.BackOff[r.repo]) {
		return execx.Result{}, fmt.Errorf("GitHub reads for %s wait out a refusal or exhausted allowance", r.repo)
	}
	hasInjectedHeaders := request.Name == "gh" && len(request.Args) > 0 && request.Args[0] == "api" && !slices.Contains(request.Args, "--include") && !slices.Contains(request.Args, "-i")
	if hasInjectedHeaders {
		request.Args = append(slices.Clone(request.Args), "--include")
	}
	result, err := r.commands.Run(ctx, request)
	if request.Name == "gh" {
		if until := githubBackOff(result, r.now()); !until.IsZero() {
			if r.state.BackOff == nil {
				r.state.BackOff = map[string]time.Time{}
			}
			r.state.BackOff[r.repo] = until
		}
	}
	if hasInjectedHeaders && err == nil {
		_, body, headerErr := githubResponse(result.Stdout)
		if headerErr != nil {
			return result, fmt.Errorf("read GitHub response headers: %w", headerErr)
		}
		result.Stdout = body
	}
	return result, err
}

var githubRefusal = regexp.MustCompile(`(?i)\bHTTP(?:/[0-9.]+)?\s+(403|429)\b`)

func githubBackOff(result execx.Result, now time.Time) time.Time {
	headers, _, _ := githubResponse(result.Stdout)
	stderr := strings.ToLower(string(result.Stderr))
	isExhausted := headers.Get("X-RateLimit-Remaining") == "0"
	isRefused := githubRefusal.Match(result.Stderr) || bytes.HasPrefix(result.Stdout, []byte("HTTP/")) && githubRefusal.Match(result.Stdout) || strings.Contains(stderr, "rate limit")
	if !isRefused && !isExhausted {
		return time.Time{}
	}
	var until time.Time
	if reset, err := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64); err == nil && time.Unix(reset, 0).After(now) {
		until = time.Unix(reset, 0)
	}
	if retry := headers.Get("Retry-After"); retry != "" {
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

// gh api --include prints response headers above an already decoded body.
func githubResponse(output []byte) (http.Header, []byte, error) {
	if !bytes.HasPrefix(output, []byte("HTTP/")) {
		return http.Header{}, output, nil
	}
	reader := bufio.NewReader(bytes.NewReader(output))
	if _, err := reader.ReadString('\n'); err != nil {
		return nil, nil, err
	}
	headers, err := textproto.NewReader(reader).ReadMIMEHeader()
	if err != nil {
		return nil, nil, err
	}
	body, err := io.ReadAll(reader)
	return http.Header(headers), body, err
}
