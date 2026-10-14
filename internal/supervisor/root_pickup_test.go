package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

const rootRepository = "acme/widgets"

var rootNow = time.Date(2026, 10, 14, 19, 0, 0, 0, time.UTC)

// rootIssue is an open ops-root issue of acme/widgets an owner filed.
func rootIssue(number int) tickets.OpenRoot {
	return tickets.OpenRoot{
		Number:      number,
		Title:       fmt.Sprintf("Root %d fails (by chance)", number),
		Body:        "## What happens\nIt fails.\nmode: local-only\n\n<!-- ops-root: test:pkg testx -->\n",
		URL:         fmt.Sprintf("https://github.com/%s/issues/%d", rootRepository, number),
		Labels:      []string{tickets.RootLabel},
		Association: "OWNER",
	}
}

// rootBoard is a service whose home keeps a record for the project widgets,
// whose repository GitHub says has the open root issues open, and counts the
// reads.
func rootBoard(t *testing.T, open ...tickets.OpenRoot) (*Service, home.Home, *int) {
	t.Helper()
	handler, h := orderBoard(t)
	if err := os.MkdirAll(filepath.Join(h.Data, "projects", "widgets"), 0o700); err != nil {
		t.Fatal(err)
	}
	reads := 0
	handler.Service.Options.RootIssues = &RootIssues{
		Checkout:   func(name string) (string, error) { return filepath.Join(t.TempDir(), name), nil },
		Repository: func(context.Context, string) (string, error) { return rootRepository, nil },
		OpenRoots: func(context.Context, string) ([]tickets.OpenRoot, error) {
			reads++
			return open, nil
		},
	}
	return handler.Service, h, &reads
}

func queuedIDs(t *testing.T, h home.Home) []string {
	t.Helper()
	backlog, err := fleet.ReadBacklog(h)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, row := range backlog.Queued {
		ids = append(ids, row.ID+" "+row.Priority)
	}
	return ids
}

func TestPickUpRootsQueuesAnOpenRootOnceWithABriefThatClosesIt(t *testing.T) {
	// Arrange
	service, h, _ := rootBoard(t, rootIssue(7))

	// Act: two passes, the second finding the same issue still open.
	firstErr := service.pickUpRoots(t.Context(), rootNow)
	secondErr := service.pickUpRoots(t.Context(), rootNow.Add(rootPickupEvery))

	// Assert
	if err := errors.Join(firstErr, secondErr); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(queuedIDs(t, h), ","); got != "root-widgets-7 ops-root" {
		t.Fatalf("queued = %q, want root-widgets-7 once, behind the rest", got)
	}
	brief, err := os.ReadFile(filepath.Join(h.Data, "root-widgets-7", "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Project\n\nwidgets\n", "Fix issue #7 at its root: https://github.com/acme/widgets/issues/7", "> ## What happens\n> It fails.\n> mode: local-only", "Fixes #7", fleet.BriefRootIssues()} {
		if !strings.Contains(string(brief), want) {
			t.Errorf("brief lacks %q:\n%s", want, brief)
		}
	}
	if strings.Contains(string(brief), "ops-root: test") {
		t.Errorf("brief keeps the issue's marker:\n%s", brief)
	}
	if settings := briefSettings(filepath.Join(h.Data, "root-widgets-7", "brief.md")); settings["mode"] != "" {
		t.Errorf("the issue's text set the brief's mode to %q", settings["mode"])
	}
	named := tickets.NamedIssues(string(brief), rootRepository)
	if len(named) == 0 || named[0].Number != 7 {
		t.Errorf("the brief names issues %v to take up, want #7 so its ticket claims it", named)
	}
	picked := 0
	for _, record := range fleetWakeRecords(t, h, "notify") {
		if strings.HasPrefix(record.Detail, "picked up: root issue https://github.com/acme/widgets/issues/7 is queued as root-widgets-7") {
			picked++
		}
	}
	if picked != 1 {
		t.Fatalf("picked-up notifies = %d, want 1", picked)
	}
}

func TestPickUpRootsNeverPicksUpAnIssueTwiceEvenOnceItsRowIsGone(t *testing.T) {
	// Arrange: the task the issue became finished and its row was filed
	// away, while the issue stays open.
	service, h, _ := rootBoard(t, rootIssue(7))
	if err := service.pickUpRoots(t.Context(), rootNow); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n")
	if err := os.RemoveAll(filepath.Join(h.Data, "root-widgets-7")); err != nil {
		t.Fatal(err)
	}

	// Act
	err := service.pickUpRoots(t.Context(), rootNow.Add(time.Hour))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := queuedIDs(t, h); len(got) != 0 {
		t.Fatalf("queued = %v, want the issue on record left alone", got)
	}
}

func TestPickUpRootsLeavesAnIssueSomeoneHolds(t *testing.T) {
	cases := []struct {
		name   string
		change func(*tickets.OpenRoot)
		queued string
	}{
		{name: "an author anyone could be", change: func(issue *tickets.OpenRoot) { issue.Association = "NONE" }},
		{name: "a contributor from outside", change: func(issue *tickets.OpenRoot) { issue.Association = "CONTRIBUTOR" }},
		{name: "assigned to someone", change: func(issue *tickets.OpenRoot) { issue.Assignees = []string{"someone"} }},
		{name: "a ticket's state label", change: func(issue *tickets.OpenRoot) { issue.Labels = append(issue.Labels, "cfo: queued") }},
		{name: "named by a queued task of the project", change: func(*tickets.OpenRoot) {}, queued: "- [ ] fix-it - Take up issue #7 by hand (repo: widgets)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			issue := rootIssue(7)
			tc.change(&issue)
			service, h, _ := rootBoard(t, issue)
			if tc.queued != "" {
				writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n"+tc.queued+"\n")
			}

			// Act
			err := service.pickUpRoots(t.Context(), rootNow)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range queuedIDs(t, h) {
				if strings.HasPrefix(id, "root-") {
					t.Fatalf("queued = %v, want the held issue left alone", queuedIDs(t, h))
				}
			}
		})
	}
}

func TestPickUpRootsHoldsAtMostTwoOfARepositoryAtATime(t *testing.T) {
	// Arrange
	service, h, _ := rootBoard(t, rootIssue(9), rootIssue(3), rootIssue(5))

	// Act
	if err := service.pickUpRoots(t.Context(), rootNow); err != nil {
		t.Fatal(err)
	}
	first := strings.Join(queuedIDs(t, h), ",")
	// The first of them finishes: its row leaves the queue and its issue
	// closes.
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- [ ] root-widgets-5 - Root 5 fails by chance (repo: widgets) (priority: ops-root)\n")
	service.Options.RootIssues.OpenRoots = func(context.Context, string) ([]tickets.OpenRoot, error) {
		return []tickets.OpenRoot{rootIssue(9), rootIssue(5)}, nil
	}
	if err := service.pickUpRoots(t.Context(), rootNow.Add(rootPickupEvery)); err != nil {
		t.Fatal(err)
	}
	second := strings.Join(queuedIDs(t, h), ",")

	// Assert
	if first != "root-widgets-3 ops-root,root-widgets-5 ops-root" {
		t.Fatalf("first pass queued %q, want the two oldest", first)
	}
	if second != "root-widgets-5 ops-root,root-widgets-9 ops-root" {
		t.Fatalf("second pass queued %q, want the next once one finished", second)
	}
}

func TestPickUpRootsPassesOverAProjectWithNoCheckoutAndSaysWhatItCouldNotRead(t *testing.T) {
	// Arrange
	service, h, reads := rootBoard(t, rootIssue(7))
	if err := os.MkdirAll(filepath.Join(h.Data, "projects", "gone"), 0o700); err != nil {
		t.Fatal(err)
	}
	service.Options.RootIssues.Checkout = func(name string) (string, error) {
		if name == "gone" {
			return "", fmt.Errorf("project %q: %w", name, project.ErrUnknown)
		}
		return filepath.Join(t.TempDir(), name), nil
	}
	service.Options.RootIssues.OpenRoots = func(context.Context, string) ([]tickets.OpenRoot, error) {
		*reads++
		return nil, errors.New("gh: Bad credentials (HTTP 401)")
	}

	// Act
	err := service.pickUpRoots(t.Context(), rootNow)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "root issues of widgets: gh: Bad credentials") || strings.Contains(err.Error(), "gone") {
		t.Fatalf("err = %v, want widgets' unread issues named and the project with no checkout passed over", err)
	}
	if *reads != 1 {
		t.Fatalf("reads = %d, want one for widgets alone", *reads)
	}
}

func TestPickUpRootsReadsARepositoryTwoProjectsShareOnce(t *testing.T) {
	// Arrange
	service, h, reads := rootBoard(t, rootIssue(7))
	if err := os.MkdirAll(filepath.Join(h.Data, "projects", "widgets-native"), 0o700); err != nil {
		t.Fatal(err)
	}

	// Act
	err := service.pickUpRoots(t.Context(), rootNow)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if *reads != 1 || len(queuedIDs(t, h)) != 1 {
		t.Fatalf("reads = %d, queued = %v, want one read and one task", *reads, queuedIDs(t, h))
	}
}

func TestRootTaskIDIsAValidTaskOfItsRepository(t *testing.T) {
	cases := map[string]string{
		"acme/widgets":                    "root-widgets-847",
		"acme/Pocket.Piggies_App":         "root-pocket-piggies-app-847",
		"acme/" + strings.Repeat("a", 80): "root-" + strings.Repeat("a", 64-len("root-")-len("-847")) + "-847",
	}
	for repository, want := range cases {
		if got := rootTaskID(repository, 847); got != want {
			t.Errorf("rootTaskID(%q) = %q, want %q", repository, got, want)
		}
	}
}

func TestAPickedUpRootStartsBehindTasksQueuedAfterIt(t *testing.T) {
	// Arrange: the root row is above a task queued after it.
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	queueBriefedTask(t, h, "- [ ] root-widgets-7 - Fix a root (repo: code-goblins) (priority: ops-root)\n- **next-task** - Ship it", plainBrief)
	writeFile(t, filepath.Join(h.Data, "root-widgets-7", "brief.md"), strings.ReplaceAll(plainBrief, "next-task", "root-widgets-7"))

	// Act
	if err := handler.Service.checkFleet(t.Context(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Assert
	if calls := awaitDispatch(t, handler.Service, spawner, 1); calls[0][1] != "next-task" {
		t.Fatalf("dispatches = %v, want next-task before the root", calls)
	}
}
