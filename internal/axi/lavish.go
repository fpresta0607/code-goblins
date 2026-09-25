package axi

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Lavish invokes lavish-axi, the Overlord's review surface, to open a page
// and to wait for his feedback on it. Its output is TOON: only the session
// fields cfo acts on are read, and a poll's whole output is kept for the CFO.
type Lavish struct {
	Commands execx.Runner
}

// PagePoll is one poll of a page.
type PagePoll struct {
	// Status is the session status lavish-axi reported: feedback, ended,
	// browser_disconnected, or waiting when the timeout passed first.
	Status string
	// Ended says the Overlord ended the session along with this feedback.
	Ended bool
	// Output is the poll's whole output, which the CFO reads.
	Output string
}

// Open opens or resumes the page's review session without opening a browser,
// and returns the page's address.
func (l Lavish) Open(ctx context.Context, file string) (string, error) {
	result, err := command(ctx, l.Commands, "lavish-axi "+file+" --no-open", "lavish-axi", file, "--no-open")
	if err != nil {
		return "", err
	}
	url := sessionField(string(result.Stdout), "url")
	if url == "" {
		return "", errors.New("axi: lavish-axi reported no address for " + file)
	}
	return url, nil
}

// Poll waits up to timeout for the Overlord's feedback on the page. Delivery
// consumes the feedback, so a page must have one poller.
func (l Lavish) Poll(ctx context.Context, file string, timeout time.Duration) (PagePoll, error) {
	result, err := command(ctx, l.Commands, "lavish-axi poll "+file, "lavish-axi", "poll", file, "--timeout-ms", strconv.FormatInt(timeout.Milliseconds(), 10))
	if err != nil {
		return PagePoll{}, err
	}
	output := string(result.Stdout)
	status := sessionField(output, "status")
	if status == "" {
		return PagePoll{}, errors.New("axi: lavish-axi poll reported no session status for " + file)
	}
	return PagePoll{Status: status, Ended: sessionField(output, "session_ended") == "true", Output: output}, nil
}

// sessionField reads one scalar field of the top-level TOON `session:`
// object, unquoting a quoted value.
func sessionField(output, name string) string {
	in := false
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if !in {
			in = line == "session:"
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			return ""
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ": ")
		if !ok || key != name {
			continue
		}
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		return value
	}
	return ""
}
