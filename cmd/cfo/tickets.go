package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

const ticketsUsage = "cfo tickets <project> [--brief <file>] [--files <paths>] [--json]"

// readRepositoryActivity reads what GitHub says is happening in the
// repository a checkout's origin names.
func readRepositoryActivity(ctx context.Context, checkout string, now time.Time) (tickets.Activity, error) {
	github := tickets.GitHub{Commands: execx.OSRunner{}}
	repository, err := github.RepositoryOf(ctx, checkout)
	if err != nil {
		return tickets.Activity{}, err
	}
	return github.Read(ctx, repository, now)
}

// checkoutHas reports whether a checkout has a repository path, written with
// forward slashes.
func checkoutHas(checkout string) func(repositoryPath string) bool {
	return func(repositoryPath string) bool {
		_, err := os.Stat(filepath.Join(checkout, filepath.FromSlash(repositoryPath)))
		return err == nil
	}
}

func runTickets(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(stderr, "cfo tickets: a project is required: %s\n", ticketsUsage)
		return 2
	}
	fs := flag.NewFlagSet("tickets", flag.ContinueOnError)
	fs.SetOutput(stderr)
	briefPath := fs.String("brief", "", "a brief whose Task and Acceptance criteria sections name the area to check for overlap")
	var files []string
	fs.Func("files", "repository paths to check for overlap, comma-separated or repeated", func(value string) error {
		for _, file := range strings.Split(value, ",") {
			if file = strings.TrimSpace(file); file != "" {
				files = append(files, file)
			}
		}
		return nil
	})
	jsonOutput := fs.Bool("json", false, "render the report as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "cfo tickets: unexpected arguments: %s\n", ticketsUsage)
		return 2
	}
	if runtime.repoActivity == nil {
		fmt.Fprintln(stderr, "cfo tickets: command runtime is incomplete")
		return 1
	}
	checkout, err := runtime.resolveProject(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "cfo tickets: %v\n", err)
		return 1
	}
	var area *tickets.Area
	if *briefPath != "" || len(files) > 0 {
		value := tickets.Area{}
		if *briefPath != "" {
			text, err := fsx.ReadFile(*briefPath)
			if err != nil {
				fmt.Fprintf(stderr, "cfo tickets: read the brief: %v\n", err)
				return 1
			}
			value = tickets.BriefArea(string(text), checkoutHas(checkout))
		}
		value, ignored := value.WithPaths(files...)
		for _, file := range ignored {
			fmt.Fprintf(stderr, "cfo tickets: --files %s is not a repository path, so it is ignored\n", file)
		}
		if len(value.Paths) == 0 {
			switch {
			case *briefPath != "" && len(files) > 0:
				fmt.Fprintln(stderr, "cfo tickets: neither the brief nor --files names a repository path, so only issue text is compared")
			case *briefPath != "":
				fmt.Fprintln(stderr, "cfo tickets: the brief names no repository path, so only issue text is compared; add --files <paths> to compare changed files")
			default:
				fmt.Fprintln(stderr, "cfo tickets: --files names no repository path, so nothing is compared")
			}
		}
		area = &value
	}
	now := time.Now()
	activity, err := runtime.repoActivity(context.Background(), checkout, now)
	if err != nil {
		fmt.Fprintf(stderr, "cfo tickets: %v\n", err)
		return 1
	}
	report := tickets.Build(activity, now, area)
	if *jsonOutput {
		err = tickets.RenderJSON(stdout, report)
	} else {
		err = tickets.RenderText(stdout, report)
	}
	if err != nil {
		fmt.Fprintf(stderr, "cfo tickets: %v\n", err)
		return 1
	}
	return 0
}
