package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

const issueUnownedUsage = "goblins issue unowned"

// unownedIssue is one open issue the listing names, with the tasks that are
// neither live nor queued that name it.
type unownedIssue struct {
	Number int
	Title  string
	URL    string
	Tasks  []string
}

// unownedRepository is one GitHub repository of the home's projects as the
// listing read it: how many issues are open, how many of them carry the
// root clean-up's label, how many a live or queued task names, and apart
// the ones only finished or parked tasks name and the ones no task names.
type unownedRepository struct {
	Repository   string
	Projects     []string
	Open         int
	Roots        int
	Owned        int
	FinishedOnly []unownedIssue
	Unnamed      []unownedIssue
	Problem      string
}

// unownedListing is the whole listing: each repository, and what could not
// be read, which leaves the issues it would have named listed as unnamed.
type unownedListing struct {
	Repositories []unownedRepository
	Problems     []string
}

// runIssueUnowned lists, for every project of the home, the open issues
// without the root clean-up's label that no live or queued task names, and
// apart those only finished or parked tasks name. It reads only.
func runIssueUnowned(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) != 0 {
		fmt.Fprintf(stderr, "goblins issue unowned: %s\n", issueUnownedUsage)
		return 2
	}
	if runtime.resolveHome == nil || runtime.repositoryOf == nil || runtime.githubAPI == nil {
		fmt.Fprintln(stderr, "goblins issue unowned: command runtime is incomplete")
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintf(stderr, "goblins issue unowned: %v\n", err)
		return 1
	}
	listing, err := listUnownedIssues(context.Background(), h, runtime)
	if err != nil {
		fmt.Fprintf(stderr, "goblins issue unowned: %v\n", err)
		return 1
	}
	renderUnownedIssues(stdout, listing)
	return 0
}

// listUnownedIssues reads the home's projects, its live, queued, parked and
// finished tasks and what GitHub says of their pull requests and of each
// repository's open issues.
func listUnownedIssues(ctx context.Context, h home.Home, runtime commandRuntime) (unownedListing, error) {
	commands := runtime.github
	if commands == nil {
		commands = execx.OSRunner{}
	}
	github := tickets.GitHub{Commands: commands, API: runtime.githubAPI}
	var listing unownedListing
	repositories := map[string]string{}
	repositoryOf := func(checkout string) string {
		key := strings.ToLower(filepath.Clean(checkout))
		if repository, isKnown := repositories[key]; isKnown {
			return repository
		}
		repository := ""
		if _, err := os.Stat(checkout); err == nil {
			if repository, err = issueRepository(ctx, h, runtime.repositoryOf, checkout); err != nil {
				listing.Problems = append(listing.Problems, fmt.Sprintf("the repository of %s cannot be read: %v", checkout, err))
			}
		}
		repositories[key] = repository
		return repository
	}
	projectRepository := func(name string) string {
		if name == "" {
			return ""
		}
		checkout, err := runtime.resolveProject(name)
		if err != nil {
			return ""
		}
		return repositoryOf(checkout)
	}

	entries, err := os.ReadDir(filepath.Join(h.Data, "projects"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return unownedListing{}, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		checkout, err := runtime.resolveProject(entry.Name())
		if err != nil {
			listing.Problems = append(listing.Problems, fmt.Sprintf("project %s is not read: %v", entry.Name(), err))
			continue
		}
		repository := repositoryOf(checkout)
		if repository == "" {
			continue
		}
		index := slices.IndexFunc(listing.Repositories, func(known unownedRepository) bool { return known.Repository == repository })
		if index < 0 {
			listing.Repositories = append(listing.Repositories, unownedRepository{Repository: repository})
			index = len(listing.Repositories) - 1
		}
		listing.Repositories[index].Projects = append(listing.Repositories[index].Projects, entry.Name())
	}

	owners, others, err := readNamingTasks(h, repositoryOf, projectRepository, &listing)
	if err != nil {
		return unownedListing{}, err
	}
	var pulls []string
	for _, task := range append(slices.Clone(owners), others...) {
		pulls = append(pulls, task.PullRequests...)
	}
	pullIssues, unread, err := github.PullRequestIssues(ctx, pulls)
	if err != nil {
		return unownedListing{}, err
	}
	if len(unread) > 0 {
		listing.Problems = append(listing.Problems, fmt.Sprintf("GitHub shows no pull request at %d of the tasks' addresses, so what they name is not read: %s", len(unread), strings.Join(unread, ", ")))
	}
	isOwned := map[tickets.IssueRef]bool{}
	for _, task := range owners {
		for _, issue := range task.issues(pullIssues) {
			isOwned[issue] = true
		}
	}
	namedBy := map[tickets.IssueRef][]string{}
	for _, task := range others {
		for _, issue := range task.issues(pullIssues) {
			namedBy[issue] = append(namedBy[issue], task.ID)
		}
	}

	for index := range listing.Repositories {
		repository := &listing.Repositories[index]
		open, err := github.OpenIssues(ctx, repository.Repository)
		if err != nil {
			repository.Problem = err.Error()
			continue
		}
		repository.Open = len(open)
		for _, issue := range open {
			ref := tickets.IssueRef{Repository: repository.Repository, Number: issue.Number}
			listed := unownedIssue{Number: issue.Number, Title: issue.Title, URL: issue.URL, Tasks: namedBy[ref]}
			switch {
			case slices.Contains(issue.Labels, tickets.RootLabel):
				repository.Roots++
			case isOwned[ref]:
				repository.Owned++
			case len(listed.Tasks) > 0:
				repository.FinishedOnly = append(repository.FinishedOnly, listed)
			default:
				repository.Unnamed = append(repository.Unnamed, listed)
			}
		}
	}
	return listing, nil
}

// readNamingTasks reads the home's tasks as the issues they name: owners,
// the live tasks and the queued rows, and others, the parked rows and the
// finished tasks, each a task whose record, status log, outcome or Done row
// the home still keeps. A task named twice is read once, with every row it
// has.
func readNamingTasks(h home.Home, repositoryOf, projectRepository func(string) string, listing *unownedListing) (owners, others []namingTask, err error) {
	scan, err := state.ScanIDs(h.State)
	if err != nil {
		return nil, nil, err
	}
	backlog, err := fleet.ReadBacklog(h)
	if err != nil {
		return nil, nil, err
	}
	tasks := map[string]*namingTask{}
	var order []string
	isOwner := map[string]bool{}
	read := func(id, repository, briefPath string) (*namingTask, error) {
		if task, isRead := tasks[id]; isRead {
			return task, nil
		}
		task := &namingTask{ID: id, Repository: repository}
		var err error
		if task.Brief, err = taskBrief(h, id, briefPath); err != nil {
			return nil, err
		}
		if task.PullRequests, err = taskPullRequests(h.State, id); err != nil {
			return nil, err
		}
		tasks[id] = task
		order = append(order, id)
		return task, nil
	}
	for _, id := range scan.MetaIDs {
		meta, err := state.ReadTaskMeta(h.State, id)
		if err != nil {
			listing.Problems = append(listing.Problems, fmt.Sprintf("the record of live task %s cannot be read, so the issues it names are not left out: %v", id, err))
			continue
		}
		if _, err := read(id, repositoryOf(meta.Project), meta.Brief); err != nil {
			return nil, nil, err
		}
		isOwner[id] = true
	}
	rows := []struct {
		rows    []fleet.BacklogRow
		isOwner bool
	}{{backlog.Queued, true}, {backlog.Parked, false}, {backlog.Done, false}}
	for _, section := range rows {
		for _, row := range section.rows {
			if !row.Structured || state.ValidTaskID(row.ID) != nil {
				continue
			}
			task, err := read(row.ID, projectRepository(row.Repo), "")
			if err != nil {
				return nil, nil, err
			}
			if task.Repository == "" {
				task.Repository = projectRepository(row.Repo)
			}
			task.Row = strings.TrimSpace(task.Row + "\n" + row.Raw)
			isOwner[row.ID] = isOwner[row.ID] || section.isOwner
		}
	}
	outcomes, err := os.ReadDir(filepath.Join(h.State, "outcomes"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	finished := slices.Clone(scan.OrphanStatusIDs)
	for _, entry := range outcomes {
		if id, isOutcome := strings.CutSuffix(entry.Name(), ".json"); isOutcome && !entry.IsDir() {
			finished = append(finished, id)
		}
	}
	for _, id := range finished {
		if state.ValidTaskID(id) != nil {
			continue
		}
		repository := ""
		if outcome, err := state.ReadOutcome(h.State, id); err == nil {
			repository = repositoryOf(outcome.Project)
		}
		task, err := read(id, repository, "")
		if err != nil {
			return nil, nil, err
		}
		if task.Repository == "" {
			task.Repository = repository
		}
	}
	for _, id := range order {
		if isOwner[id] {
			owners = append(owners, *tasks[id])
		} else {
			others = append(others, *tasks[id])
		}
	}
	return owners, others, nil
}

// renderUnownedIssues writes the listing as one block per repository.
func renderUnownedIssues(w io.Writer, listing unownedListing) {
	for _, repository := range listing.Repositories {
		fmt.Fprintf(w, "%s (%s): ", repository.Repository, strings.Join(repository.Projects, ", "))
		if repository.Problem != "" {
			fmt.Fprintf(w, "not read: %s\n", repository.Problem)
			continue
		}
		fmt.Fprintf(w, "%d open, %d %s, %d named by live or queued tasks, %d named only by finished or parked tasks, %d named by no task\n", repository.Open, repository.Roots, tickets.RootLabel, repository.Owned, len(repository.FinishedOnly), len(repository.Unnamed))
		if len(repository.FinishedOnly) > 0 {
			fmt.Fprintln(w, "  named only by finished or parked tasks:")
			for _, issue := range repository.FinishedOnly {
				fmt.Fprintf(w, "    #%d %s (%s) %s\n", issue.Number, issue.Title, strings.Join(issue.Tasks, ", "), issue.URL)
			}
		}
		if len(repository.Unnamed) > 0 {
			fmt.Fprintln(w, "  named by no task:")
			for _, issue := range repository.Unnamed {
				fmt.Fprintf(w, "    #%d %s %s\n", issue.Number, issue.Title, issue.URL)
			}
		}
	}
	for _, problem := range listing.Problems {
		fmt.Fprintf(w, "not read: %s\n", problem)
	}
}
