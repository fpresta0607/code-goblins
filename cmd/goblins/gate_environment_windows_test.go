package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGateTurnDoesNotInferNativeBindingFromDirectoryNames(t *testing.T) {
	for _, test := range []struct {
		name string
		exit int
	}{{"success", 0}, {"command failure", 7}} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			turnLine(t)
			root := t.TempDir()
			checkout := filepath.Join(root, "worktrees", "demo", "feature")
			if err := os.MkdirAll(checkout, 0o755); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command("git", "-C", checkout, "init", "--quiet").CombinedOutput(); err != nil {
				t.Fatalf("create ordinary checkout: %s (%v)", output, err)
			}
			if err := os.WriteFile(filepath.Join(root, "state.sqlite"), []byte("not a native database"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GOBLINS_HOME", filepath.Join(root, "fleet"))
			t.Setenv("NM_HOME", filepath.Join(root, "unrelated-native-home"))
			t.Setenv(gateTurnTestMemoryVariable, filepath.Join(root, "plenty"))
			marks := commandFolder(t)

			// Act
			run := startGateTurn(t, checkout, nil, turnCommand(marks, "ordinary", test.exit, false)...)
			exit := run.exit(t, "ordinary command")

			// Assert
			if exit != test.exit || !left(marks, "ordinary", "ended") || run.stdout.String() != commandOutput("ordinary") || run.stderr.String() != commandErrors("ordinary") {
				t.Fatalf("exit=%d ended=%v stdout=%q stderr=%q; want the ordinary command's output and exit %d", exit, left(marks, "ordinary", "ended"), run.stdout.String(), run.stderr.String(), test.exit)
			}
		})
	}
}

func TestGateTurnRechecksTheRequiredEnvironmentAfterWaitingForAdmission(t *testing.T) {
	// Arrange
	environment, comparison := gateEnvironmentFixture(t)
	turnLine(t)
	dir := commandFolder(t)
	holder := startGateTurn(t, dir, nil, turnCommand(dir, "holder", 0, true)...)
	eventually(t, "holder started", func() bool { return left(dir, "holder", "started") })
	var stdout, stderr lockedBuffer
	ended := make(chan int, 1)
	go func() {
		ended <- gateTurn(turnRuntime(), &stdout, &stderr, append([]string{"--gate-run", "run", "--test-comparison-sha", comparison}, turnCommand(dir, "configured", 0, false)...)...)
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

func TestGateTurnRefusesChangedSourceAfterWaitingForAdmission(t *testing.T) {
	// Arrange
	_, comparison := gateEnvironmentFixture(t)
	turnLine(t)
	dir := commandFolder(t)
	holder := startGateTurn(t, dir, nil, turnCommand(dir, "holder", 0, true)...)
	eventually(t, "holder started", func() bool { return left(dir, "holder", "started") })
	var stdout, stderr lockedBuffer
	ended := make(chan int, 1)
	go func() {
		ended <- gateTurn(turnRuntime(), &stdout, &stderr, append([]string{"--gate-run", "run", "--test-comparison-sha", comparison}, turnCommand(dir, "configured", 0, false)...)...)
	}()
	eventually(t, "configured command waiting", func() bool { return strings.Contains(stderr.String(), "waiting for its turn") })
	arguments := []string{"-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "change managed source"}
	if output, err := exec.Command("git", arguments...).CombinedOutput(); err != nil {
		t.Fatalf("change managed source: %s (%v)", output, err)
	}
	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("sqlite3", filepath.Join(os.Getenv("NM_HOME"), "state.sqlite"), "UPDATE runs SET head_sha='"+strings.TrimSpace(string(head))+"'").CombinedOutput(); err != nil {
		t.Fatalf("record changed source: %s (%v)", output, err)
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
	if exit != exitNotStarted || left(dir, "configured", "started") || !strings.Contains(stderr.String(), "changed while waiting") {
		t.Fatalf("exit=%d started=%v stderr=%q; want changed source refused before execution", exit, left(dir, "configured", "started"), stderr.String())
	}
}
