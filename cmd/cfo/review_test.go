package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// cfo review refuses a withdrawal mixed with a publication, and a report
// from a process that is not the task's goblin records nothing.
func TestReviewCommandRefusesBeforeRecordingAnything(t *testing.T) {
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}
	for name, c := range map[string]struct {
		args []string
		exit int
		says string
	}{
		"a withdrawal with a title": {[]string{"--id", "mockups-review-1", "--withdraw", "replaced", "--title", "Look"}, 2, "--withdraw takes only"},
		"a stray argument":          {[]string{"--id", "mockups-review-1", "--title", "Look", "extra"}, 2, ""},
		"a task with no goblin":     {[]string{"--id", "mockups-review-1", "--task", "g1", "--title", "Look"}, 1, "no live record"},
		"a clear with no reason":    {[]string{"--clear", "mockups-review-1"}, 2, "--clear needs --reason"},
		"a clear with a title":      {[]string{"--clear", "mockups-review-1", "--reason", "decided", "--title", "Look"}, 2, "--clear takes only --reason"},
		"a reason with no clear":    {[]string{"--id", "mockups-review-1", "--title", "Look", "--reason", "decided"}, 2, "--reason goes with --clear"},
		"a clear from no CFO":       {[]string{"--clear", "mockups-review-1", "--reason", "decided"}, 1, "not registered"},
		"a withdrawal that reopens": {[]string{"--id", "mockups-review-1", "--withdraw", "replaced", "--reopen"}, 2, "--withdraw takes only"},
		"a reopen with no page":     {[]string{"--id", "mockups-review-1", "--title", "Look", "--reopen"}, 2, "--reopen goes with --lavish"},
	} {
		var stdout, stderr bytes.Buffer
		if exit := runReview(c.args, &stdout, &stderr, runtime); exit != c.exit || !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%s: exit=%d stderr=%q, want %d naming %q", name, exit, stderr.String(), c.exit, c.says)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(h.State, "reviews-inbox")); err == nil && len(entries) != 0 {
		t.Fatalf("a refused report left %d inbox records", len(entries))
	}
	if _, err := os.Stat(filepath.Join(h.State, "reviews")); !os.IsNotExist(err) {
		t.Fatalf("a refused report copied images: %v", err)
	}
}

// --lavish takes a page's HTML file as well as its link: the file is checked
// and opened without a browser before anything is published, so the
// supervisor can poll it, and a link needs no lavish-axi at all.
func TestReviewCommandOpensTheLavishPageItIsGiven(t *testing.T) {
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}
	page := filepath.Join(dir, "plan.html")
	notes := filepath.Join(dir, "notes.txt")
	for _, path := range []string{page, notes} {
		if err := os.WriteFile(path, []byte("<html></html>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	review := func(lavish string) (int, string) {
		var stdout, stderr bytes.Buffer
		exit := runReview([]string{"--id", "plan-review-1", "--task", "g1", "--title", "Pick a plan", "--lavish", lavish}, &stdout, &stderr, runtime)
		return exit, stderr.String()
	}

	t.Setenv("PATH", t.TempDir())
	for name, c := range map[string]struct {
		lavish string
		exit   int
		says   string
	}{
		"a page that is not HTML":       {notes, 2, notes + " is not an HTML file"},
		"a page that is missing":        {filepath.Join(dir, "gone.html"), 2, "is not an HTML file"},
		"a page lavish-axi cannot show": {page, 1, "lavish-axi cannot show " + page},
		"a link, which needs no page":   {"http://127.0.0.1:4387/session/f26e", 1, "no live record"},
	} {
		if exit, stderr := review(c.lavish); exit != c.exit || !strings.Contains(stderr, c.says) {
			t.Errorf("%s: exit=%d stderr=%q, want %d naming %q", name, exit, stderr, c.exit, c.says)
		}
	}

	args := filepath.Join(dir, "lavish-args.txt")
	bin := t.TempDir()
	script := "@echo off\r\necho %* > \"" + args + "\"\r\necho session:\r\necho   url: \"http://127.0.0.1:4387/session/f26e\"\r\necho   status: opened\r\n"
	if err := os.WriteFile(filepath.Join(bin, "lavish-axi.cmd"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Join(os.Getenv("SystemRoot"), "System32"))

	// g1 is no goblin, so publishing refuses it; reaching that refusal means
	// the page was checked and opened first.
	if exit, stderr := review(page); exit != 1 || !strings.Contains(stderr, "no live record") {
		t.Fatalf("exit=%d stderr=%q, want the page opened and then g1 refused", exit, stderr)
	}
	if data, err := os.ReadFile(args); err != nil || strings.TrimSpace(string(data)) != page+" --no-open" {
		t.Fatalf("lavish-axi ran with %q (%v), want %q", data, err, page+" --no-open")
	}
	if entries, err := os.ReadDir(filepath.Join(h.State, "reviews-inbox")); err == nil && len(entries) != 0 {
		t.Fatalf("a refused report left %d inbox records", len(entries))
	}
}

// The Overlord, 2026-10-17 about 22:10Z, opened a review card whose page's
// session had ended a minute before and got Scrawl's "Session ended". A
// review of a session that has ended is refused with a line naming --reopen,
// and --reopen opens that session's page again with lavish-axi's own
// --reopen before the item is published with the address it returns.
func TestReviewCommandRefusesAnEndedPageUnlessToldToReopenIt(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}
	page := filepath.Join(dir, "mockup.html")
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	lavish := t.TempDir()
	t.Setenv("LAVISH_AXI_STATE_DIR", lavish)
	stored := `{"sessions":{"ad6519f76542da4e":{"key":"ad6519f76542da4e","file":` + strconv.Quote(page) + `,"status":"ended","ended_by":"user"}}}`
	if err := os.WriteFile(filepath.Join(lavish, "state.json"), []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	args := filepath.Join(dir, "lavish-args.txt")
	bin := t.TempDir()
	script := "@echo off\r\necho %* > \"" + args + "\"\r\necho session:\r\necho   url: \"http://127.0.0.1:4387/session/b7e0c1d2a3f40516\"\r\necho   status: opened\r\n"
	if err := os.WriteFile(filepath.Join(bin, "lavish-axi.cmd"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Join(os.Getenv("SystemRoot"), "System32"))
	review := func(extra ...string) (int, string) {
		var stdout, stderr bytes.Buffer
		exit := runReview(append([]string{"--id", "site-real-photos-1", "--task", "g1", "--title", "The site with real photos", "--lavish", "https://sermon.tailcc4238.ts.net:4388/session/ad6519f76542da4e"}, extra...), &stdout, &stderr, runtime)
		return exit, stderr.String()
	}

	// Act
	refusedExit, refused := review()
	_, ranBefore := os.Stat(args)
	reopenedExit, reopened := review("--reopen")

	// Assert
	if refusedExit != 1 || !strings.Contains(refused, "has ended") || !strings.Contains(refused, "--reopen") {
		t.Errorf("a review of an ended session: exit=%d stderr=%q, want it refused naming --reopen", refusedExit, refused)
	}
	if ranBefore == nil {
		t.Error("the refused review ran lavish-axi")
	}
	// g1 is no goblin, so publishing refuses it; reaching that refusal means
	// the page was opened again first.
	if reopenedExit != 1 || !strings.Contains(reopened, "no live record") {
		t.Fatalf("--reopen: exit=%d stderr=%q, want the page opened again and then g1 refused", reopenedExit, reopened)
	}
	if data, err := os.ReadFile(args); err != nil || strings.TrimSpace(string(data)) != page+" --no-open --reopen" {
		t.Fatalf("lavish-axi ran with %q (%v), want %q", data, err, page+" --no-open --reopen")
	}
}

// Opening a page has its own budget: a slow lavish-axi still opens the page,
// and the item is published after it.
func TestReviewCommandGivesThePageOpenItsOwnTime(t *testing.T) {
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(dir, "plan.html")
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "@echo off\r\nping -n 3 127.0.0.1 >nul\r\necho session:\r\necho   url: \"http://127.0.0.1:4387/session/f26e\"\r\necho   status: opened\r\n"
	if err := os.WriteFile(filepath.Join(bin, "lavish-axi.cmd"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Join(os.Getenv("SystemRoot"), "System32"))
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}

	var stdout, stderr bytes.Buffer
	exit := runReview([]string{"--id", "plan-review-1", "--task", "g1", "--title", "Pick a plan", "--lavish", page}, &stdout, &stderr, runtime)

	// g1 is no goblin, so publishing refuses it: reaching that refusal means
	// the slow open finished.
	if exit != 1 || !strings.Contains(stderr.String(), "no live record") {
		t.Fatalf("exit=%d stderr=%q, want the page opened and then g1 refused", exit, stderr.String())
	}
}
