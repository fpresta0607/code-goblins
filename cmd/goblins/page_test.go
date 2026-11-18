package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/pagecheck"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func TestPageCheckRefusesWhatIsNoPageBeforeOpeningAnything(t *testing.T) {
	notes := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(notes, []byte("not a page"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"no subcommand":          {nil, "usage: goblins page check <html-file>"},
		"another subcommand":     {[]string{"open", "page.html"}, "usage: goblins page check <html-file>"},
		"no page":                {[]string{"check"}, "usage: goblins page check <html-file>"},
		"a flag before the page": {[]string{"check", "--out", "pictures", "page.html"}, "usage: goblins page check <html-file>"},
		"a stray argument":       {[]string{"check", notes, "extra"}, "unexpected argument"},
		"a file that is no page": {[]string{"check", notes}, "is not an HTML file"},
		"a page that is missing": {[]string{"check", filepath.Join(t.TempDir(), "gone.html")}, "is not an HTML file"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime(append([]string{"page"}, c.args...), &stdout, &stderr, commandRuntime{})

			// Assert
			if exit != 2 || !strings.Contains(stderr.String(), c.says) || stdout.Len() != 0 {
				t.Errorf("exit=%d stdout=%q stderr=%q, want 2 naming %q", exit, stdout.String(), stderr.String(), c.says)
			}
		})
	}
}

// pageLavish stands in for lavish-axi, answering an open with what lavish-axi
// 0.1.71 prints, and records the request it was given.
type pageLavish struct {
	stdout   string
	err      error
	requests *[]execx.Request
}

func (l pageLavish) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	*l.requests = append(*l.requests, req)
	return execx.Result{Stdout: []byte(l.stdout), Stderr: []byte("lavish-axi failed")}, l.err
}

const pageSessionURL = "http://127.0.0.1:4387/session/c42fd9a0de5ec55d"

func pageLavishOutput(status string) string {
	return "session:\n" +
		"  file: \"C:\\\\work\\\\.lavish\\\\plan.html\"\n" +
		"  url: \"" + pageSessionURL + "\"\n" +
		"  status: " + status + "\n"
}

func TestPageCheckReportsWhatTheHiddenChromeFound(t *testing.T) {
	overflow := pagecheck.Warning{Rule: "page-horizontal-overflow", Title: "Page scrolls sideways", ViewportClass: "phone", Selector: "pre.log", Explanation: "the line is 1200 pixels wide in a 390 pixel window"}
	clipped := pagecheck.Warning{Rule: "text-clipped", Title: "Text is cut off", ViewportClass: "desktop", Selector: "h1", Explanation: "the heading is clipped by its box"}
	for name, c := range map[string]struct {
		warnings []pagecheck.Warning
		exit     int
		says     []string
	}{
		"a page that fits": {nil, 0, []string{"layout: no issues at the desktop or the phone width"}},
		"a page with layout issues": {[]pagecheck.Warning{overflow, clipped}, 1, []string{
			"layout: 2 issues",
			"- Page scrolls sideways, phone width, at pre.log: the line is 1200 pixels wide in a 390 pixel window\n",
			"- Text is cut off, desktop width, at h1: the heading is clipped by its box\n",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			page := filepath.Join(t.TempDir(), "plan.html")
			if err := os.WriteFile(page, []byte("<h1>plan</h1>"), 0o600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "pictures")
			var requests []execx.Request
			var checked []string
			runtime := commandRuntime{
				lavish: pageLavish{stdout: pageLavishOutput("opened"), requests: &requests},
				checkPage: func(_ context.Context, session, folder string) (pagecheck.Report, error) {
					checked = append(checked, session, folder)
					return pagecheck.Report{
						Pictures: []pagecheck.Picture{{View: pagecheck.Views[0], Path: filepath.Join(folder, "desktop.png")}, {View: pagecheck.Views[1], Path: filepath.Join(folder, "phone.png")}},
						Warnings: c.warnings,
					}, nil
				},
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime([]string{"page", "check", page, "--out", out}, &stdout, &stderr, runtime)

			// Assert
			if exit != c.exit || stderr.Len() != 0 {
				t.Fatalf("exit=%d stderr=%q, want %d and nothing on stderr", exit, stderr.String(), c.exit)
			}
			if len(requests) != 1 || requests[0].Name != "lavish-axi" || !slices.Equal(requests[0].Args, []string{page, "--no-open"}) {
				t.Errorf("lavish-axi ran %+v, want one lavish-axi %s --no-open", requests, page)
			}
			if !slices.Equal(checked, []string{pageSessionURL, out}) {
				t.Errorf("the hidden Chrome checked %q, want the session %s pictured in %s", checked, pageSessionURL, out)
			}
			for _, line := range append([]string{
				"picture desktop, 1920 pixels wide: " + filepath.Join(out, "desktop.png") + "\n",
				"picture phone, 390 pixels wide: " + filepath.Join(out, "phone.png") + "\n",
			}, c.says...) {
				if !strings.Contains(stdout.String(), line) {
					t.Errorf("stdout = %q, want it to say %q", stdout.String(), line)
				}
			}
		})
	}
}

func TestPageCheckFailsWithoutAPictureWhenThePageCannotBeChecked(t *testing.T) {
	page := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(page, []byte("<h1>plan</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		lavishOutput string
		lavishErr    error
		checkErr     error
		isChecked    bool
		says         string
	}{
		"a review the Overlord ended": {pageLavishOutput("user-ended"), nil, nil, false, supervisor.EndedPage(page).Error()},
		"lavish-axi cannot open it":   {"", errors.New("lavish-axi is not installed"), nil, false, "lavish-axi cannot show " + page},
		"the hidden Chrome fails":     {pageLavishOutput("opened"), nil, errors.New("no Chrome to draw the page in"), true, "goblins page check: no Chrome to draw the page in"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var requests []execx.Request
			isChecked := false
			runtime := commandRuntime{
				lavish: pageLavish{stdout: c.lavishOutput, err: c.lavishErr, requests: &requests},
				checkPage: func(context.Context, string, string) (pagecheck.Report, error) {
					isChecked = true
					return pagecheck.Report{}, c.checkErr
				},
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime([]string{"page", "check", page}, &stdout, &stderr, runtime)

			// Assert
			if exit != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), c.says) {
				t.Errorf("exit=%d stdout=%q stderr=%q, want 1 naming %q", exit, stdout.String(), stderr.String(), c.says)
			}
			if isChecked != c.isChecked {
				t.Errorf("the hidden Chrome checked the page: %t, want %t", isChecked, c.isChecked)
			}
		})
	}
}
