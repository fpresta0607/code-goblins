package memory

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// isOneName reports whether the file system that holds dir opens the entry
// named name by its name in capitals, making the entry when it is missing.
func isOneName(t *testing.T, dir, name string) bool {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, err := os.Stat(filepath.Join(dir, strings.ToUpper(name)))
	return err == nil
}

// Recall scopes an item to a project or a task named in another case
// exactly where the file system opens the one name by the other: a project
// by the home's data folder and a task by the state folder.
func TestRecallScopesProjectsAndTasksByTheFileSystemsCase(t *testing.T) {
	// Arrange
	index := buildFixtureIndex(t)
	isOneProject := isOneName(t, index.dataDir, "northwind")
	isOneTask := isOneName(t, index.stateDir, "harbor-live.meta")
	cases := []struct {
		name     string
		query    RecallQuery
		recalled string
		isWanted bool
	}{
		{name: "a project in another case", query: RecallQuery{Question: "cache port", Project: "NORTHWIND"}, recalled: "northwind-cache-port", isWanted: isOneProject},
		{name: "a project in its own case", query: RecallQuery{Question: "cache port", Project: "northwind"}, recalled: "northwind-cache-port", isWanted: true},
		{name: "a task in another case", query: RecallQuery{Question: "harbor gallery PR", Task: "HARBOR-LIVE"}, recalled: "done-harbor-live", isWanted: isOneTask},
		{name: "a task in its own case", query: RecallQuery{Question: "harbor gallery PR", Task: "harbor-live"}, recalled: "done-harbor-live", isWanted: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.query.Limit = 10

			// Act
			result, err := index.Recall(tc.query)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if isRecalled := slices.Contains(recalledIDs(result), tc.recalled); isRecalled != tc.isWanted {
				t.Fatalf("Recall(%+v) = %v, want %s recalled: %t", tc.query, recalledIDs(result), tc.recalled, tc.isWanted)
			}
		})
	}
}

// A pack leads with the lessons accepted for its project and task, and
// lists its project's done reports, each named in another case exactly
// where the file system opens the one name by the other.
func TestBuildPackNamesTheProjectAndTaskByTheFileSystemsCase(t *testing.T) {
	// Arrange
	dataDir, stateDir := writeIndexFixture(t)
	isOneProject := isOneName(t, dataDir, "harbor")
	isOneTask := isOneName(t, stateDir, "harbor-live.meta")
	writeFile(t, filepath.Join(dataDir, "memory", "zebra-paint.md"), "---\nname: zebra-paint\ndescription: zebra crossings need paint\nmetadata:\n  type: feedback\n  lesson: lesson-1\n  projects: [harbor]\n  modified: 2026-11-09T13:00:00Z\n---\n\nZebra crossings need fresh paint.\n")
	writeFile(t, filepath.Join(dataDir, "memory", "quay-ropes.md"), "---\nname: quay-ropes\ndescription: quay ropes need coiling\nmetadata:\n  type: feedback\n  lesson: lesson-2\n  projects: [HARBOR]\n  task: HARBOR-LIVE\n  modified: 2026-11-09T13:00:00Z\n---\n\nQuay ropes need coiling.\n")
	index, err := BuildIndex(dataDir, stateDir, indexDate)
	if err != nil {
		t.Fatal(err)
	}
	input := harborPackInput()
	input.Project, input.Task = "HARBOR", "harbor-live"

	// Act
	pack, err := index.BuildPack(input)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		text     string
		isWanted bool
	}{
		{"zebra-paint (", isOneProject},
		{"quay-ropes (", isOneTask},
		{"harbor-gallery: Pearl", isOneProject},
	} {
		if isCarried := strings.Contains(pack.Text, want.text); isCarried != want.isWanted {
			t.Errorf("the pack carries %q: %t, want %t:\n%s", want.text, isCarried, want.isWanted, pack.Text)
		}
	}
}

// memory.md is the index file MEMORY.md, which is no fact, exactly where the
// file system opens the one name by the other.
func TestBuildIndexKnowsTheIndexFileByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root := t.TempDir()
	dataDir, stateDir := filepath.Join(root, "data"), filepath.Join(root, "state")
	memoryDir := filepath.Join(dataDir, "memory")
	writeFile(t, filepath.Join(memoryDir, "memory.md"), "# a lighthouse note\n")
	_, err := os.Stat(filepath.Join(memoryDir, "MEMORY.md"))
	isOnePath := err == nil

	// Act
	index, err := BuildIndex(dataDir, stateDir, indexDate)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if isIndexed := slices.ContainsFunc(index.Items, func(item Item) bool { return item.ID == "memory" }); isIndexed == isOnePath {
		t.Fatalf("memory.md indexed as a fact: %t, want %t", isIndexed, !isOnePath)
	}
}
