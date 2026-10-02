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
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

const ticketsUsage = "cfo tickets <project> [--brief <file>] [--files <paths>] [--json] | cfo tickets <project> --allow-public-tickets"

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

// ticketWriter is what the supervisor keeps tickets with: gh for GitHub, and
// the machine's projects root for a task that names its project by a bare
// name.
func ticketWriter(commands execx.Runner, runtime commandRuntime) *supervisor.Tickets {
	github := tickets.GitHub{Commands: commands}
	return &supervisor.Tickets{
		Checkout:      runtime.resolveProject,
		Repository:    github.RepositoryOf,
		Collaboration: github.Collaboration,
		EnsureLabels:  github.EnsureLabels,
		Apply:         github.Apply,
	}
}

// allowPublicTickets records the CFO's word that tickets may be kept in a
// project's public repository, where each task's issue is public.
func allowPublicTickets(checkout string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if runtime.repositoryOf == nil || runtime.resolveHome == nil {
		fmt.Fprintln(stderr, "cfo tickets: command runtime is incomplete")
		return 1
	}
	repository, err := runtime.repositoryOf(context.Background(), checkout)
	if err != nil {
		fmt.Fprintf(stderr, "cfo tickets: %v\n", err)
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintf(stderr, "cfo tickets: %v\n", err)
		return 1
	}
	if err := tickets.AllowPublic(h.State, repository); err != nil {
		fmt.Fprintf(stderr, "cfo tickets: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Tickets may now be kept in %s. It is public, so each task's issue there is public: its title, its state, who is on it and its pull request.\n", repository)
	return 0
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
	allowPublic := fs.Bool("allow-public-tickets", false, "let the supervisor keep each task's ticket in this project's repository although it is public; asked once per repository")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "cfo tickets: unexpected arguments: %s\n", ticketsUsage)
		return 2
	}
	if *allowPublic && (*briefPath != "" || len(files) > 0 || *jsonOutput) {
		fmt.Fprintf(stderr, "cfo tickets: --allow-public-tickets takes no other flag: %s\n", ticketsUsage)
		return 2
	}
	if !*allowPublic && runtime.repoActivity == nil {
		fmt.Fprintln(stderr, "cfo tickets: command runtime is incomplete")
		return 1
	}
	checkout, err := runtime.resolveProject(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "cfo tickets: %v\n", err)
		return 1
	}
	if *allowPublic {
		return allowPublicTickets(checkout, stdout, stderr, runtime)
	}
	var area *tickets.Area
	if *briefPath != "" || len(files) > 0 {
		value := tickets.Area{}
		if *briefPath != "" {
			text, err := os.ReadFile(*briefPath)
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
