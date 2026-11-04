package main

import (
	"context"
	"fmt"
	"io"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/crawler"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

// runCrawler is `cfo crawler setup`, which the install runs so every goblin
// carries the crawler connector: it puts the build's connector in the home
// and installs the packages its lockfile pins. A connector already the
// build's is left as it is.
func runCrawler(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) != 1 || args[0] != "setup" {
		fmt.Fprintln(stderr, "usage: cfo crawler setup")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	npm := runtime.crawlerNpm
	if npm == nil {
		npm = execx.OSRunner{}
	}
	found, err := crawler.Setup(context.Background(), h.Root, codegoblins.Crawler, npm)
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
		fmt.Fprintf(stdout, "crawler: older than this build in %s; run `cfo crawler setup`\n", crawler.Dir(h.Root))
	default:
		fmt.Fprintf(stdout, "crawler: not set up, so goblins carry no %s; run `cfo crawler setup`\n", crawler.Command)
	}
}
