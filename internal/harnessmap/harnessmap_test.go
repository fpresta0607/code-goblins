package harnessmap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// mklink makes a directory junction as cfo does on Windows.
func mklink(link, target string) error {
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		return &os.PathError{Op: "mklink " + strings.TrimSpace(string(out)), Path: link, Err: err}
	}
	return nil
}

func profile(t *testing.T) (string, Map) {
	t.Helper()
	user := t.TempDir()
	return user, Find(func(string) string { return "" }, user)
}

var shipped = fstest.MapFS{
	"lavish/SKILL.md":        {Data: []byte("lavish v2")},
	"lavish/references/a.md": {Data: []byte("a")},
	"stow/SKILL.md":          {Data: []byte("stow")},
	"README-not-a-skill.txt": {Data: []byte("x")},
}

func TestFindReadsEachHarnessFromItsVariableOrItsDefault(t *testing.T) {
	user := t.TempDir()
	codex := filepath.Join(t.TempDir(), "codex-home")
	if err := os.MkdirAll(filepath.Join(user, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"CODEX_HOME": codex}

	m := Find(func(name string) string { return env[name] }, user)

	want := map[string]Root{
		"claude": {Path: filepath.Join(user, ".claude"), Source: "default", Present: true},
		"codex":  {Path: codex, Source: "CODEX_HOME", Present: false},
		"pi":     {Path: filepath.Join(user, ".pi", "agent"), Source: "default", Present: false},
	}
	for name, w := range want {
		got, ok := m.Harness(name)
		if !ok || got.Path != w.Path || got.Source != w.Source || got.Present != w.Present || got.Skills != filepath.Join(w.Path, "skills") {
			t.Errorf("%s = %+v, want %+v", name, got, w)
		}
	}
	if m.SharedSkills != filepath.Join(user, ".agents", "skills") {
		t.Errorf("shared skills = %q", m.SharedSkills)
	}
}

func TestInstallSkillsKeepsOneCopyAndAJunctionForClaude(t *testing.T) {
	// Arrange
	_, m := profile(t)

	// Act
	result, err := InstallSkills(shipped, m, mklink)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.Names, ",") != "lavish,stow" || strings.Join(result.Changed, ",") != "lavish,stow" || len(result.Kept) != 0 {
		t.Fatalf("installed %+v; want lavish and stow installed and written", result)
	}
	claude, _ := m.Harness("claude")
	for _, name := range []string{"lavish", "stow"} {
		shared := filepath.Join(m.SharedSkills, name)
		target, err := os.Readlink(filepath.Join(claude.Skills, name))
		if err != nil || !sameDir(target, shared) {
			t.Errorf("%s in Claude's skills = %q, %v; want a junction to %s", name, target, err, shared)
		}
		if info, err := os.Lstat(shared); err != nil || !info.IsDir() {
			t.Errorf("%s is not a real folder: %v", shared, err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(m.SharedSkills, "lavish", "references", "a.md")); string(got) != "a" {
		t.Errorf("lavish/references/a.md = %q", got)
	}
	if problems := Check(m); len(problems) != 3 {
		// Only the three missing harness roots: one copy, no duplicate.
		t.Errorf("problems = %+v, want only the three harness roots this profile lacks", problems)
	}
}

func TestInstallSkillsNeverTouchesASkillItDidNotPutThere(t *testing.T) {
	// Arrange: the Overlord keeps his own lavish in the shared folder.
	_, m := profile(t)
	own := filepath.Join(m.SharedSkills, "lavish")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "SKILL.md"), []byte("his own"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	result, err := InstallSkills(shipped, m, mklink)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(own, "SKILL.md")); string(got) != "his own" {
		t.Errorf("his SKILL.md = %q, want it untouched", got)
	}
	if strings.Join(result.Names, ",") != "stow" || len(result.Kept) != 1 || !strings.Contains(result.Kept[0], own) {
		t.Errorf("installed %+v; want stow installed and his lavish named as kept", result)
	}
}

// A re-run of the same build writes nothing and says so; a build that ships
// a skill changed updates it and drops what it no longer ships.
func TestReinstallUpdatesItsOwnSkillAndDropsWhatItNoLongerShips(t *testing.T) {
	_, m := profile(t)
	if _, err := InstallSkills(shipped, m, mklink); err != nil {
		t.Fatal(err)
	}
	same, err := InstallSkills(shipped, m, mklink)
	if err != nil || len(same.Changed) != 0 {
		t.Fatalf("a re-run of the same skills changed %v, %v; want nothing", same.Changed, err)
	}
	next := fstest.MapFS{"lavish/SKILL.md": {Data: []byte("lavish v3")}}

	updated, err := InstallSkills(next, m, mklink)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(updated.Changed, ",") != "lavish" {
		t.Errorf("changed %v, want lavish", updated.Changed)
	}

	if got, _ := os.ReadFile(filepath.Join(m.SharedSkills, "lavish", "SKILL.md")); string(got) != "lavish v3" {
		t.Errorf("SKILL.md = %q, want the new build's", got)
	}
	if _, err := os.Stat(filepath.Join(m.SharedSkills, "lavish", "references", "a.md")); !os.IsNotExist(err) {
		t.Errorf("a file the build no longer ships survived: %v", err)
	}
}

func TestRemoveSkillsTakesOnlyWhatItInstalled(t *testing.T) {
	_, m := profile(t)
	if _, err := InstallSkills(shipped, m, mklink); err != nil {
		t.Fatal(err)
	}
	his := filepath.Join(m.SharedSkills, "his-skill")
	if err := os.MkdirAll(his, 0o755); err != nil {
		t.Fatal(err)
	}

	removed, err := RemoveSkills(m, []string{"lavish", "stow", "his-skill"})

	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(removed, ",") != "lavish,stow" {
		t.Errorf("removed %v, want lavish and stow", removed)
	}
	claude, _ := m.Harness("claude")
	for _, gone := range []string{filepath.Join(m.SharedSkills, "lavish"), filepath.Join(claude.Skills, "lavish")} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived: %v", gone, err)
		}
	}
	if _, err := os.Stat(his); err != nil {
		t.Errorf("his skill was removed: %v", err)
	}
}

func TestCheckFlagsDuplicatesBrokenJunctionsAndMissingRoots(t *testing.T) {
	// Arrange: lavish kept as a real folder in both the shared and Codex's
	// skills, and a Claude junction whose target is gone.
	user, m := profile(t)
	codex, _ := m.Harness("codex")
	claude, _ := m.Harness("claude")
	for _, dir := range []string{filepath.Join(m.SharedSkills, "lavish"), filepath.Join(codex.Skills, "Lavish"), claude.Skills, filepath.Join(user, "gone")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := mklink(filepath.Join(claude.Skills, "old"), filepath.Join(user, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(user, "gone")); err != nil {
		t.Fatal(err)
	}
	m = Find(func(string) string { return "" }, user)

	// Act
	problems := Check(m)

	// Assert
	kinds := map[string]int{}
	for _, problem := range problems {
		kinds[problem.Kind]++
	}
	if kinds["duplicate"] != 1 || kinds["broken"] != 1 || kinds["missing"] != 1 {
		t.Errorf("problems = %+v, want one duplicate, one broken junction and pi's missing root", problems)
	}
}

func TestWriteAndReadRoundTripTheMap(t *testing.T) {
	_, m := profile(t)
	m.Installed = []string{"lavish"}
	state := t.TempDir()
	if err := Write(state, m); err != nil {
		t.Fatal(err)
	}
	got, err := Read(state)
	if err != nil || got.SharedSkills != m.SharedSkills || len(got.Roots) != 3 || got.Installed[0] != "lavish" {
		t.Errorf("Read = %+v, %v; want the map written", got, err)
	}
}
