package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crawler"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// crawlerPackages stands in for npm ci: it installs the package the
// connector runs on in the folder it runs in, or fails with exitCode.
type crawlerPackages struct {
	runs     int
	exitCode int
}

func (p *crawlerPackages) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	p.runs++
	if p.exitCode != 0 {
		return execx.Result{ExitCode: p.exitCode, Stderr: []byte("npm error network request failed")}, nil
	}
	installed := filepath.Join(request.Dir, "node_modules", "@crawlee", "playwright", "package.json")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		return execx.Result{}, err
	}
	return execx.Result{}, os.WriteFile(installed, []byte("{}"), 0o644)
}

func crawlerRuntime(h home.Home, npm execx.Runner) commandRuntime {
	return commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }, crawlerNpm: npm}
}

func TestCrawlerSetupInstallsTheBuildsConnectorOnceAndDoctorSaysSo(t *testing.T) {
	// Arrange
	h := home.Home{Root: t.TempDir()}
	npm := &crawlerPackages{}
	var before bytes.Buffer
	reportCrawler(&before, crawlerRuntime(h, npm))

	// Act
	var first, second, stderr bytes.Buffer
	firstCode := runWithRuntime([]string{"crawler", "setup"}, &first, &stderr, crawlerRuntime(h, npm))
	secondCode := runWithRuntime([]string{"crawler", "setup"}, &second, &stderr, crawlerRuntime(h, npm))
	var after bytes.Buffer
	reportCrawler(&after, crawlerRuntime(h, npm))

	// Assert
	if want := "crawler: not set up, so goblins carry no goblin-crawl; run `goblins crawler setup`\n"; before.String() != want {
		t.Errorf("doctor before the setup says %q, want %q", before.String(), want)
	}
	if firstCode != 0 || first.String() != "crawler: ready: goblin-crawl, in "+crawler.Dir(h.Root)+"\n" {
		t.Errorf("goblins crawler setup = %d, %q\nstderr: %s", firstCode, first.String(), stderr.String())
	}
	if secondCode != 0 || !strings.Contains(second.String(), "so nothing was installed") || npm.runs != 1 {
		t.Errorf("the second goblins crawler setup = %d, %q after %d npm runs, want it to install nothing", secondCode, second.String(), npm.runs)
	}
	if !crawler.IsCommandIn(crawler.BinDir(h.Root)) {
		t.Error("the setup left no goblin-crawl for a goblin's terminal to find")
	}
	if want := "crawler: ready: goblin-crawl, in " + crawler.Dir(h.Root) + "\n"; after.String() != want {
		t.Errorf("doctor after the setup says %q, want %q", after.String(), want)
	}
}

func TestCrawlerSetupThatCannotInstallExitsOneAndSaysWhy(t *testing.T) {
	// Arrange
	h := home.Home{Root: t.TempDir()}
	var stdout, stderr bytes.Buffer

	// Act
	code := runWithRuntime([]string{"crawler", "setup"}, &stdout, &stderr, crawlerRuntime(h, &crawlerPackages{exitCode: 1}))

	// Assert
	if code != 1 || !strings.Contains(stderr.String(), "npm error network request failed") {
		t.Errorf("goblins crawler setup = %d, want 1 naming npm's error\nstderr: %s", code, stderr.String())
	}
}

func TestCrawlerTakesOnlySetup(t *testing.T) {
	for _, args := range [][]string{{"crawler"}, {"crawler", "install"}, {"crawler", "setup", "now"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// Arrange
			var stdout, stderr bytes.Buffer
			runtime := commandRuntime{resolveHome: func() (home.Home, error) {
				t.Error("a refused command resolved the home")
				return home.Home{}, errors.New("no home")
			}}

			// Act
			code := runWithRuntime(args, &stdout, &stderr, runtime)

			// Assert
			if code != 2 || !strings.Contains(stderr.String(), "usage: goblins crawler setup") {
				t.Errorf("goblins %s = %d, want 2 with the usage\nstderr: %s", strings.Join(args, " "), code, stderr.String())
			}
		})
	}
}

// The setup's budget comes from what npm ci took on cold CI runners: under a
// minute in every job that finished, and one was still running when a budget
// of two minutes stopped it. It stays inside an update's whole install, which
// is ended past releaseRefreshWait, with room for what that install does first.
func TestCrawlerSetupBudgetOutlastsASlowNpmAndEndsInsideAnUpdate(t *testing.T) {
	// Arrange
	stoppedStillRunning := 2 * time.Minute
	restOfTheInstall := time.Minute

	// Act
	room := releaseRefreshWait - crawlerSetupWait

	// Assert
	if crawlerSetupWait <= stoppedStillRunning {
		t.Errorf("the crawler setup's budget is %s, and a cold runner's npm ci was still running at %s", crawlerSetupWait, stoppedStillRunning)
	}
	if room < restOfTheInstall {
		t.Errorf("the crawler setup leaves an update's install %s of its %s, want at least %s", room, releaseRefreshWait, restOfTheInstall)
	}
}
