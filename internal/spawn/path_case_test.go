package spawn

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// Another task works in this goblin's repository when its checkout's folder
// has the repository's name, in another case exactly where the file system
// opens the one folder by the other.
func TestLiveTasksNameTheRepositorysTasksByTheFileSystemsCase(t *testing.T) {
	// Arrange
	stateDir, parent := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, "PROJECT")
	_, err := os.Stat(upper)
	isOnePath := err == nil
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "g2", Project: upper, Window: "native", Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	service := Service{StateDir: stateDir}

	// Act
	live, err := service.liveTasks(context.Background(), state.TaskMeta{ID: "g1"}, "project")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if isNamed := len(live) == 1 && live[0].ID == "g2"; isNamed != isOnePath || len(live) > 1 {
		t.Fatalf("liveTasks = %+v, want g2 in %s named: %t, as the file system opens project by PROJECT: %t", live, upper, isOnePath, isOnePath)
	}
}

// A cleaned-up task's archived state is its id's when the archive names it,
// in another case exactly where the file system opens the archived entry by
// that spelling.
func TestArchivedFindsATasksStateByTheFileSystemsCase(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	archive := filepath.Join(stateDir, state.ArchiveDirName)
	if err := os.MkdirAll(filepath.Join(archive, "Foo.20261118"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(archive, "foo.20261118"))
	isOnePath := err == nil
	cases := []struct {
		name         string
		id           string
		wantArchived bool
	}{
		{name: "its own spelling", id: "Foo", wantArchived: true},
		{name: "another case", id: "foo", wantArchived: isOnePath},
		{name: "another id", id: "Fo", wantArchived: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isArchived, err := AuthRefresher{StateDir: stateDir}.archived(tc.id)

			// Assert
			if err != nil || isArchived != tc.wantArchived {
				t.Fatalf("archived(%s) = %t, %v; want %t", tc.id, isArchived, err, tc.wantArchived)
			}
		})
	}
}
