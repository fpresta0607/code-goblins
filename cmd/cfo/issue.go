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
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
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
	filed, err := fileRoot(context.Background(), runtime, *projectName, root, time.Now())
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

// fileRoot files root on the repository of the project projectArg names,
// a name or a path, through the screen of what the issue may say: the one
// filing path the command, a merge train and the supervisor share. It
// refuses a repository the fleet does not own and one with issues turned
// off. A refusal is an error that says what it found.
func fileRoot(ctx context.Context, runtime commandRuntime, projectArg string, root tickets.Root, now time.Time) (tickets.FiledRoot, error) {
	if runtime.resolveHome == nil || runtime.repositoryOf == nil {
		return tickets.FiledRoot{}, errors.New("command runtime is incomplete")
	}
	checkout, err := runtime.resolveProject(projectArg)
	if err != nil {
		return tickets.FiledRoot{}, err
	}
	h, err := runtime.resolveHome()
	if err != nil {
		return tickets.FiledRoot{}, err
	}
	repository, err := runtime.repositoryOf(ctx, checkout)
	if err != nil {
		return tickets.FiledRoot{}, err
	}
	commands := runtime.github
	if commands == nil {
		commands = execx.OSRunner{}
	}
	github := tickets.GitHub{Commands: commands}
	read, err := github.ReadRepository(ctx, repository)
	if err != nil {
		return tickets.FiledRoot{}, err
	}
	if err := refuseAnotherOwner(ctx, github, h, checkout, repository, read.Owner); err != nil {
		return tickets.FiledRoot{}, err
	}
	if !read.HasIssues {
		return tickets.FiledRoot{}, fmt.Errorf("refused: %s has issues turned off, so no issue can go on it", repository)
	}
	screen, err := issueScreen(ctx, github, runtime, h, checkout, repository, read.IsPublic, root)
	if err != nil {
		return tickets.FiledRoot{}, err
	}
	if refusal := screen.Refusal(strings.Join([]string{root.Title, root.Body, root.Evidence, root.Key}, "\n")); refusal != "" {
		where := "private"
		if screen.IsPublic {
			where = "public"
		}
		return tickets.FiledRoot{}, fmt.Errorf("refused: the issue would go on %s, which is %s, and %s; write it without that", repository, where, refusal)
	}
	return github.FileRoot(ctx, repository, root, now)
}

// refuseAnotherOwner refuses repository, which the checkout's origin names
// and owner owns, unless the fleet owns it: owner is the account gh works as
// or one config/fleet.json's github_owners names, the rule the supervisor's
// pull request watch keeps. A checkout of a fork may keep origin on the
// fork's parent, another owner's repository, so the refusal names each
// remote of the checkout whose repository the fleet owns, where the issue
// belongs instead.
func refuseAnotherOwner(ctx context.Context, github tickets.GitHub, h home.Home, checkout, repository, owner string) error {
	account, err := ghText(ctx, github.Commands, "api", "user", "--jq", ".login")
	if err == nil && account == "" {
		err = errors.New("gh named no account it works as")
	}
	if err != nil {
		return fmt.Errorf("read the account gh works as, whose repositories an issue may go on: %w", err)
	}
	if strings.EqualFold(owner, account) {
		return nil
	}
	settings, err := fleetconfig.Read(h.Root)
	if err != nil {
		return fmt.Errorf("%s is not %s's, the account gh works as, and config/fleet.json, whose github_owners name the fleet's other owners, cannot be read: %w", repository, account, err)
	}
	isFleets := func(name string) bool {
		return strings.EqualFold(name, account) || slices.ContainsFunc(settings.GitHubOwners, func(named string) bool { return strings.EqualFold(named, name) })
	}
	if isFleets(owner) {
		return nil
	}
	refusal := fmt.Sprintf("refused: the issue would go on %s, which origin names, and its owner %s is neither %s, the account gh works as, nor an owner config/fleet.json names", repository, owner, account)
	remotes, err := github.Remotes(ctx, checkout)
	if err != nil {
		return fmt.Errorf("%s; %w", refusal, err)
	}
	var fleets []string
	for _, remote := range remotes {
		if remoteOwner, _, _ := strings.Cut(remote.Repository, "/"); isFleets(remoteOwner) {
			fleets = append(fleets, remote.Repository+", which remote "+remote.Name+" names")
		}
	}
	if len(fleets) == 0 {
		return errors.New(refusal + "; no remote of the checkout names a repository the fleet owns")
	}
	return errors.New(refusal + "; the fleet owns " + strings.Join(fleets, ", and "))
}

// issueScreen is what a root issue filed on repository, public or not, may
// not say: every credential value this terminal's environment carries, and
// on a public repository the fleet's other projects whose repositories are
// not public, the teammates the supervisor has read, and the repository's
// owner, whom it may mention. Only the projects the issue names are asked
// about.
func issueScreen(ctx context.Context, github tickets.GitHub, runtime commandRuntime, h home.Home, checkout, repository string, isPublic bool, root tickets.Root) (tickets.Screen, error) {
	var screen tickets.Screen
	for _, variable := range os.Environ() {
		name, value, _ := strings.Cut(variable, "=")
		if secretName.MatchString(name) && len(value) >= 8 {
			screen.Secrets = append(screen.Secrets, value)
		}
	}
	if screen.IsPublic = isPublic; !isPublic {
		return screen, nil
	}
	owner, name, _ := strings.Cut(repository, "/")
	screen.Account = owner
	var err error
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
	read, err := github.ReadRepository(ctx, repository)
	return err == nil && read.IsPublic
}

// fileRootFrom is the supervisor's filing path, the one cfo issue file uses,
// or nil on the example board, whose made-up work must never reach GitHub.
func fileRootFrom(runtime commandRuntime, isExample bool) func(context.Context, string, tickets.Root) (tickets.FiledRoot, error) {
	if isExample {
		return nil
	}
	return func(ctx context.Context, project string, root tickets.Root) (tickets.FiledRoot, error) {
		return fileRoot(ctx, runtime, project, root, time.Now())
	}
}
