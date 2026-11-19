package cutover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harnessmap"
)

// caseFile writes the file name in capitals in folder and returns its path
// and whether the file system opens it by name as written.
func caseFile(t *testing.T, folder, name string) (upper string, isOnePath bool) {
	t.Helper()
	upper = filepath.Join(folder, strings.ToUpper(name))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(upper, []byte("run `"+oldName+"`"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(folder, name))
	return upper, err == nil
}

// isMoved reports whether a step of the plan moves path.
func isMoved(changes []Change, path string) bool {
	for _, change := range changes {
		for _, step := range change.Steps {
			if step.Kind == "move" && step.Key == path {
				return true
			}
		}
	}
	return false
}

// A name the old build gave, spelled in capitals, is its file exactly where
// the file system of the folder that holds it opens the one spelling by the
// other.
func TestRenamedNamesTheOldBuildsFilesByTheirFoldersCase(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	_, isOnePath := caseFile(t, dir, "probe")
	isFolded := fsx.FoldsCase(dir)
	r := newRules(nil, []string{newName + "-afk"})
	cases := []struct {
		name   string
		isFile bool
	}{
		{name: strings.ToUpper(oldName) + ".EXE.1794.update-old"},
		{name: "." + strings.ToUpper(oldName) + "-HOME"},
		{name: strings.ToUpper(oldName) + "-HANDOFF-2026-11-05.md"},
		{name: strings.ToUpper(oldName) + "-AFK"},
		{name: "settings.json." + strings.ToUpper(oldName) + "-NATIVE.BACKUP", isFile: true},
		{name: "settings.json." + strings.ToUpper(oldName) + "-INSTALL.BAK", isFile: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			renamed := r.renamed(tc.name, isFolded)
			if tc.isFile {
				renamed = r.renamedFile(tc.name, isFolded)
			}

			// Assert
			if isRenamed := renamed != tc.name; isRenamed != isOnePath {
				t.Fatalf("renamed %s as %s: %t, want %t", tc.name, renamed, isRenamed, isOnePath)
			}
		})
	}
}

// The home's marker and the CEO's terminal's host file, spelled in capitals,
// are moved exactly where the file system opens them by the old build's
// spelling.
func TestStateFilesMovesTheOldBuildsFilesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root := t.TempDir()
	marker, isOnePath := caseFile(t, root, "."+oldName+"-home")
	hostLog, _ := caseFile(t, filepath.Join(root, "state", "hosts"), oldName+".log")
	p := &planner{machine: Machine{Root: root}, rules: newRules(nil, nil), claimed: map[string]bool{}}

	// Act
	err := p.stateFiles()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{marker, hostLog} {
		if got := isMoved(p.changes, path); got != isOnePath {
			t.Errorf("%s moved: %t, want %t; plan %+v", path, got, isOnePath, p.changes)
		}
	}
}

// A shared skill folder named for this build's skill in capitals is that
// skill's exactly where the file system opens the one spelling by the other,
// so a folder Code Goblins did not install is listed as kept there, and is
// another folder elsewhere.
func TestSkillsNamesTheBuildsSkillsByTheFileSystemsCase(t *testing.T) {
	// Arrange
	shared := t.TempDir()
	folder := filepath.Join(shared, strings.ToUpper(oldName)+"-AFK")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(shared, oldName+"-afk"))
	isOnePath := err == nil
	skills := fstest.MapFS{newName + "-afk/SKILL.md": {Data: []byte("skill")}}
	machine := Machine{Skills: skills, Harnesses: harnessmap.Map{SharedSkills: shared}}
	p := &planner{machine: machine, rules: newRules(nil, []string{newName + "-afk"}), claimed: map[string]bool{}}

	// Act
	err = p.skills()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if isListed := len(p.changes) == 1 && p.changes[0].Kind == KindListed; isListed != isOnePath || len(p.changes) > 1 {
		t.Fatalf("plan %+v, want %s listed as kept: %t", p.changes, folder, isOnePath)
	}
}

// A copy an update set aside whose name begins with the old program's in
// capitals is the old build's exactly where the file system opens it by the
// old spelling.
func TestRemoveBridgesNamesTheOldProgramsByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	_, isOnePath := caseFile(t, bin, oldName+".exe")
	copied := filepath.Join(bin, strings.ToUpper(oldName)+".EXE.1794.update-old")
	if err := os.WriteFile(copied, []byte("the old build"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	err := RemoveBridges(root, nil)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(copied); os.IsNotExist(statErr) != isOnePath {
		t.Fatalf("%s removed: %t, want %t", copied, os.IsNotExist(statErr), isOnePath)
	}
}

// A memory folder named archive in capitals is recorded history, whose files
// keep their words, exactly where the file system opens it as archive.
func TestTaskFilesKeepsHistoryFoldersByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root := t.TempDir()
	memory := filepath.Join(root, "data", "memory")
	archive := filepath.Join(memory, "ARCHIVE")
	note, _ := caseFile(t, archive, "note.md")
	_, err := os.Stat(filepath.Join(memory, "archive"))
	isOnePath := err == nil
	p := &planner{machine: Machine{Root: root}, rules: newRules(nil, nil), claimed: map[string]bool{}}

	// Act
	err = p.taskFiles()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	isRewritten := false
	for _, change := range p.changes {
		for _, step := range change.Steps {
			isRewritten = isRewritten || step.Key == note
		}
	}
	if isRewritten == isOnePath {
		t.Fatalf("%s rewritten: %t, want %t; plan %+v", note, isRewritten, !isOnePath, p.changes)
	}
}
