package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/pagecheck"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// pageCheckTimeout bounds a whole check: a hidden Chrome's start, two widths
// each waiting at most 30 seconds on the layout gate, and its close.
var pageCheckTimeout = 2 * time.Minute

// runPage checks a Scrawl page a goblin wrote without the goblin's own
// browser (#2542):
//
//	goblins page check <html-file> [--out <folder>]
//
// It opens the page's review session as goblins review does, without a
// browser, then pagecheck draws it in a hidden Chrome at a desktop and a
// phone width, saves a picture at each and lists every layout issue
// lavish-axi's gate found, as the Overlord's Layout issues inbox would show
// it. It exits 0 with none, and 1 with any or when it could not check.
func runPage(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) < 2 || args[0] != "check" || strings.HasPrefix(args[1], "-") {
		fmt.Fprintln(stderr, "usage: goblins page check <html-file> [--out <folder>]")
		return 2
	}
	f := flag.NewFlagSet("page check", flag.ContinueOnError)
	f.SetOutput(stderr)
	out := f.String("out", "", "the folder its pictures are saved in; <page>-check beside the page when not given")
	if !parseFlags(f, args[2:]) {
		return 2
	}
	page, err := lavishPageFile(args[1])
	if err != nil {
		fmt.Fprintln(stderr, "goblins page check: "+err.Error())
		return 2
	}
	if *out == "" {
		*out = strings.TrimSuffix(page, filepath.Ext(page)) + "-check"
	}
	commands := runtime.lavish
	if commands == nil {
		commands = execx.OSRunner{}
	}
	check := runtime.checkPage
	if check == nil {
		check = func(ctx context.Context, session, out string) (pagecheck.Report, error) {
			executable, err := browserlogin.Executable()
			if err != nil {
				return pagecheck.Report{}, fmt.Errorf("no Chrome to draw the page in: %w", err)
			}
			return pagecheck.Check(ctx, executable, session, out)
		}
	}
	openCtx, openCancel := context.WithTimeout(context.Background(), pageOpenTimeout)
	session, err := (axi.Lavish{Commands: commands}).Open(openCtx, page)
	openCancel()
	if errors.Is(err, axi.ErrSessionEnded) {
		fmt.Fprintln(stderr, "goblins page check: "+supervisor.EndedPage(page).Error())
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "goblins page check: lavish-axi cannot show %s (%v)\n", page, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), pageCheckTimeout)
	defer cancel()
	report, err := check(ctx, session, *out)
	if err != nil {
		fmt.Fprintln(stderr, "goblins page check: "+err.Error())
		return 1
	}
	for _, picture := range report.Pictures {
		fmt.Fprintf(stdout, "picture %s, %d pixels wide: %s\n", picture.View.Name, picture.View.Width, picture.Path)
	}
	if len(report.Warnings) == 0 {
		fmt.Fprintln(stdout, "layout: no issues at the desktop or the phone width")
		return 0
	}
	fmt.Fprintf(stdout, "layout: %d issues, as his Layout issues inbox shows them:\n", len(report.Warnings))
	for _, warning := range report.Warnings {
		fmt.Fprintf(stdout, "- %s, %s width, at %s: %s\n", warning.Title, warning.ViewportClass, warning.Selector, warning.Explanation)
	}
	return 1
}
