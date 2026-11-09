package main

import (
	"context"
	"fmt"
	"io"
	"time"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/crawler"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

// runCrawler is `ceo crawler setup`, which sets up the crawler connector
// every goblin carries as ceo install does: it puts the build's connector in
// the home and installs the packages its lockfile pins. A connector already
// the build's is left as it is.
func runCrawler(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) != 1 || args[0] != "setup" {
		fmt.Fprintln(stderr, "usage: ceo crawler setup")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	found, err := setUpCrawler(h.Root, runtime)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if found == crawler.Ready {
		fmt.Fprintf(stdout, "crawler: ready: %s, already in %s, so nothing was installed\n", crawler.Command, crawler.Dir(h.Root))
		return 0
	}
	fmt.Fprintf(stdout, "crawler: ready: %s, in %s\n", crawler.Command, crawler.Dir(h.Root))
	return 0
}

// crawlerSetupWait bounds the crawler's setup, whose npm ci can wait on the
// network for as long as npm retries. On cold CI runners it took under a
// minute in every job that finished, and one was still running at two
// minutes. ceo install runs it last, and the board's Update ends an install
// that runs past releaseRefreshWait, so four minutes leaves that install one.
var crawlerSetupWait = 4 * time.Minute

// setUpCrawler makes the crawler connector in the home at root this build's,
// with the npm runtime names, or the machine's. A setup that runs past
// crawlerSetupWait is stopped, npm and every process it started with it.
func setUpCrawler(root string, runtime commandRuntime) (crawler.State, error) {
	npm := runtime.crawlerNpm
	if npm == nil {
		npm = execx.OSRunner{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), crawlerSetupWait)
	defer cancel()
	found, err := crawler.Setup(ctx, root, codegoblins.Crawler, npm)
	if err != nil && ctx.Err() != nil {
		return found, fmt.Errorf("crawler: the setup took longer than %s, so it was stopped: %w", crawlerSetupWait, err)
	}
	return found, err
}

// reportCrawler says whether the crawler connector every goblin carries is
// the build's, or names the fix. It never counts against the health verdict:
// a goblin without it works on, only without a crawler.
func reportCrawler(stdout io.Writer, runtime commandRuntime) {
	h, err := runtime.resolveHome()
	if err != nil {
		return
	}
	state, err := crawler.Check(h.Root, codegoblins.Crawler)
	switch {
	case err != nil:
		fmt.Fprintf(stdout, "crawler: unreadable (%v)\n", err)
	case state == crawler.Ready:
		fmt.Fprintf(stdout, "crawler: ready: %s, in %s\n", crawler.Command, crawler.Dir(h.Root))
	case state == crawler.Stale:
		fmt.Fprintf(stdout, "crawler: older than this build in %s; run `ceo crawler setup`\n", crawler.Dir(h.Root))
	default:
		fmt.Fprintf(stdout, "crawler: not set up, so goblins carry no %s; run `ceo crawler setup`\n", crawler.Command)
	}
}
