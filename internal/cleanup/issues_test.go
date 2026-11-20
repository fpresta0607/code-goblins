package cleanup

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	firstIssue  = "acme/widgets#12"
	secondIssue = "acme/gadgets#7"
)

// opensIssues makes the fixture's task name the issues given, each open.
func opensIssues(fixture *cleanupFixture, issues ...string) {
	fixture.service.OpenIssues = func(context.Context, state.TaskMeta) ([]string, error) {
		return issues, nil
	}
}

// assertNothingRetired proves a refused cleanup left the task record and its
// worktree in place and wrote nothing to the task's status log.
func assertNothingRetired(t *testing.T, fixture *cleanupFixture) {
	t.Helper()
	fixture.assertMetadataPreserved(t)
	if _, err := os.Stat(fixture.worktree); err != nil {
		t.Errorf("worktree after a refused cleanup: %v, want it kept", err)
	}
	if status, err := state.TailStatus(fixture.stateDir, "g1", 5); err != nil || len(status) != 0 {
		t.Errorf("status log after a refused cleanup = %v, %v; want it untouched", status, err)
	}
}

// A task's work is not settled while an issue it names is open: the issue
// still asks for work nobody is doing. The cleanup refuses it, naming the
// issue, with the task record and worktree left as they were.
func TestCleanupRefusesATaskWhoseIssueIsStillOpen(t *testing.T) {
	// Arrange
	fixture := newCleanupFixture(t)
	opensIssues(fixture, firstIssue)

	// Act
	_, err := fixture.service.Cleanup(t.Context(), "g1")

	// Assert
	if err == nil || !strings.Contains(err.Error(), firstIssue) || !strings.Contains(err.Error(), "--keep-issue-open") {
		t.Fatalf("Cleanup = %v, want a refusal naming %s and how to keep it open", err, firstIssue)
	}
	assertNothingRetired(t, fixture)
}

func TestCleanupRefusalNamesEveryOpenIssueNotKept(t *testing.T) {
	// Arrange
	fixture := newCleanupFixture(t)
	opensIssues(fixture, firstIssue, secondIssue)
	fixture.service.KeepOpen = []string{secondIssue}
	fixture.service.KeepReason = "another task carries it on"

	// Act
	_, err := fixture.service.Cleanup(t.Context(), "g1")

	// Assert
	if err == nil || !strings.Contains(err.Error(), firstIssue) || strings.Contains(err.Error(), secondIssue) {
		t.Fatalf("Cleanup = %v, want a refusal naming only %s", err, firstIssue)
	}
	assertNothingRetired(t, fixture)
}

func TestCleanupRetiresATaskWhoseIssuesAreAllClosed(t *testing.T) {
	// Arrange
	fixture := newCleanupFixture(t)
	opensIssues(fixture)

	// Act
	result, err := fixture.service.Cleanup(t.Context(), "g1")

	// Assert
	if err != nil {
		t.Fatalf("Cleanup = %v, want the task retired", err)
	}
	if strings.Contains(result.Output, "kept the issue") {
		t.Errorf("output = %q, want no issue kept open", result.Output)
	}
	if _, err := state.ReadTaskMeta(fixture.stateDir, "g1"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("task record after the cleanup: %v, want it retired", err)
	}
}

func TestCleanupRetiresATaskWithAnIssueKeptOpenAndRecordsWhy(t *testing.T) {
	// Arrange
	fixture := newCleanupFixture(t)
	opensIssues(fixture, firstIssue, secondIssue)
	fixture.service.KeepOpen = []string{"ACME/Widgets#12", secondIssue}
	fixture.service.KeepReason = "the follow-up\nis a task of its own"

	// Act
	result, err := fixture.service.Cleanup(t.Context(), "g1")

	// Assert
	if err != nil {
		t.Fatalf("Cleanup = %v, want the task retired with its issues open", err)
	}
	wantReason := "the follow-up is a task of its own"
	status, err := state.TailStatus(fixture.stateDir, "g1", 10)
	if err != nil || len(status) != 3 {
		t.Fatalf("status = %v, %v; want one line per kept issue, then the retirement", status, err)
	}
	for index, issue := range []string{firstIssue, secondIssue} {
		if _, event := state.SplitStatus(status[index]); event != "issue-kept-open: "+issue+": "+wantReason {
			t.Errorf("status line %d = %q, want the open issue %s recorded with its reason", index, event, issue)
		}
		if want := "\nkept the issue " + issue + " open: " + wantReason; !strings.Contains(result.Output, want) {
			t.Errorf("output = %q, want it to say %q", result.Output, want)
		}
	}
	if _, event := state.SplitStatus(status[2]); !strings.Contains(event, "returned worktree") {
		t.Errorf("last status line = %q, want the retirement after the kept issues", event)
	}
	if _, err := state.ReadTaskMeta(fixture.stateDir, "g1"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("task record after the cleanup: %v, want it retired", err)
	}
}

func TestCleanupRefusesAnIssueKeepItCannotHonourAndChangesNothing(t *testing.T) {
	tests := []struct {
		name       string
		issues     func(context.Context, state.TaskMeta) ([]string, error)
		keepOpen   []string
		keepReason string
		want       []string
	}{
		{
			name:       "a keep naming an issue that is not open",
			issues:     func(context.Context, state.TaskMeta) ([]string, error) { return []string{firstIssue}, nil },
			keepOpen:   []string{firstIssue, secondIssue},
			keepReason: "carried on elsewhere",
			want:       []string{secondIssue, "not an issue g1 names that is open", firstIssue},
		},
		{
			name:       "a keep when no issue is open",
			issues:     func(context.Context, state.TaskMeta) ([]string, error) { return nil, nil },
			keepOpen:   []string{firstIssue},
			keepReason: "carried on elsewhere",
			want:       []string{firstIssue, "not an issue g1 names that is open", "none"},
		},
		{
			name:     "a keep with no reason",
			issues:   func(context.Context, state.TaskMeta) ([]string, error) { return []string{firstIssue}, nil },
			keepOpen: []string{firstIssue},
			want:     []string{"needs a reason", firstIssue, "--reason"},
		},
		{
			name:       "a keep with a blank reason",
			issues:     func(context.Context, state.TaskMeta) ([]string, error) { return []string{firstIssue}, nil },
			keepOpen:   []string{firstIssue},
			keepReason: "  \t ",
			want:       []string{"needs a reason", firstIssue},
		},
		{
			name:   "issues that cannot be read",
			issues: func(context.Context, state.TaskMeta) ([]string, error) { return nil, errors.New("gh unavailable") },
			want:   []string{"the issues g1 names cannot be read", "nothing was removed", "gh unavailable"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			fixture := newCleanupFixture(t)
			fixture.service.OpenIssues = tc.issues
			fixture.service.KeepOpen = tc.keepOpen
			fixture.service.KeepReason = tc.keepReason

			// Act
			_, err := fixture.service.Cleanup(t.Context(), "g1")

			// Assert
			for _, want := range tc.want {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("Cleanup = %v, want a refusal saying %q", err, want)
				}
			}
			assertNothingRetired(t, fixture)
		})
	}
}

// Every retirement asks, --force-archive and an empty folder's included: a
// task is as unsettled with its worktree pinned or gone as with it intact.
func TestEveryRetirementRefusesAnOpenIssueAndKeepsOneOpenWithItsReason(t *testing.T) {
	for _, tc := range []struct {
		name           string
		isForceArchive bool
		isEmptyFolder  bool
		wantRetired    string
	}{
		{name: "a force archive", isForceArchive: true, wantRetired: "force-archived g1"},
		{name: "a cleanup of an empty folder", isEmptyFolder: true, wantRetired: "removed the empty folder"},
	} {
		arrange := func(t *testing.T) *cleanupFixture {
			fixture := newCleanupFixture(t)
			fixture.service.ForceArchive = tc.isForceArchive
			if tc.isEmptyFolder {
				fixture.git.top = fixture.project
			}
			opensIssues(fixture, firstIssue)
			return fixture
		}
		t.Run(tc.name+" refuses an open issue", func(t *testing.T) {
			// Arrange
			fixture := arrange(t)

			// Act
			_, err := fixture.service.Cleanup(t.Context(), "g1")

			// Assert
			if err == nil || !strings.Contains(err.Error(), firstIssue) {
				t.Fatalf("Cleanup = %v, want a refusal naming %s", err, firstIssue)
			}
			assertNothingRetired(t, fixture)
			if len(fixture.runner.tabClosed) != 0 {
				t.Errorf("tab closes = %v, want none from a refused cleanup", fixture.runner.tabClosed)
			}
		})
		t.Run(tc.name+" keeps an open issue open", func(t *testing.T) {
			// Arrange
			fixture := arrange(t)
			fixture.service.KeepOpen = []string{firstIssue}
			fixture.service.KeepReason = "carried on elsewhere"

			// Act
			result, err := fixture.service.Cleanup(t.Context(), "g1")

			// Assert
			if err != nil || !strings.Contains(result.Output, tc.wantRetired) {
				t.Fatalf("Cleanup = %q, %v; want the task retired", result.Output, err)
			}
			if want := "\nkept the issue " + firstIssue + " open: carried on elsewhere"; !strings.Contains(result.Output, want) {
				t.Errorf("output = %q, want it to say %q", result.Output, want)
			}
			status, err := state.TailStatus(fixture.stateDir, "g1", 10)
			if err != nil || len(status) != 2 {
				t.Fatalf("status = %v, %v; want the kept issue, then the retirement", status, err)
			}
			if _, event := state.SplitStatus(status[0]); event != "issue-kept-open: "+firstIssue+": carried on elsewhere" {
				t.Errorf("first status line = %q, want the kept issue and its reason", event)
			}
			if _, err := state.ReadTaskMeta(fixture.stateDir, "g1"); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("task record after the cleanup: %v, want it retired", err)
			}
		})
	}
}
