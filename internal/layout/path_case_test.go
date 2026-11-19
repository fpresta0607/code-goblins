package layout

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// opensByUpper reports whether the file system opens path by its name in
// capitals.
func opensByUpper(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(filepath.Dir(path), strings.ToUpper(filepath.Base(path))))
	return err == nil
}

// plannedIDs plans h's filing and returns the id of each folder it moves.
func plannedIDs(t *testing.T, h home.Home) []string {
	t.Helper()
	moves, err := Plan(h, "", filingNow)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, move := range moves {
		ids = append(ids, move.ID)
	}
	return ids
}

// A task's state archive entry, or an open row or live text naming its data
// folder, in another case are the task's exactly where the file system opens
// the one name by the other.
func TestPlanNamesATasksFolderByTheFileSystemsCase(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		name    string
		backlog string
		arrange func(t *testing.T, h home.Home)
		// isFiledWhenOnePath is whether g1 is filed where the file system
		// opens the one spelling by the other; elsewhere it is the opposite.
		isFiledWhenOnePath bool
	}{
		{"an archive entry", emptyBacklog, func(t *testing.T, h home.Home) {
			finishedAt(t, filepath.Join(h.State, "archive", "G1.20260925T120000Z"), "", day)
		}, true},
		{"a status archive entry", emptyBacklog, func(t *testing.T, h home.Home) {
			finishedAt(t, filepath.Join(h.State, "archive", "G1.status.20260925T120000Z"), "", day)
		}, true},
		{"an open row's id", "# Backlog\n\n## Queued\n- **G1** - Resume it\n\n## Parked\n\n## Done\n", func(t *testing.T, h home.Home) {
			write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n")
		}, false},
		{"an open row's path", "# Backlog\n\n## Queued\n- [ ] q1 - Start from data/G1/report.md\n\n## Parked\n\n## Done\n", func(t *testing.T, h home.Home) {
			write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n")
		}, false},
		{"the directives", emptyBacklog, func(t *testing.T, h home.Home) {
			write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n")
			write(t, filepath.Join(h.Data, "overlord.md"), "a ruling that cites data/G1/report.md\n")
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			h := filingHome(t, tc.backlog)
			taskFolder(t, h, "g1", 2*day)
			isOnePath := opensByUpper(t, filepath.Join(h.Data, "g1"))
			tc.arrange(t, h)

			// Act
			ids := plannedIDs(t, h)

			// Assert
			if isFiled, want := slices.Contains(ids, "g1"), tc.isFiledWhenOnePath == isOnePath; isFiled != want {
				t.Fatalf("planned %v, want g1 filed: %t, as the file system opens g1 by G1: %t", ids, want, isOnePath)
			}
		})
	}
}

// A data folder named for a layout folder in another case is that layout
// folder exactly where the file system opens the one name by the other, and
// a task's folder elsewhere.
func TestPlanNamesTheLayoutsFoldersByTheFileSystemsCase(t *testing.T) {
	// Arrange
	h := filingHome(t, emptyBacklog)
	isOnePath := opensByUpper(t, filepath.Join(h.Data, "archive"))
	taskFolder(t, h, "ARCHIVE", StaleBriefAge)

	// Act
	ids := plannedIDs(t, h)

	// Assert
	if isFiled := slices.Contains(ids, "ARCHIVE"); isFiled == isOnePath {
		t.Fatalf("planned %v, want ARCHIVE parked as a task's: %t", ids, !isOnePath)
	}
}

// A data folder holding only the lane policy spelled in another case is a new
// home exactly where the file system opens the policy by that spelling, and
// a legacy one elsewhere.
func TestEnsureNamesTheLanePolicyByTheFileSystemsCase(t *testing.T) {
	// Arrange
	data := filepath.Join(t.TempDir(), "data")
	write(t, filepath.Join(data, "ROUTING.JSON"), "{}\n")
	_, err := os.Stat(filepath.Join(data, "routing.json"))
	isOnePath := err == nil

	// Act
	_, isLegacy, err := Ensure(data)

	// Assert
	if err != nil || isLegacy == isOnePath {
		t.Fatalf("Ensure = legacy %t, %v; want legacy: %t", isLegacy, err, !isOnePath)
	}
}

// The imported memory folder's index spelled in another case is the index
// exactly where the file system opens the home's index by that spelling.
func TestPlanMigrationNamesTheMemoryIndexByTheFileSystemsCase(t *testing.T) {
	// Arrange
	h := filingHome(t, emptyBacklog)
	isOnePath := opensByUpper(t, filepath.Join(h.Data, "memory", "memory.md"))
	source := filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(source, "memory.md"), "# Memory index\n\n- [A fact](a-fact.md) - one\n")
	write(t, filepath.Join(source, "a-fact.md"), "fact one\n")

	// Act
	m, err := PlanMigration(h, source, filingNow)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	isStamped := slices.ContainsFunc(m.Writes, func(w Write) bool {
		return w.Path == "memory/memory.md" && strings.Contains(string(w.Content), importStamp(m))
	})
	if isStamped != isOnePath {
		t.Fatalf("writes %+v, want memory/memory.md imported as the stamped index: %t", m.Writes, isOnePath)
	}
}

// A fact the home's index links in another case is linked exactly where the
// file system of the memory folder opens it by that spelling, so the import
// adds no second line for it there.
func TestPlanMigrationLinksFactsByTheFileSystemsCase(t *testing.T) {
	// Arrange
	h := filingHome(t, emptyBacklog)
	isOnePath := opensByUpper(t, filepath.Join(h.Data, "memory"))
	write(t, filepath.Join(h.Data, "memory", "MEMORY.md"), "# Memory index\n\n- [A fact](A-FACT.md) - one\n")
	source := filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(source, "MEMORY.md"), "# Memory index\n\n- [A fact](a-fact.md) - one\n")
	write(t, filepath.Join(source, "a-fact.md"), "fact one\n")

	// Act
	m, err := PlanMigration(h, source, filingNow)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	isAdded := slices.ContainsFunc(m.Writes, func(w Write) bool {
		return w.Path == MemoryIndex && strings.Contains(string(w.Content), "(a-fact.md)")
	})
	if isAdded == isOnePath {
		t.Fatalf("writes %+v, want a line for a-fact.md added: %t", m.Writes, !isOnePath)
	}
}

// A file the plan read under a moved folder spelled in another case moves
// with it, and is what the folder held, exactly where the file system opens
// the folder by that spelling.
func TestMigrationMovesFilesUnderAFolderByTheFileSystemsCase(t *testing.T) {
	// Arrange
	h := filingHome(t, emptyBacklog)
	write(t, filepath.Join(h.Data, "g1", "brief.md"), "the brief\n")
	isOnePath := opensByUpper(t, filepath.Join(h.Data, "g1"))
	move := Move{ID: "g1", From: filepath.Join(h.Data, "g1"), To: filepath.Join(h.Data, "archive", "finished", "g1")}
	m := Migration{Home: h, Before: Manifest{"G1/brief.md": hashBytes([]byte("the brief\n"))}, Moves: []Move{move}, Moved: map[string]string{}}

	// Act
	m.predict()
	err := m.unchangedSincePlan()

	// Assert
	if isMoved := m.Moved["G1/brief.md"] == "archive/finished/g1/brief.md"; isMoved != isOnePath {
		t.Errorf("moved %v, want G1/brief.md moved with g1: %t", m.Moved, isOnePath)
	}
	if isUnchanged := err == nil; isUnchanged != isOnePath {
		t.Errorf("unchangedSincePlan = %v, want g1 unchanged since the plan: %t", err, isOnePath)
	}
}
