package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// projectFixture is a scratch home and a projects root holding one real
// checkout, northwind, with a commit.
type projectFixture struct {
	home     home.Home
	checkout string
	runtime  commandRuntime
}

func newProjectFixture(t *testing.T) projectFixture {
	t.Helper()
	h := testHome(t)
	root := t.TempDir()
	checkout := filepath.Join(root, "northwind")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "README.md"), []byte("northwind\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch=main"},
		{"add", "--all"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "--message", "first"},
	} {
		command := exec.Command("git", args...)
		command.Dir = checkout
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runtime := testCommandRuntimeForHome(h)
	runtime.projectsRoot = func() (string, error) { return root, nil }
	return projectFixture{home: h, checkout: checkout, runtime: runtime}
}

func (f projectFixture) record(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(f.home.Data, "projects", "northwind", "project.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (f projectFixture) run(args ...string) (int, string, string) {
	var stdout, stderr strings.Builder
	code := runProject(args, &stdout, &stderr, f.runtime)
	return code, stdout.String(), stderr.String()
}

// The check answered a project with no record with the operating system's
// "cannot find the file specified". It now says what is missing, where it
// belongs and what it costs, by the project's name and by its path alike.
func TestProjectCheckReportsAMissingRecordByNameAndByPath(t *testing.T) {
	// Arrange
	f := newProjectFixture(t)

	for _, project := range []string{"northwind", f.checkout} {
		// Act
		code, stdout, stderr := f.run("check", project)

		// Assert
		if code != 1 {
			t.Errorf("cfo project check %s = %d, want 1: %s", project, code, stderr)
		}
		for _, want := range []string{
			"high record/record-missing:",
			filepath.Join(f.home.Data, "projects", "northwind", "project.json"),
			"project northwind: record failed",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("cfo project check %s does not print %q:\n%s", project, want, stdout)
			}
		}
		if strings.Contains(stdout+stderr, "cannot find the file") {
			t.Errorf("cfo project check %s still answers with the operating system's error:\n%s%s", project, stdout, stderr)
		}
	}
}

// One area can be asked for by itself, and the command passes when no line
// of it is worse than low.
func TestProjectCheckPassesAnAreaWithNothingWrong(t *testing.T) {
	// Arrange
	f := newProjectFixture(t)
	f.record(t, `{"project":"northwind","verification":{"fast":[["git","status"]]}}`)

	// Act
	code, stdout, stderr := f.run("check", "northwind", "--area", "record")

	// Assert
	if code != 0 {
		t.Fatalf("cfo project check northwind --area record = %d, want 0: %s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "ok record/record-valid:") || !strings.Contains(stdout, "project northwind: record passed") {
		t.Errorf("the record area's lines and verdict are missing:\n%s", stdout)
	}
	if strings.Contains(stdout, "configs/") || strings.Contains(stdout, "connectors/") {
		t.Errorf("--area record printed another area's lines:\n%s", stdout)
	}
}

func TestProjectCheckRefusesAnAreaItDoesNotHave(t *testing.T) {
	// Arrange
	f := newProjectFixture(t)

	// Act
	code, _, stderr := f.run("check", "northwind", "--area", "weather")

	// Assert
	if code != 2 || !strings.Contains(stderr, "weather") || !strings.Contains(stderr, "record") {
		t.Errorf("cfo project check --area weather = %d, want 2 naming the areas: %s", code, stderr)
	}
}

func TestProjectCheckPrintsItsReportAsJSON(t *testing.T) {
	// Arrange
	f := newProjectFixture(t)

	// Act
	code, stdout, _ := f.run("check", "northwind", "--json")

	// Assert
	var report struct {
		Project string `json:"project"`
		Lines   []struct {
			Area, Check, Severity, Says, Evidence string
		} `json:"lines"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("cfo project check --json did not print JSON: %v\n%s", err, stdout)
	}
	if code != 1 || report.Project != "northwind" || len(report.Lines) == 0 {
		t.Errorf("cfo project check --json = %d with %d lines for %q, want 1 with the report of northwind", code, len(report.Lines), report.Project)
	}
}

func TestProjectCheckRefusesAProjectThatIsNoCheckout(t *testing.T) {
	// Arrange
	f := newProjectFixture(t)

	// Act
	code, _, stderr := f.run("check", "southwind")

	// Assert
	if code != 1 || !strings.Contains(stderr, "southwind") {
		t.Errorf("cfo project check southwind = %d, want 1 naming it: %s", code, stderr)
	}
}

// A project is a name or a path wherever a command takes one, so show and
// init file the record under the checkout's folder name for both.
func TestProjectInitAndShowKeyTheRecordByTheCheckoutsFolder(t *testing.T) {
	// Arrange
	f := newProjectFixture(t)
	want := filepath.Join(f.home.Data, "projects", "northwind", "project.json")

	// Act
	code, stdout, stderr := f.run("init", f.checkout)

	// Assert
	if code != 0 || strings.TrimSpace(stdout) != want {
		t.Fatalf("cfo project init %s = %d printing %q, want 0 printing %s: %s", f.checkout, code, stdout, want, stderr)
	}
	for _, project := range []string{"northwind", f.checkout} {
		code, stdout, stderr := f.run("show", project)
		if code != 0 || !strings.Contains(stdout, `"project": "northwind"`) {
			t.Errorf("cfo project show %s = %d, want 0 showing the record of northwind: %s%s", project, code, stdout, stderr)
		}
	}
}

// commit adds one tracked file to the checkout.
func (f projectFixture) commit(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.checkout, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "--all"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "--message", name},
	} {
		command := exec.Command("git", args...)
		command.Dir = f.checkout
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// The draft is written where the caller says and never over a file, and
// once a person places it as the project's record the record area passes.
func TestProjectCheckWritesADraftThatPassesTheRecordAreaOncePlaced(t *testing.T) {
	// Arrange
	f := newProjectFixture(t)
	f.commit(t, ".no-mistakes.yaml", "commands:\n  test: \"git status\"\n")
	draft := filepath.Join(t.TempDir(), "drafts", "northwind", "project.json")

	// Act
	code, stdout, stderr := f.run("check", "northwind", "--draft", draft)

	// Assert
	if code != 1 || !strings.Contains(stdout, "high record/record-missing:") || !strings.Contains(stdout, draft) {
		t.Fatalf("cfo project check --draft = %d, want 1 with the missing record and the draft's path: %s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "draft tier: the draft's fast tier is the gate's own test command") {
		t.Errorf("cfo project check --draft does not say where the draft's fast tier came from:\n%s", stdout)
	}
	written, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"project": "northwind"`, `"git"`, `"status"`} {
		if !strings.Contains(string(written), want) {
			t.Errorf("the draft does not hold %s:\n%s", want, written)
		}
	}
	f.record(t, string(written))
	if code, stdout, stderr := f.run("check", "northwind", "--area", "record"); code != 0 {
		t.Errorf("the placed draft does not pass the record area (%d): %s%s", code, stdout, stderr)
	}
	if code, _, stderr := f.run("check", "northwind", "--draft", draft); code != 1 || !strings.Contains(stderr, "already") {
		t.Errorf("a second --draft to the same file = %d, want 1 refusing to write over it: %s", code, stderr)
	}
}

// A draft with no verification command would fail the record area of the
// check that wrote it, so none is written and the command says what is
// missing.
func TestProjectCheckWritesNoDraftThatWouldFailTheRecordArea(t *testing.T) {
	// Arrange
	f := newProjectFixture(t)
	draft := filepath.Join(t.TempDir(), "drafts", "northwind", "project.json")

	// Act
	code, stdout, stderr := f.run("check", "northwind", "--draft", draft)

	// Assert
	if code != 1 || !strings.Contains(stderr, "no draft is written") || !strings.Contains(stderr, "names no verification command") {
		t.Errorf("cfo project check --draft = %d, want 1 saying why no draft is written: %s%s", code, stdout, stderr)
	}
	if _, err := os.Stat(draft); err == nil {
		t.Error("the check wrote a draft with no verification command")
	}
}

// A record under the home steers routing and verification for live spawns,
// so the check never writes one there: a person places a draft.
func TestProjectCheckNeverDraftsIntoTheHomesProjects(t *testing.T) {
	// Arrange
	f := newProjectFixture(t)
	record := filepath.Join(f.home.Data, "projects", "northwind", "project.json")

	// Act
	code, _, stderr := f.run("check", "northwind", "--draft", record)

	// Assert
	if code != 1 || !strings.Contains(stderr, "place") {
		t.Errorf("cfo project check --draft into the home = %d, want 1 saying a person places it: %s", code, stderr)
	}
	if _, err := os.Stat(record); err == nil {
		t.Error("the check wrote a record into the home")
	}
}
