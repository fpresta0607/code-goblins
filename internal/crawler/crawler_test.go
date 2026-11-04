package crawler

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// source is a build's connector: each of its files holds version.
func source(version string) fstest.MapFS {
	build := fstest.MapFS{}
	for _, name := range files {
		build[SourceDir+"/"+name] = &fstest.MapFile{Data: []byte(name + " " + version)}
	}
	return build
}

// npm stands in for npm ci: it records each run, says whether the commands
// that start the connector were already there when it ran, and installs the
// packages unless it is told to fail.
type npm struct {
	runs          []execx.Request
	sawCommand    bool
	exitCode      int
	shouldInstall bool
}

func (n *npm) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	n.runs = append(n.runs, request)
	n.sawCommand = IsCommandIn(filepath.Join(request.Dir, "bin"))
	if n.exitCode != 0 {
		return execx.Result{ExitCode: n.exitCode, Stderr: []byte("npm error code E404\nnpm error 404 Not Found")}, nil
	}
	if n.shouldInstall {
		installed := filepath.Join(request.Dir, installedPackage)
		if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
			return execx.Result{}, err
		}
		if err := os.WriteFile(installed, []byte("{}"), 0o644); err != nil {
			return execx.Result{}, err
		}
	}
	return execx.Result{}, nil
}

func TestSetupInstallsTheBuildsConnectorFromItsLockfileAndOnlyThenItsCommand(t *testing.T) {
	// Arrange
	home := t.TempDir()
	runner := &npm{shouldInstall: true}

	// Act
	found, err := Setup(context.Background(), home, source("v1"), runner)

	// Assert
	if err != nil || found != Missing {
		t.Fatalf("Setup = %v, %v, want Missing, nil", found, err)
	}
	if len(runner.runs) != 1 {
		t.Fatalf("npm ran %d times, want once", len(runner.runs))
	}
	run := runner.runs[0]
	if run.Name != "npm.cmd" || !slices.Equal(run.Args, []string{"ci", "--omit=dev", "--no-audit", "--no-fund"}) || run.Dir != Dir(home) || !run.KillTree {
		t.Errorf("npm ran as %+v, want npm.cmd ci --omit=dev --no-audit --no-fund in %s, as a tree", run, Dir(home))
	}
	if runner.sawCommand {
		t.Error("goblin-crawl was on its folder while npm ci still installed what it runs on")
	}
	for _, name := range files {
		if data, err := os.ReadFile(filepath.Join(Dir(home), name)); err != nil || string(data) != name+" v1" {
			t.Errorf("%s holds %q, %v, want the build's", name, data, err)
		}
	}
	for name, want := range shims {
		if data, err := os.ReadFile(filepath.Join(BinDir(home), name)); err != nil || string(data) != want {
			t.Errorf("%s holds %q, %v, want %q", name, data, err, want)
		}
	}
	if state, err := Check(home, source("v1")); state != Ready || err != nil {
		t.Errorf("Check after Setup = %v, %v, want Ready", state, err)
	}
}

func TestSetupLeavesAReadyConnectorAsItIs(t *testing.T) {
	// Arrange
	home := t.TempDir()
	if _, err := Setup(context.Background(), home, source("v1"), &npm{shouldInstall: true}); err != nil {
		t.Fatal(err)
	}
	runner := &npm{shouldInstall: true}

	// Act
	found, err := Setup(context.Background(), home, source("v1"), runner)

	// Assert
	if err != nil || found != Ready || len(runner.runs) != 0 {
		t.Errorf("Setup = %v, %v with %d npm runs, want Ready with none", found, err, len(runner.runs))
	}
}

// A build that pins other packages installs them before a goblin can start
// the connector again, so no goblin runs new code on old packages.
func TestSetupOfANewerBuildTakesTheCommandAwayWhileItInstalls(t *testing.T) {
	// Arrange
	home := t.TempDir()
	if _, err := Setup(context.Background(), home, source("v1"), &npm{shouldInstall: true}); err != nil {
		t.Fatal(err)
	}
	runner := &npm{shouldInstall: true}

	// Act
	found, err := Setup(context.Background(), home, source("v2"), runner)

	// Assert
	if err != nil || found != Stale || len(runner.runs) != 1 {
		t.Fatalf("Setup = %v, %v with %d npm runs, want Stale with one", found, err, len(runner.runs))
	}
	if runner.sawCommand {
		t.Error("goblin-crawl stayed on its folder while npm ci installed the newer build's packages")
	}
	if state, _ := Check(home, source("v2")); state != Ready {
		t.Errorf("Check after the newer Setup = %v, want Ready", state)
	}
}

func TestSetupThatNpmFailsLeavesNoCommandAndSaysWhy(t *testing.T) {
	// Arrange
	home := t.TempDir()
	runner := &npm{exitCode: 1}

	// Act
	_, err := Setup(context.Background(), home, source("v1"), runner)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "exited with code 1: npm error 404 Not Found") {
		t.Errorf("Setup error = %v, want npm's exit code and its last line", err)
	}
	if IsCommandIn(BinDir(home)) {
		t.Error("goblin-crawl is on its folder though its packages never installed")
	}
	if state, _ := Check(home, source("v1")); state != Missing {
		t.Errorf("Check = %v, want Missing", state)
	}
}

func TestABriefLeavesTheCrawlerOutOnlyWithConnectorsNone(t *testing.T) {
	for _, test := range []struct {
		name         string
		brief        string
		isLeftOut    bool
		shouldRefuse bool
	}{
		{name: "no connectors line", brief: "## Authentication\n\ncredentials: github\nmcp: none\n"},
		{name: "connectors none", brief: "## Authentication\n\ncredentials: none\nmcp: none\nconnectors: none\n", isLeftOut: true},
		{name: "connectors crawler", brief: "## Authentication\r\n\r\nconnectors: crawler\r\n"},
		{name: "a connectors line outside Authentication", brief: "## Task\n\nconnectors: none\n\n## Authentication\n\nmcp: none\n"},
		{name: "an unknown connector", brief: "## Authentication\n\nconnectors: firecrawl\n", shouldRefuse: true},
		{name: "two connectors lines", brief: "## Authentication\n\nconnectors: none\nconnectors: crawler\n", shouldRefuse: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			isLeftOut, err := IsLeftOutBy(test.brief)

			// Assert
			if (err != nil) != test.shouldRefuse {
				t.Fatalf("IsLeftOutBy error = %v, want refused %v", err, test.shouldRefuse)
			}
			if isLeftOut != test.isLeftOut {
				t.Errorf("IsLeftOutBy = %v, want %v", isLeftOut, test.isLeftOut)
			}
		})
	}
}
