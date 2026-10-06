package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/install"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// legacyMachine is a machine of the test's own, a fresh profile with a file
// standing in for the user environment, whose CFO home is a code-goblins
// checkout an older build made its home: its source tracked, beside it the
// fleet's state, data, config and caches and the older build's programs, and
// CFO_HOME and PATH naming it. It returns the old home and the user
// environment file.
func legacyMachine(t *testing.T) (string, string) {
	t.Helper()
	machine := t.TempDir()
	profile := filepath.Join(machine, "profile")
	local := filepath.Join(profile, "AppData", "Local")
	for _, folder := range []string{local, filepath.Join(profile, ".claude")} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	from := filepath.Join(machine, "code-goblins")
	writeTestFile(t, filepath.Join(from, "AGENTS.md"), "contract")
	writeTestFile(t, filepath.Join(from, "data", "routing.json"), `{"lanes":[]}`)
	writeTestFile(t, filepath.Join(from, ".gitignore"), "/state/\n/data/*\n!/data/routing.json\n/caches/\n/cfo.exe\n/goblins.exe\n")
	gitIn(t, from, "init", "-q")
	gitIn(t, from, "add", ".")
	gitIn(t, from, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "source")
	writeTestFile(t, filepath.Join(from, "state", "g1.meta"), "id=g1\nbrief="+filepath.Join(from, "data", "g1", "brief.md")+"\n")
	writeTestFile(t, filepath.Join(from, "data", "g1", "brief.md"), "do the work")
	writeTestFile(t, filepath.Join(from, "caches", "uv", "blob"), "downloaded")
	writeTestFile(t, filepath.Join(from, "cfo.exe"), "an older build")
	envFile := filepath.Join(machine, "user-env.json")
	env, err := json.Marshal(map[string]string{"CFO_HOME": from, "Path": `C:\Windows;` + from})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, envFile, string(env))
	t.Setenv(install.UserEnvFileVariable, envFile)
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", filepath.Join(profile, "AppData", "Roaming"))
	t.Setenv("USERPROFILE", profile)
	t.Setenv("HOME", profile)
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "CFO_STATE_OVERRIDE", "CFO_PROJECTS_ROOT"} {
		t.Setenv(name, "")
	}
	t.Setenv("CFO_HOME", from)
	return from, envFile
}

var planLine = regexp.MustCompile(`(?m)^plan: ([0-9a-f]{64})`)

// moveDryRun runs cfo home move without --apply and returns its plan's digest.
func moveDryRun(t *testing.T) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := runHomeMove(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("the dry run exited %d:\n%s%s", code, stdout.String(), stderr.String())
	}
	match := planLine.FindStringSubmatch(stdout.String())
	if match == nil {
		t.Fatalf("the dry run printed no plan:\n%s", stdout.String())
	}
	return match[1]
}

// The dry run changes nothing; applying its plan moves the fleet's folders
// into the per-user home, installs this build there, points the user
// environment at it with the old home off PATH, removes the older build's
// programs, and leaves the checkout its source.
func TestHomeMoveDryRunChangesNothingAndApplyMakesItsPlan(t *testing.T) {
	// Arrange
	from, envFile := legacyMachine(t)
	to := filepath.Join(os.Getenv("LOCALAPPDATA"), "CodeGoblins")
	statusBefore := gitIn(t, from, "status", "--porcelain")
	digest := moveDryRun(t)
	if _, err := os.Stat(filepath.Join(from, "state", "g1.meta")); err != nil {
		t.Fatalf("the dry run changed the home: %v", err)
	}

	// Act
	var stdout, stderr bytes.Buffer
	code := runHomeMove([]string{"--apply", "--plan", digest}, &stdout, &stderr)

	// Assert
	if code != 0 {
		t.Fatalf("apply exited %d:\n%s%s", code, stdout.String(), stderr.String())
	}
	for _, moved := range []string{filepath.Join("state", "g1.meta"), filepath.Join("data", "g1", "brief.md"), filepath.Join("caches", "uv", "blob"), filepath.Join("bin", "cfo.exe")} {
		if _, err := os.Stat(filepath.Join(to, moved)); err != nil {
			t.Errorf("the new home has no %s: %v", moved, err)
		}
	}
	for _, gone := range []string{"state", "caches", "cfo.exe"} {
		if _, err := os.Stat(filepath.Join(from, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still in the old home: %v", gone, err)
		}
	}
	if meta, err := os.ReadFile(filepath.Join(to, "state", "g1.meta")); err != nil || !strings.Contains(string(meta), "brief="+filepath.Join(to, "data", "g1", "brief.md")) {
		t.Errorf("the moved record does not follow its brief:\n%s", meta)
	}
	if status := gitIn(t, from, "status", "--porcelain"); status != statusBefore {
		t.Errorf("the checkout's git status = %q, want %q as before", status, statusBefore)
	}
	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]string
	if err := json.Unmarshal(env, &values); err != nil {
		t.Fatal(err)
	}
	if values["CFO_HOME"] != to || values["Path"] != `C:\Windows;`+filepath.Join(to, "bin") {
		t.Errorf("the user environment is %v, want CFO_HOME the new home and PATH its bin with the old home gone", values)
	}
}

// A plan is made only as approved: when the home changed after the dry run,
// apply refuses, names the difference and changes nothing.
func TestHomeMoveApplyRefusesAHomeThatChangedSinceItsDryRun(t *testing.T) {
	// Arrange
	from, _ := legacyMachine(t)
	to := filepath.Join(os.Getenv("LOCALAPPDATA"), "CodeGoblins")
	digest := moveDryRun(t)
	writeTestFile(t, filepath.Join(from, "state", "g2.status"), "working")

	// Act
	var stdout, stderr bytes.Buffer
	code := runHomeMove([]string{"--apply", "--plan", digest}, &stdout, &stderr)

	// Assert
	if code != 1 || !strings.Contains(stderr.String(), "not as plan") || !strings.Contains(stderr.String(), "state/g2.status") {
		t.Fatalf("apply exited %d, want a refusal naming the changed file:\n%s%s", code, stdout.String(), stderr.String())
	}
	for _, kept := range []string{filepath.Join("state", "g1.meta"), filepath.Join("state", "g2.status"), "cfo.exe"} {
		if _, err := os.Stat(filepath.Join(from, kept)); err != nil {
			t.Errorf("the refused move changed the old home's %s: %v", kept, err)
		}
	}
	if _, err := os.Stat(filepath.Join(to, "state")); !os.IsNotExist(err) {
		t.Errorf("the refused move wrote the new home's state: %v", err)
	}
}
