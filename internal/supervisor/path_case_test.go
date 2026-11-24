package supervisor

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// caseSpellings makes a folder named name in a new temporary folder and
// returns it, its name in capitals beside it, and whether the file system
// opens the one by the other.
func caseSpellings(t *testing.T, name string) (lower, upper string, isOnePath bool) {
	t.Helper()
	parent := fsx.LongPath(t.TempDir())
	lower, upper = filepath.Join(parent, name), filepath.Join(parent, strings.ToUpper(name))
	if err := os.Mkdir(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(upper)
	return lower, upper, err == nil
}

// Pi's own folder spelled in another case is its default profile exactly
// where the file system opens the one by the other.
func TestKeySpendProfileNameNamesTheDefaultFolderByTheFileSystemsCase(t *testing.T) {
	// Arrange
	agent, upper, isOnePath := caseSpellings(t, "agent")

	// Act
	profile := keySpendProfileName(upper, agent)

	// Assert
	if isDefault := profile == "default"; isDefault != isOnePath {
		t.Fatalf("keySpendProfileName(%s, %s) = %q, want the default: %t", upper, agent, profile, isOnePath)
	}
}

// A page's file spelled in another case is the same page exactly where the
// file system opens the one by the other.
func TestPageKeyNamesAPageByTheFileSystemsCase(t *testing.T) {
	// Arrange
	folder, upper, isOnePath := caseSpellings(t, "pages")

	// Act
	isSamePage := pageKey(filepath.Join(folder, "a.html")) == pageKey(filepath.Join(upper, "a.html"))

	// Assert
	if isSamePage != isOnePath {
		t.Fatalf("pageKey names %s and %s as one page: %t, want %t", folder, upper, isSamePage, isOnePath)
	}
}

// A window's image in the home's bin spelled in another case is a renamed
// copy beside the program only where the file system opens the bin by that
// spelling; elsewhere it is no copy beside it.
func TestLeftOnAnOldProgramComparesFoldersByTheFileSystemsCase(t *testing.T) {
	// Arrange
	bin, upper, isOnePath := caseSpellings(t, "bin")
	program := filepath.Join(bin, windowName)
	if err := os.WriteFile(program, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		image    string
		wantLeft bool
	}{
		{name: "a renamed copy in the bin in another case", image: filepath.Join(upper, "goblins-window.old.exe"), wantLeft: isOnePath},
		{name: "the program itself in another case", image: filepath.Join(bin, strings.ToUpper(windowName)), wantLeft: !isOnePath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isLeft := leftOnAnOldProgram(tc.image, program)

			// Assert
			if isLeft != tc.wantLeft {
				t.Fatalf("leftOnAnOldProgram(%s, %s) = %t, want %t", tc.image, program, isLeft, tc.wantLeft)
			}
		})
	}
}

// A window run from the home's bin spelled in another case is the home's own
// exactly where the file system opens the bin by that spelling.
func TestHomesWindowComparesTheBinByTheFileSystemsCase(t *testing.T) {
	// Arrange
	bin, upper, isOnePath := caseSpellings(t, "bin")
	image := filepath.Join(upper, windowName)

	// Act
	isHomes := homesWindow(proc.Entry{ExeBase: windowName}, image, bin)

	// Assert
	if isHomes != isOnePath {
		t.Fatalf("homesWindow(%s, %s) = %t, want %t", image, bin, isHomes, isOnePath)
	}
}

// VS Code's shim in a folder named bin in another case is its install's
// exactly where the file system opens bin by that spelling.
func TestFindVSCodeNamesTheShimsBinByTheFileSystemsCase(t *testing.T) {
	// Arrange
	bin, upper, isOnePath := caseSpellings(t, "bin")
	executable := filepath.Join(filepath.Dir(bin), "Code.exe")
	if err := os.WriteFile(executable, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(upper, "code")

	// Act
	found, err := findVSCode(func(string) (string, error) { return shim, nil })

	// Assert
	if isFound := err == nil && found == filepath.Join(filepath.Dir(upper), "Code.exe"); isFound != isOnePath {
		t.Fatalf("findVSCode for %s = %s, %v; want %s found: %t", shim, found, err, executable, isOnePath)
	}
}

// A path below a root spelled in another case is below it exactly where the
// file system opens the root by that spelling.
func TestPathBelowComparesNamesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root, upper, isOnePath := caseSpellings(t, "root")
	path := filepath.Join(upper, "a.png")

	// Act
	below := pathBelow(root, path)

	// Assert
	if isBelow := below == "a.png"; isBelow != isOnePath {
		t.Fatalf("pathBelow(%s, %s) = %q, want a.png: %t", root, path, below, isOnePath)
	}
}

// A file a listed folder holds is found by its name in another case exactly
// where the file system opens it by that name.
func TestAskFindsAListedFileByTheFileSystemsCase(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "status"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(folder, "STATUS")
	_, statErr := os.Stat(upper)
	isOnePath := statErr == nil

	// Act
	_, _, err := ask(upper, true, nil)

	// Assert
	if isFound := err == nil; isFound != isOnePath || !isFound && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ask(%s) = %v, want found: %t", upper, err, isOnePath)
	}
}

// A run card names its folder, and that folder spelled in another case is
// the one it names exactly where the file system opens it by that spelling.
func TestOpenCardPathNamesARunFolderByTheFileSystemsCase(t *testing.T) {
	// Arrange
	handler, _, _, _ := cardPathFixture(t)
	folder := handler.Service.Store.db.Runs[0].Cwd
	upper := filepath.Join(filepath.Dir(folder), strings.ToUpper(filepath.Base(folder)))
	_, err := os.Stat(upper)
	isOnePath := err == nil

	// Act
	code, body := postCardPath(handler, cardPathBody(t, "run", "run-task-1-8", upper))

	// Assert
	if isUnnamed := strings.Contains(body, "That card names no such path"); isUnnamed == isOnePath {
		t.Fatalf("open %s = %d %s, want it named by the card: %t", upper, code, body, isOnePath)
	}
}

// A rule naming a skill's SKILL.md in another case turns that skill off
// exactly where the file system opens the file by that spelling.
func TestIsSkillEnabledNamesASkillsPathByTheFileSystemsCase(t *testing.T) {
	// Arrange
	folder, upper, isOnePath := caseSpellings(t, "skill")
	skill := filepath.Join(folder, "SKILL.md")
	if err := os.WriteFile(skill, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	config := codexConfig{skillRules: []codexSkillRule{{path: filepath.Join(upper, "SKILL.md")}}}

	// Act
	isEnabled := config.isSkillEnabled(codexSkill{name: "skill", path: skill})

	// Assert
	if isEnabled == isOnePath {
		t.Fatalf("isSkillEnabled(%s) under a rule for %s = %t, want %t", skill, upper, isEnabled, !isOnePath)
	}
}

// Pi's trust of a folder spelled in another case is its trust of the
// project's folder exactly where the file system opens the one by the other.
func TestPiCommandProjectTrustedNamesTheFolderByTheFileSystemsCase(t *testing.T) {
	// Arrange
	folder, upper, isOnePath := caseSpellings(t, "project")
	agent := t.TempDir()
	trust, err := json.Marshal(map[string]bool{upper: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agent, "trust.json"), trust, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	isTrusted := piCommandProjectTrusted(folder, agent, "never")

	// Assert
	if isTrusted != isOnePath {
		t.Fatalf("piCommandProjectTrusted(%s) with %s trusted = %t, want %t", folder, upper, isTrusted, isOnePath)
	}
}

// A live task whose project is the checkout's folder in another case works
// in that project exactly where the file system opens the one by the other,
// so the issues its brief names are taken as the project's.
func TestIssuesNamedInProjectNamesALiveTasksProjectByTheFileSystemsCase(t *testing.T) {
	// Arrange
	_, h := testStore(t)
	checkout, upper, isOnePath := caseSpellings(t, "project")
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "g1", Project: upper, Window: "native", Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.Data, "g1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "g1", "brief.md"), []byte("## Task\n\nResolve issue #42.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	named := issuesNamedInProject(h, fleet.BacklogRows{}, "project", checkout, "fpresta0607/code-goblins")

	// Assert
	if named[42] != isOnePath {
		t.Fatalf("issuesNamedInProject for %s with g1 in %s = %v, want #42 named: %t", checkout, upper, named, isOnePath)
	}
}

// The note a task's last pause saved is its handoff when it lies in the
// task's own folder, spelled in another case exactly where the file system
// opens that folder by that spelling.
func TestHandoffPlacesTakeThePauseNoteByTheFileSystemsCase(t *testing.T) {
	// Arrange
	_, h := testStore(t)
	if err := os.Mkdir(filepath.Join(h.State, "tasktmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(h.State, "TASKTMP"))
	isOnePath := err == nil
	record := state.Lifecycle{Generation: "1", HandoffSaved: true, Handoff: filepath.Join(h.State, "TASKTMP", "g1", "pause-pause-1.md")}
	lifecycle := func(string) (state.Lifecycle, error) { return record, nil }

	// Act
	places, err := handoffPlaces(h, "g1", state.TaskMeta{ID: "g1", SpawnGen: "1"}, nil, lifecycle, nil)

	// Assert
	if isNoteTaken := err == nil && len(places) == 2 && places[0].path == filepath.Join("tasktmp", "g1", "pause-pause-1.md"); isNoteTaken != isOnePath {
		t.Fatalf("handoffPlaces with the note at %s = %+v, %v; want the note taken: %t", record.Handoff, places, err, isOnePath)
	}
}

// A parent waiting on its helper takes the helper's progress when the wait
// names the helper, or the helper names its parent, in another case exactly
// where the file system opens the one task id by the other in the state
// folder.
func TestAParentWaitingOnItsHelperNamesTasksByTheFileSystemsCase(t *testing.T) {
	cases := []struct {
		name, awaited, parent string
	}{
		{name: "the wait names the helper in another case", awaited: "PARENT-TASK-H1", parent: "parent-task"},
		{name: "the helper names its parent in another case", awaited: "parent-task-h1", parent: "PARENT-TASK"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			service, h := fleetService(t)
			liveGoblin(t, h, "parent-task", h.Root)
			helper := state.TaskMeta{ID: "parent-task-h1", Parent: tc.parent, Project: h.Root, Worktree: filepath.Join(h.Root, ".worktrees", "gb-parent-task-h1"), Harness: "claude", Backend: "native", SpawnGen: "s1"}
			if err := state.WriteTaskMeta(h.State, helper); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(filepath.Join(h.State, "PARENT-TASK-H1.meta"))
			isOnePath := err == nil
			git := &worktreeGit{heads: map[string]string{
				filepath.Join(h.Root, ".worktrees", "gb-parent-task"): strings.Repeat("a", 40),
				helper.Worktree: strings.Repeat("a", 40),
			}}
			service.Options.Progress = git
			now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
			writeFile(t, filepath.Join(h.State, "parent-task.status"), now.Format(time.RFC3339)+" waiting on "+tc.awaited+": merging its work once it is done\n")
			if err := service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			git.heads[helper.Worktree] = strings.Repeat("b", 40)
			if err := service.checkFleet(t.Context(), now.Add(15*time.Minute)); err != nil {
				t.Fatal(err)
			}

			// Act
			err = service.checkFleet(t.Context(), now.Add(25*time.Minute))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if isFolded := stallWakes(t, service, "parent-task") == 0; isFolded != isOnePath {
				t.Fatalf("parent waiting on %s took its helper's commit: %t, want %t", tc.awaited, isFolded, isOnePath)
			}
		})
	}
}

// Git Bash is never a bash.exe under SystemRoot, spelled in another case
// exactly where the file system opens SystemRoot by that spelling.
func TestRunShellPathComparesSystemRootByTheFileSystemsCase(t *testing.T) {
	// Arrange
	lower, upper, isOnePath := caseSpellings(t, "windows")
	git := filepath.Join(lower, "cmd", "git.exe")
	lookPath := func(name string) (string, error) {
		if name == "git" {
			return git, nil
		}
		return "", errors.New("not found")
	}
	exists := func(string) bool { return true }
	cases := []struct {
		name        string
		systemRoot  string
		wantRefused bool
	}{
		{name: "its own spelling", systemRoot: lower, wantRefused: true},
		{name: "another case", systemRoot: upper, wantRefused: isOnePath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			path, err := runShellPath("bash", lookPath, exists, tc.systemRoot)

			// Assert
			if isRefused := err != nil; isRefused != tc.wantRefused {
				t.Fatalf("runShellPath(bash) beside %s under SystemRoot %s = %q, %v; want refused: %t", git, tc.systemRoot, path, err, tc.wantRefused)
			}
		})
	}
}
