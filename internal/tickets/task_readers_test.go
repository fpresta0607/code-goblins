package tickets

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestTaskPullRequestsNamesEachPullRequestOnRecordOnce(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	const (
		checked  = "https://github.com/acme/widgets/pull/8"
		reported = "https://github.com/acme/widgets/pull/9"
	)
	if err := state.WriteMeta(state.TaskMetaPath(stateDir, "nw-sync"), map[string]string{"pr": checked}); err != nil {
		t.Fatal(err)
	}
	status := "2026-11-24T10:00:00Z done: PR " + checked + "\n2026-11-24T11:00:00Z done: PR " + reported + "\n2026-11-24T12:00:00Z done: PR not an address\n"
	if err := os.WriteFile(state.StatusPath(stateDir, "nw-sync"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	pulls, err := TaskPullRequests(stateDir, "nw-sync")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{checked, reported}; !slices.Equal(pulls, want) {
		t.Fatalf("pulls = %v, want %v", pulls, want)
	}
}

func TestTaskPullRequestsOfATaskWithNoRecordsAreNone(t *testing.T) {
	// Act
	pulls, err := TaskPullRequests(t.TempDir(), "nw-sync")

	// Assert
	if err != nil || len(pulls) != 0 {
		t.Fatalf("pulls = %v, err = %v, want none", pulls, err)
	}
}

func TestTaskBriefReadsThePathThenTheHomesCopyThenNothing(t *testing.T) {
	// Arrange
	data := t.TempDir()
	h := home.Home{Data: data}
	if err := os.MkdirAll(filepath.Join(data, "nw-sync"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "nw-sync", "brief.md"), []byte("the home's copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(named, []byte("the named brief"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, testCase := range []struct {
		name, id, path, want string
	}{
		{name: "a brief at the path", id: "nw-sync", path: named, want: "the named brief"},
		{name: "a path with no file", id: "nw-sync", path: filepath.Join(data, "gone.md"), want: "the home's copy"},
		{name: "no path", id: "nw-sync", want: "the home's copy"},
		{name: "neither", id: "other", want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Act
			text, err := TaskBrief(h, testCase.id, testCase.path)

			// Assert
			if err != nil || text != testCase.want {
				t.Fatalf("text = %q, err = %v, want %q", text, err, testCase.want)
			}
		})
	}
}
