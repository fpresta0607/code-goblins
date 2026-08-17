package auth

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Browser drives an interactive login page far enough to finish a handshake
// that only needs a click, using whatever browser session the user already
// has. It never types a credential: if a page asks for one, that is a human's
// job and the service lands in the consolidated login request instead.
type Browser interface {
	// Confirm opens url and clicks the first control whose label matches one
	// of labels. The returned note describes what happened, for the report.
	Confirm(ctx context.Context, url string, labels []string) (string, error)
}

// BrowserTimeout bounds the whole browser attempt. A stalled OAuth page must
// not hold up the rest of a preflight.
const BrowserTimeout = 45 * time.Second

// ChromeBrowser drives chrome-devtools-axi, the fleet's browser AXI.
type ChromeBrowser struct {
	Runner  execx.Runner
	Timeout time.Duration
}

// snapshotRef matches one interactive element in a chrome-devtools-axi
// accessibility snapshot: `uid=g12:3_4 button "Authorize"`.
var snapshotRef = regexp.MustCompile(`uid=(\S+)\s+(?:button|link)\s+"([^"]*)"`)

// Confirm opens the page and clicks an approval control when the existing
// browser session already put one on screen. A page with no matching control
// is reported, not forced: that is the signal a human is genuinely required.
func (b ChromeBrowser) Confirm(ctx context.Context, url string, labels []string) (string, error) {
	if b.Runner == nil {
		return "", fmt.Errorf("auth: no command runner configured")
	}
	if len(labels) == 0 {
		return "", nil
	}
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = BrowserTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	opened, err := b.Runner.Run(ctx, execx.Request{Name: "chrome-devtools-axi", Args: []string{"open", url}})
	if err != nil {
		return "", fmt.Errorf("open %s: %w", url, err)
	}
	if opened.ExitCode != 0 {
		return "", fmt.Errorf("chrome-devtools-axi open exited %d", opened.ExitCode)
	}

	ref, label := findControl(string(opened.Stdout), labels)
	if ref == "" {
		// The open already prints a snapshot; ask again only when it did not
		// contain what we need, in case the page was still settling.
		snapshot, err := b.Runner.Run(ctx, execx.Request{Name: "chrome-devtools-axi", Args: []string{"snapshot"}})
		if err != nil {
			return "", fmt.Errorf("snapshot %s: %w", url, err)
		}
		ref, label = findControl(string(snapshot.Stdout), labels)
	}
	if ref == "" {
		return "no approval control on the page; a human sign-in is required", nil
	}

	clicked, err := b.Runner.Run(ctx, execx.Request{Name: "chrome-devtools-axi", Args: []string{"click", "@" + ref}})
	if err != nil {
		return "", fmt.Errorf("click %q: %w", label, err)
	}
	if clicked.ExitCode != 0 {
		return "", fmt.Errorf("chrome-devtools-axi click %q exited %d", label, clicked.ExitCode)
	}
	return fmt.Sprintf("clicked %q on %s", label, url), nil
}

// findControl returns the ref of the first button or link whose label
// contains one of the wanted strings, case-insensitively. Labels are matched
// in the caller's order so a manifest can rank "Authorize" above "Continue".
func findControl(snapshot string, wanted []string) (ref, label string) {
	matches := snapshotRef.FindAllStringSubmatch(snapshot, -1)
	for _, want := range wanted {
		want = strings.ToLower(strings.TrimSpace(want))
		if want == "" {
			continue
		}
		for _, match := range matches {
			if strings.Contains(strings.ToLower(match[2]), want) {
				return match[1], match[2]
			}
		}
	}
	return "", ""
}
