package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestGateTurnRefusesMissingRequiredProjectEnvironmentBeforeCommand(t *testing.T) {
	// Arrange
	project, gateRoot, worktree := gateTaskFixture(t, "cg-example")
	owner := filepath.Join(project, ".worktrees", "gb-cg-example")
	homeRoot := os.Getenv("CFO_HOME")
	if err := state.WriteTaskMeta(filepath.Join(homeRoot, "state"), state.TaskMeta{ID: "cg-example", Project: project, Worktree: owner}); err != nil {
		t.Fatal(err)
	}
	writeGateSource(t, gateRoot, project, worktree, "feature")
	t.Setenv("NM_HOME", gateRoot)
	manifest := filepath.Join(homeRoot, "data", "projects", filepath.Base(project), "project.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"project":"`+filepath.Base(project)+`","test_environment":{"python_version":"3.12.10"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	turnLine(t)
	dir := commandFolder(t)
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(turnRuntime(), &stdout, &stderr, append([]string{"--gate-run", "run"}, turnCommand(dir, "configured", 0, false)...)...)

	// Assert
	if exit != exitNotStarted || left(dir, "configured", "started") || !strings.Contains(stderr.String(), "test environment") {
		t.Fatalf("exit=%d started=%v stderr=%q; want missing required environment refused before execution", exit, left(dir, "configured", "started"), stderr.String())
	}
}

func TestGateTurnBindsTheVerifiedEnvironmentWithoutReplacingTheConfiguredCommand(t *testing.T) {
	for _, code := range []string{"0", "7"} {
		t.Run(code, func(t *testing.T) {
			// Arrange
			environment := gateEnvironmentFixture(t)
			t.Setenv("UV_PROJECT_ENVIRONMENT", "unverified-client-environment")
			t.Setenv("UV_NO_SYNC", "0")
			t.Setenv("UNCHANGED_DAEMON_SETTING", "daemon-sentinel")
			turnLine(t)
			command := `echo %UV_PROJECT_ENVIRONMENT%^|%UV_PYTHON%^|%UV_NO_SYNC%^|%UV_OFFLINE%^|%UV_PYTHON_DOWNLOADS%^|%UNCHANGED_DAEMON_SETTING% & exit /b ` + code
			var stdout, stderr lockedBuffer

			// Act
			exit := gateTurn(turnRuntime(), &stdout, &stderr, "--gate-run", "run", "--", "cmd.exe", "/d", "/c", command)

			// Assert
			want := environment + "|" + filepath.Join(environment, "Scripts", "python.exe") + "|1|1|never|daemon-sentinel"
			if (code == "0" && exit != 0) || (code == "7" && exit != 7) || strings.TrimSpace(stdout.String()) != want || stderr.String() != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q; want configured exit %s and %q", exit, stdout.String(), stderr.String(), code, want)
			}
			if os.Getenv("UV_PROJECT_ENVIRONMENT") != "unverified-client-environment" || os.Getenv("UV_NO_SYNC") != "0" || os.Getenv("UNCHANGED_DAEMON_SETTING") != "daemon-sentinel" {
				t.Fatal("binding modified the wrapper's environment")
			}
		})
	}
}

func TestGateTurnRechecksTheRequiredEnvironmentAfterWaitingForAdmission(t *testing.T) {
	// Arrange
	environment := gateEnvironmentFixture(t)
	turnLine(t)
	dir := commandFolder(t)
	holder := startGateTurn(t, dir, nil, turnCommand(dir, "holder", 0, true)...)
	eventually(t, "holder started", func() bool { return left(dir, "holder", "started") })
	var stdout, stderr lockedBuffer
	ended := make(chan int, 1)
	go func() {
		ended <- gateTurn(turnRuntime(), &stdout, &stderr, append([]string{"--gate-run", "run"}, turnCommand(dir, "configured", 0, false)...)...)
	}()
	eventually(t, "configured command waiting", func() bool { return strings.Contains(stderr.String(), "waiting for its turn") })
	if err := os.Rename(environment, environment+"-unavailable"); err != nil {
		t.Fatal(err)
	}

	// Act
	letGo(t, dir, "holder")
	if exit := holder.exit(t, "holder"); exit != 0 {
		t.Fatalf("holder exit=%d", exit)
	}
	var exit int
	select {
	case exit = <-ended:
	case <-time.After(time.Minute):
		t.Fatalf("configured command did not end: %s", stderr.String())
	}

	// Assert
	if exit != exitNotStarted || left(dir, "configured", "started") || !strings.Contains(stderr.String(), "required test environment") {
		t.Fatalf("exit=%d started=%v stderr=%q", exit, left(dir, "configured", "started"), stderr.String())
	}
}

func gateEnvironmentFixture(t *testing.T) string {
	t.Helper()
	project, gateRoot, worktree := gateTaskFixture(t, "cg-example")
	owner := filepath.Join(project, ".worktrees", "gb-cg-example")
	homeRoot := os.Getenv("CFO_HOME")
	if err := state.WriteTaskMeta(filepath.Join(homeRoot, "state"), state.TaskMeta{ID: "cg-example", Project: project, Worktree: owner}); err != nil {
		t.Fatal(err)
	}
	writeGateSource(t, gateRoot, project, worktree, "feature")
	t.Setenv("NM_HOME", gateRoot)
	manifest := filepath.Join(homeRoot, "data", "projects", filepath.Base(project), "project.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"project":"`+filepath.Base(project)+`","test_environment":{"python_version":"3.12.10"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := filepath.Join(owner, ".venv")
	python := filepath.Join(environment, "Scripts", "python.exe")
	if err := os.MkdirAll(filepath.Dir(python), 0o755); err != nil {
		t.Fatal(err)
	}
	standin.Put(t, python)
	if err := os.WriteFile(filepath.Join(environment, "pyvenv.cfg"), []byte("home = existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := json.Marshal(struct {
		Executable   string `json:"executable"`
		Prefix       string `json:"prefix"`
		BasePrefix   string `json:"base_prefix"`
		Version      string `json:"version"`
		ReleaseLevel string `json:"releaselevel"`
		Serial       int    `json:"serial"`
	}{python, environment, project, "3.12.10", "final", 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range standin.Env("", standin.Rule{Prefix: true, Args: "-I -c", Stdout: string(identity)}) {
		name, value, _ := strings.Cut(setting, "=")
		t.Setenv(name, value)
	}
	t.Setenv("CFO_GATE_TURN_TEST_MEMORY", filepath.Join(t.TempDir(), "plenty"))
	t.Setenv("CFO_TASK_ID", "")
	return environment
}

func TestGateTurnBindsOnlyTheVerifiedRunAndItsExactBaseClone(t *testing.T) {
	for _, scenario := range []string{"base clone", "wrong origin", "wrong base commit", "unknown run", "unbound wrapper"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			environment := gateEnvironmentFixture(t)
			managed, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			base := filepath.Join(t.TempDir(), "base")
			git := func(dir string, args ...string) string {
				t.Helper()
				output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %s (%v)", args, output, err)
				}
				return strings.TrimSpace(string(output))
			}
			git(managed, "clone", "--quiet", "--shared", managed, base)
			head := git(base, "rev-parse", "HEAD")
			if output, err := exec.Command("sqlite3", filepath.Join(os.Getenv("NM_HOME"), "state.sqlite"), "UPDATE runs SET base_sha='"+head+"'").CombinedOutput(); err != nil {
				t.Fatalf("record base: %s (%v)", output, err)
			}
			runID := "run"
			switch scenario {
			case "wrong origin":
				git(base, "remote", "set-url", "origin", t.TempDir())
			case "wrong base commit":
				git(base, "config", "user.name", "test")
				git(base, "config", "user.email", "test@example.invalid")
				git(base, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "change base")
			case "unknown run":
				runID = "unknown"
			}
			t.Chdir(base)
			if scenario == "unbound wrapper" {
				t.Chdir(managed)
			}
			turnLine(t)
			args := []string{"--gate-run", runID, "--", "cmd.exe", "/d", "/c", "echo %UV_PROJECT_ENVIRONMENT%"}
			if scenario == "unbound wrapper" {
				args = args[2:]
			}
			var stdout, stderr lockedBuffer

			// Act
			exit := gateTurn(turnRuntime(), &stdout, &stderr, args...)

			// Assert
			if scenario == "base clone" {
				if exit != 0 || strings.TrimSpace(stdout.String()) != environment {
					t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
				}
			} else if exit != exitNotStarted || stdout.String() != "" || !strings.Contains(stderr.String(), "test environment") {
				t.Fatalf("unverified command ran: exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
			}
		})
	}
}

func TestGateTurnLeavesAnUnconfiguredProjectsEnvironmentAndOwnershipAlone(t *testing.T) {
	// Arrange
	project, gateRoot, worktree := gateTaskFixture(t, "cg-example")
	writeGateSource(t, gateRoot, project, worktree, "feature")
	t.Setenv("NM_HOME", gateRoot)
	t.Setenv("UV_PROJECT_ENVIRONMENT", "existing-daemon-setting")
	t.Setenv("ARBITRARY_DAEMON_SETTING", "unchanged")
	turnLine(t)
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(turnRuntime(), &stdout, &stderr, "--gate-run", "run", "--", "cmd.exe", "/d", "/c", "echo %UV_PROJECT_ENVIRONMENT%^|%ARBITRARY_DAEMON_SETTING%")

	// Assert
	if exit != 0 || strings.TrimSpace(stdout.String()) != "existing-daemon-setting|unchanged" || stderr.String() != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q; want no borrowed binding or required fleet ownership", exit, stdout.String(), stderr.String())
	}
}
