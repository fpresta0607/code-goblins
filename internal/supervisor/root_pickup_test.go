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

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/state"
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
		ClosingPullRequests: func(context.Context, string, int) ([]string, error) { return nil, nil },
	}
	return handler.Service, h, &reads
}

// fileRootAsLiveTask makes task a live task of h that filed the root issue
// at url with cfo issue file.
func fileRootAsLiveTask(t *testing.T, h home.Home, task, url string) {
	t.Helper()
	taskTmp := filepath.Join(h.State, "tasktmp", task)
	if err := os.MkdirAll(taskTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, state.TaskMetaPath(h.State, task), "project=widgets\ntasktmp="+taskTmp+"\n")
	if err := tickets.RecordFiledRoot(taskTmp, url); err != nil {
		t.Fatal(err)
	}
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
	for _, want := range []string{"## Project\n\nwidgets\n", "Fix issue #7 at its root: https://github.com/acme/widgets/issues/7", "> ## What happens\n> It fails.\n> mode: local-only", "Fixes #7", fleet.BriefRootIssues(), fleet.BriefStepCards()} {
		if !strings.Contains(string(brief), want) {
			t.Errorf("brief lacks %q:\n%s", want, brief)
		}
	}
	if err := fleet.ValidateBriefReadiness(brief); err != nil {
		t.Fatalf("generated root brief is not ready: %v", err)
	}
	if strings.Contains(string(brief), "ops-root: test") {
		t.Errorf("brief keeps the issue's marker:\n%s", brief)
	}
	// A root fix is pushed for CI to judge, on any harness and with no gate
	// daemon to depend on, and the issue's own "mode:" line never sets it.
	if settings := briefSettings(filepath.Join(h.Data, "root-widgets-7", "brief.md")); settings["mode"] != "direct-PR" {
		t.Errorf("the brief's mode = %q, want direct-PR", settings["mode"])
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

func TestQueueRootRecordsDefaultCredentialsWithoutChangingQuotedIssue(t *testing.T) {
	for _, hasDefaults := range []bool{false, true} {
		t.Run(fmt.Sprint(hasDefaults), func(t *testing.T) {
			issue := rootIssue(7)
			issue.Body = "Example:\ncredentials: none\nmcp: none\nmode: local-only\nready: false\n"
			service, h, _ := rootBoard(t, issue)
			if hasDefaults {
				writeFile(t, auth.ManifestPath(h.Data, "widgets"), `{"project":"widgets","services":[{"name":"github","method":"env","env":["FIXTURE_GITHUB"],"default":true},{"name":"other","method":"env","env":["FIXTURE_OTHER"]}]}`)
			}

			err := service.pickUpRoots(t.Context(), rootNow)

			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(h.Data, "root-widgets-7", "brief.md")
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(content), "> Example:\n> credentials: none\n> mcp: none\n> mode: local-only\n> ready: false\n") {
				t.Fatalf("quoted issue changed: %s", content)
			}
			need, err := auth.NeedFromBrief(string(content))
			if err != nil || need.IsUnstated || need.IsMCPUnstated || len(need.MCPServers) != 0 || len(need.Services) != map[bool]int{false: 0, true: 1}[hasDefaults] || hasDefaults && need.Services[0] != "github" {
				t.Fatalf("recorded authentication = %+v, %v", need, err)
			}
			if err := fleet.ValidateBriefReadiness(content); err != nil {
				t.Fatal(err)
			}
			if settings := briefSettings(path); settings["mode"] != "direct-PR" {
				t.Fatalf("delivery mode = %q, want direct-PR", settings["mode"])
			}
		})
	}
}

func TestQueueRootSharesTheQueuedAdmissionLock(t *testing.T) {
	_, h, _ := rootBoard(t, rootIssue(7))
	id := "root-widgets-7"
	queueLock := ".queued-" + id + ".lock"
	if _, err := lock.AcquireExclusiveNamed(h.State, queueLock); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.ReleaseExclusiveNamed(h.State, queueLock); err != nil {
			t.Error(err)
		}
	})

	err := queueRoot(h, "widgets", id, rootIssue(7))

	if !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("root publication = %v, want queued admission contention", err)
	}
	if _, err := os.Stat(filepath.Join(h.Data, id, "brief.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("contended pickup published a brief: %v", err)
	}
	if got := queuedIDs(t, h); len(got) != 0 {
		t.Fatalf("contended pickup queued %v", got)
	}
}

func TestQueueRootPreservesAuthoredDraftAndActiveMetadata(t *testing.T) {
	_, h, _ := rootBoard(t, rootIssue(7))
	id := "root-widgets-7"
	path := filepath.Join(h.Data, id, "brief.md")
	draft := "# Brief root-widgets-7\r\n## Project\r\nwidgets\r\n## Task\r\n{TASK - unfinished requirements}\r\n## Authentication\r\ncredentials: github\r\nmcp: chosen-server\r\n## Delivery\r\nmode: local-only\r\nclass: high-risk\r\nharness: codex\r\nmodel: chosen-model\r\neffort: high\r\nready: false\r\n"
	queueLock := ".queued-" + id + ".lock"
	if _, err := lock.AcquireExclusiveNamed(h.State, queueLock); err != nil {
		t.Fatal(err)
	}
	writeErr := os.MkdirAll(filepath.Dir(path), 0o700)
	if writeErr == nil {
		writeErr = os.WriteFile(path, []byte(draft), 0o600)
	}
	if err := errors.Join(writeErr, lock.ReleaseExclusiveNamed(h.State, queueLock)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, auth.ManifestPath(h.Data, "widgets"), "{invalid")
	activeMeta := state.TaskMetaPath(h.State, "active-task")
	activeContent := "project=widgets\nmode=no-mistakes\nmodel=active-model\n"
	writeFile(t, activeMeta, activeContent)
	activePolicy := filepath.Join(h.State, "tasktmp", "active-task", "pipeline.json")
	policyContent := `{"class":"high-risk","policy_sha256":"existing-policy","review_cycles":3}`
	writeFile(t, activePolicy, policyContent)

	firstErr := queueRoot(h, "widgets", id, rootIssue(7))
	secondErr := queueRoot(h, "widgets", id, rootIssue(7))

	if err := errors.Join(firstErr, secondErr); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != draft {
		t.Fatalf("authored draft changed: %q, %v", content, err)
	}
	if content, err := os.ReadFile(activeMeta); err != nil || string(content) != activeContent {
		t.Fatalf("active metadata changed: %q, %v", content, err)
	}
	if content, err := os.ReadFile(activePolicy); err != nil || string(content) != policyContent {
		t.Fatalf("active policy changed: %q, %v", content, err)
	}
	if got := strings.Join(queuedIDs(t, h), ","); got != "root-widgets-7 ops-root" {
		t.Fatalf("queued = %q, want one root task", got)
	}
	if err := fleet.ValidateBriefReadiness(content); !errors.Is(err, fleet.ErrBriefNotReady) {
		t.Fatalf("authored draft readiness = %v, want draft refusal", err)
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

func TestPickUpRootsLeavesALiveTasksFollowUpToItsOneOwner(t *testing.T) {
	// Arrange: a live task given a follow-up filed its issue after it was
	// spawned, so its brief does not name it, as on 2026-10-26.
	service, h, _ := rootBoard(t, rootIssue(7))
	fileRootAsLiveTask(t, h, "widgets-follow-up", rootIssue(7).URL)
	var closing []string
	service.Options.RootIssues.ClosingPullRequests = func(_ context.Context, repository string, number int) ([]string, error) {
		if repository != rootRepository || number != 7 {
			t.Errorf("read the pull requests that close %s#%d, want acme/widgets#7", repository, number)
		}
		return closing, nil
	}

	// Act: a pass while the task works on it, one after it opened its pull
	// request and ended, and one after the pull request closed unmerged.
	whileRunning := service.pickUpRoots(t.Context(), rootNow)
	runningQueued := queuedIDs(t, h)
	closing = []string{"https://github.com/acme/widgets/pull/8"}
	if err := os.Remove(state.TaskMetaPath(h.State, "widgets-follow-up")); err != nil {
		t.Fatal(err)
	}
	whilePullOpen := service.pickUpRoots(t.Context(), rootNow.Add(rootPickupEvery))
	pullQueued := queuedIDs(t, h)
	closing = nil
	afterPull := service.pickUpRoots(t.Context(), rootNow.Add(2*rootPickupEvery))

	// Assert
	if err := errors.Join(whileRunning, whilePullOpen, afterPull); err != nil {
		t.Fatal(err)
	}
	if len(runningQueued) != 0 || len(pullQueued) != 0 {
		t.Fatalf("queued while the task ran = %v, while its pull request was open = %v, want neither a second owner", runningQueued, pullQueued)
	}
	if got := strings.Join(queuedIDs(t, h), ","); got != "root-widgets-7 ops-root" {
		t.Fatalf("queued once it has no owner = %q, want root-widgets-7", got)
	}
}

func TestPickUpRootsLeavesAnIssueWhosePullRequestsCannotBeRead(t *testing.T) {
	// Arrange
	service, h, _ := rootBoard(t, rootIssue(7))
	service.Options.RootIssues.ClosingPullRequests = func(context.Context, string, int) ([]string, error) {
		return nil, errors.New("gh: Bad credentials (HTTP 401)")
	}

	// Act
	err := service.pickUpRoots(t.Context(), rootNow)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "root issues of widgets: gh: Bad credentials") {
		t.Fatalf("err = %v, want the unread pull requests named", err)
	}
	if got := queuedIDs(t, h); len(got) != 0 {
		t.Fatalf("queued = %v, want nothing taken whose owner is unknown", got)
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
