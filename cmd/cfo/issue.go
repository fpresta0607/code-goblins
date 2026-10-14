package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

const issueUsage = "cfo issue file --project <project> --title <title> [--body-file <file>] [--evidence <text>]... [--root <key>]"

// secretName is an environment variable whose value is a credential, which
// no issue may hold.
var secretName = regexp.MustCompile(`(?i)token|secret|password|passwd|credential|api_?key|access_?key|private_?key|_pat$|^pat_`)

func runIssue(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 || args[0] != "file" {
		fmt.Fprintf(stderr, "cfo issue: %s\n", issueUsage)
		return 2
	}
	fs := flag.NewFlagSet("issue file", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectName := fs.String("project", "", "the project whose repository the root issue belongs to")
	title := fs.String("title", "", "what goes wrong, in one line")
	bodyFile := fs.String("body-file", "", "a file holding what happens, why and when it is done")
	rootKey := fs.String("root", "", "the cause a recurrence is matched by, such as a test's package and name; the title by default")
	var evidence []string
	fs.Func("evidence", "what this sighting saw, such as a run's address; repeat for more", func(value string) error {
		if value = strings.TrimSpace(value); value != "" {
			evidence = append(evidence, value)
		}
		return nil
	})
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *projectName == "" || strings.TrimSpace(*title) == "" {
		fmt.Fprintf(stderr, "cfo issue file: --project and --title are required: %s\n", issueUsage)
		return 2
	}
	root := tickets.Root{Title: strings.TrimSpace(*title), Key: *rootKey, Evidence: strings.Join(evidence, "\n")}
	if *bodyFile != "" {
		body, err := fsx.ReadFile(*bodyFile)
		if err != nil {
			fmt.Fprintf(stderr, "cfo issue file: read the body: %v\n", err)
			return 1
		}
		root.Body = string(body)
	}
	if runtime.resolveHome == nil || runtime.repositoryOf == nil {
		fmt.Fprintln(stderr, "cfo issue file: command runtime is incomplete")
		return 1
	}
	checkout, err := runtime.resolveProject(*projectName)
	if err != nil {
		fmt.Fprintf(stderr, "cfo issue file: %v\n", err)
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintf(stderr, "cfo issue file: %v\n", err)
		return 1
	}
	ctx := context.Background()
	repository, err := runtime.repositoryOf(ctx, checkout)
	if err != nil {
		fmt.Fprintf(stderr, "cfo issue file: %v\n", err)
		return 1
	}
	commands := runtime.github
	if commands == nil {
		commands = execx.OSRunner{}
	}
	github := tickets.GitHub{Commands: commands}
	screen, err := issueScreen(ctx, github, runtime, h, checkout, repository, root)
	if err != nil {
		fmt.Fprintf(stderr, "cfo issue file: %v\n", err)
		return 1
	}
	if refusal := screen.Refusal(strings.Join([]string{root.Title, root.Body, root.Evidence, root.Key}, "\n")); refusal != "" {
		where := "private"
		if screen.IsPublic {
			where = "public"
		}
		fmt.Fprintf(stderr, "cfo issue file: refused: the issue would go on %s, which is %s, and %s; write it without that\n", repository, where, refusal)
		return 1
	}
	filed, err := github.FileRoot(ctx, repository, root, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "cfo issue file: %v\n", err)
		return 1
	}
	if filed.IsRecurrence {
		fmt.Fprintf(stdout, "commented on #%d %s: the same root was open, so this sighting is a dated comment on it\n", filed.Number, filed.URL)
		return 0
	}
	fmt.Fprintf(stdout, "filed #%d %s with the %s label\n", filed.Number, filed.URL, tickets.RootLabel)
	return 0
}

// issueScreen is what a root issue filed on repository may not say: every
// credential value this terminal's environment carries, and on a public
// repository the fleet's other projects whose repositories are not public,
// the teammates the supervisor has read, and the repository's owner, whom it
// may mention. Only the projects the issue names are asked about.
func issueScreen(ctx context.Context, github tickets.GitHub, runtime commandRuntime, h home.Home, checkout, repository string, root tickets.Root) (tickets.Screen, error) {
	var screen tickets.Screen
	for _, variable := range os.Environ() {
		name, value, _ := strings.Cut(variable, "=")
		if secretName.MatchString(name) && len(value) >= 8 {
			screen.Secrets = append(screen.Secrets, value)
		}
	}
	isPublic, err := github.IsPublic(ctx, repository)
	if err != nil {
		return tickets.Screen{}, err
	}
	if screen.IsPublic = isPublic; !isPublic {
		return screen, nil
	}
	owner, name, _ := strings.Cut(repository, "/")
	screen.Account = owner
	if screen.People, err = tickets.Teammates(h.State); err != nil {
		return tickets.Screen{}, err
	}
	folders := []string{filepath.Join(h.Data, "projects")}
	if runtime.projectsRoot != nil {
		root, err := runtime.projectsRoot()
		if err != nil {
			return tickets.Screen{}, fmt.Errorf("read the projects root, whose checkouts a public issue may not name: %w", err)
		}
		if root != "" {
			folders = append(folders, root)
		}
	}
	var projects []string
	for _, dir := range folders {
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return tickets.Screen{}, err
		}
		for _, entry := range entries {
			if entry.IsDir() && !strings.EqualFold(entry.Name(), name) && !strings.EqualFold(entry.Name(), filepath.Base(checkout)) {
				projects = append(projects, entry.Name())
			}
		}
	}
	text := strings.Join([]string{root.Title, root.Body, root.Evidence, root.Key}, "\n")
	for _, project := range tickets.Mentioned(text, projects) {
		if !isPublicProject(ctx, github, runtime, project) {
			screen.Private = append(screen.Private, project)
		}
	}
	return screen, nil
}

// isPublicProject says a project's checkout has a GitHub repository anyone
// can see. A project with no checkout here, no GitHub origin or no answer
// from GitHub is not known to be public, so a public issue may not name it.
func isPublicProject(ctx context.Context, github tickets.GitHub, runtime commandRuntime, project string) bool {
	checkout, err := runtime.resolveProject(project)
	if err != nil {
		return false
	}
	repository, err := runtime.repositoryOf(ctx, checkout)
	if err != nil {
		return false
	}
	isPublic, err := github.IsPublic(ctx, repository)
	return err == nil && isPublic
}
