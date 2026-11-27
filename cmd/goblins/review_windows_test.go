package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// lavish-axi keeps a page's session under the file's real path, and opens
// again, uninvited, a page whose review an agent ended. So that page, given
// through a junction to its folder, is refused before lavish-axi runs, as it
// is by the path lavish-axi keeps.
func TestReviewCommandRefusesAnAgentEndedPageGivenThroughAJunction(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}
	pages := filepath.Join(dir, "pages")
	if err := os.MkdirAll(pages, 0o700); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(pages, "mockup.html")
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(dir, "via-junction")
	if output, err := exec.Command("cmd", "/d", "/c", "mklink", "/J", junction, pages).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, output)
	}
	lavish := t.TempDir()
	t.Setenv("LAVISH_AXI_STATE_DIR", lavish)
	stored := `{"sessions":{"ad6519f76542da4e":{"key":"ad6519f76542da4e","file":` + strconv.Quote(page) + `,"status":"ended","ended_by":"agent"}}}`
	if err := os.WriteFile(filepath.Join(lavish, "state.json"), []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	args := filepath.Join(dir, "lavish-args.txt")
	bin := t.TempDir()
	script := "@echo off\r\necho %* > \"" + args + "\"\r\necho session:\r\necho   url: \"http://127.0.0.1:4387/session/ad6519f76542da4e\"\r\necho   status: opened\r\n"
	if err := os.WriteFile(filepath.Join(bin, "lavish-axi.cmd"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Join(os.Getenv("SystemRoot"), "System32"))

	// Act
	var stdout, stderr bytes.Buffer
	exit := runReview([]string{"--id", "site-real-photos-1", "--task", "g1", "--title", "The site with real photos", "--lavish", filepath.Join(junction, "mockup.html")}, &stdout, &stderr, runtime)

	// Assert
	if exit != 1 || !strings.Contains(stderr.String(), "has ended") || !strings.Contains(stderr.String(), "--reopen") {
		t.Fatalf("exit=%d stderr=%q, want the ended page refused naming --reopen", exit, stderr.String())
	}
	if _, err := os.Stat(args); err == nil {
		t.Fatal("the refused review ran lavish-axi, which opens again a page an agent ended")
	}
	if entries, err := os.ReadDir(filepath.Join(h.State, "reviews-inbox")); err == nil && len(entries) != 0 {
		t.Fatalf("the refused review left %d inbox records", len(entries))
	}
}
