package uninstall

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// caseSpellings makes a folder named name in parent and returns it, its
// name in capitals beside it, and whether the file system opens the one by
// the other.
func caseSpellings(t *testing.T, parent, name string) (lower, upper string, isOnePath bool) {
	t.Helper()
	lower, upper = filepath.Join(parent, name), filepath.Join(parent, strings.ToUpper(name))
	if err := os.MkdirAll(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(upper)
	return lower, upper, err == nil
}

// A path spelled in another case is in a folder exactly where the file
// system opens the folder by that spelling.
func TestWithinComparesFoldersByTheFileSystemsCase(t *testing.T) {
	// Arrange
	folder, upper, isOnePath := caseSpellings(t, t.TempDir(), "home")
	cases := []struct {
		name string
		path string
	}{
		{name: "the folder", path: upper},
		{name: "a file in it", path: filepath.Join(upper, "bin", "goblins")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isWithin := within(tc.path, folder)

			// Assert
			if isWithin != isOnePath {
				t.Fatalf("within(%s, %s) = %t, want %t", tc.path, folder, isWithin, isOnePath)
			}
		})
	}
}

// Only the system's separators end a folder's name: a backslash elsewhere is
// a character of one name, and a folder at a volume's root holds what is
// under it.
func TestWithinReadsFoldersBySeparator(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	cases := []struct {
		name       string
		path       string
		folder     string
		wantWithin bool
	}{
		{name: "a backslash after the folder", path: dir + `\bin`, folder: dir, wantWithin: os.IsPathSeparator('\\')},
		{name: "a folder at the volume's root", path: dir, folder: filepath.VolumeName(dir) + string(filepath.Separator), wantWithin: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isWithin := within(tc.path, tc.folder)

			// Assert
			if isWithin != tc.wantWithin {
				t.Fatalf("within(%s, %s) = %t, want %t", tc.path, tc.folder, isWithin, tc.wantWithin)
			}
		})
	}
}

// Claude Code's and pi's records named for the home's folders in another
// case are the home's exactly where the file system that holds the records
// opens the one name by the other.
func TestConversationsNameRecordsByTheFileSystemsCase(t *testing.T) {
	// Arrange
	m := testMachine(t)
	claude := filepath.Join(m.Install.HarnessDirs["claude"], "projects")
	pi := filepath.Join(m.Install.HarnessDirs["pi"], "sessions")
	write(t, filepath.Join(claude, strings.ToUpper(slug(m.Home.Root)), "ceo.jsonl"), "{}")
	write(t, filepath.Join(pi, strings.ToUpper(piSlug(filepath.Join(m.Home.Root, "worktrees", "demo", "g1"))), "s.jsonl"), "{}")
	_, statErr := os.Stat(filepath.Join(claude, slug(m.Home.Root)))
	isOnePath := statErr == nil

	// Act
	items, err := m.conversations()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if isFound := len(items) == 2; isFound != isOnePath || len(items) != 0 && len(items) != 2 {
		t.Fatalf("found\n%s\nwant both records found: %t", whats(items), isOnePath)
	}
}

// A worktree whose .git file names its checkout's .git and worktrees folders
// in another case is that checkout's exactly where the file system opens
// those folders by that spelling.
func TestCheckoutOfNamesGitsFoldersByTheFileSystemsCase(t *testing.T) {
	// Arrange
	checkout := t.TempDir()
	_, _, isOnePath := caseSpellings(t, checkout, filepath.Join(".git", "worktrees", "g1"))
	gitCheckout := filepath.Join(checkout, ".GIT")
	if isOnePath {
		gitCheckout = checkout
	}
	cases := []struct {
		name         string
		gitdir       string
		wantOK       bool
		wantCheckout string
	}{
		{name: "the worktrees folder", gitdir: filepath.Join(checkout, ".git", "WORKTREES", "g1"), wantOK: isOnePath, wantCheckout: checkout},
		{name: "the .git folder", gitdir: filepath.Join(checkout, ".GIT", "worktrees", "g1"), wantOK: true, wantCheckout: gitCheckout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			worktree := t.TempDir()
			write(t, filepath.Join(worktree, ".git"), "gitdir: "+tc.gitdir+"\n")

			// Act
			got, isOK := checkoutOf(worktree)

			// Assert
			if isOK != tc.wantOK || isOK && got != tc.wantCheckout {
				t.Fatalf("checkoutOf with gitdir %s = %q, %t; want %q, %t", tc.gitdir, got, isOK, tc.wantCheckout, tc.wantOK)
			}
		})
	}
}

// Two spellings of one checkout are one checkout, and two folders whose
// names differ only in case are two, as the file system has them.
func TestKnownCheckoutsCountsCheckoutsByTheFileSystemsCase(t *testing.T) {
	// Arrange
	m := testMachine(t)
	parent := t.TempDir()
	lower, upper := filepath.Join(parent, "project"), filepath.Join(parent, "PROJECT")
	if err := os.MkdirAll(filepath.Join(lower, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(upper)
	isOnePath := statErr == nil
	if err := os.MkdirAll(filepath.Join(upper, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := 2
	if isOnePath {
		want = 1
	}

	// Act
	checkouts, err := m.knownCheckouts([]goblinWorktree{{Checkout: lower}, {Checkout: upper}})

	// Assert
	if err != nil || len(checkouts) != want {
		t.Fatalf("knownCheckouts(%s, %s) = %v, %v; want %d", lower, upper, checkouts, err, want)
	}
}

// Paths named twice in two spellings are one path exactly where the file
// system opens the one by the other.
func TestUniqueKeepsPathsByTheFileSystemsCase(t *testing.T) {
	// Arrange
	lower, upper, isOnePath := caseSpellings(t, t.TempDir(), "skills")

	// Act
	kept := unique([]string{lower, upper})

	// Assert
	if isOne := len(kept) == 1; isOne != isOnePath {
		t.Fatalf("unique(%s, %s) = %v, want one path: %t", lower, upper, kept, isOnePath)
	}
}

// A folder holding only a folder the install made, named in another case,
// is empty but for it exactly where the file system opens the one name by
// the other.
func TestEmptyButNamesMadeFoldersByTheFileSystemsCase(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	lower, _, isOnePath := caseSpellings(t, folder, "skills")
	if err := os.Remove(lower); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(folder, "SKILLS"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Act
	isEmpty := emptyBut(folder, []string{lower})

	// Assert
	if isEmpty != isOnePath {
		t.Fatalf("emptyBut(%s, %s) = %t, want %t", folder, lower, isEmpty, isOnePath)
	}
}

// Claude Code's folder named in another case is the profile's .claude, whose
// entries are in the profile's .claude.json, exactly where the file system
// opens it by that spelling.
func TestTrustNamesClaudesFolderByTheFileSystemsCase(t *testing.T) {
	// Arrange
	m := testMachine(t)
	_, upper, isOnePath := caseSpellings(t, m.Profile, ".claude")
	m.Install.HarnessDirs["claude"] = upper
	g1 := filepath.Join(m.Home.Root, "worktrees", "demo", "g1")
	claude, err := json.Marshal(map[string]any{"projects": map[string]any{filepath.ToSlash(g1): map[string]bool{"hasTrustDialogAccepted": true}}})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Profile, ".claude.json"), string(claude))

	// Act
	items, err := m.trust()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if isFound := len(items) == 1; isFound != isOnePath {
		t.Fatalf("found\n%s\nwant Claude Code's entries in the profile's .claude.json: %t", whats(items), isOnePath)
	}
}

// A PATH entry the install added, now spelled in another case, is the
// install's to name and to take off exactly where the file system opens it
// by that spelling.
func TestToolsNamePathEntriesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	m := testMachine(t)
	added, upper, isOnePath := caseSpellings(t, m.Profile, "bin")
	t.Setenv(installbase.UserEnvFileVariable, filepath.Join(t.TempDir(), "env.json"))
	m.Install.Env = installbase.NewEnvStore(execx.OSRunner{})
	if err := m.Install.Env.Set("Path", upper); err != nil {
		t.Fatal(err)
	}
	fetched, err := json.Marshal(Fetched{Path: []string{added}})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Home.State, FetchedFile), string(fetched))

	// Act
	items, toolsErr := m.tools(context.Background(), Choices{RemoveTools: true})
	doAll(t, items)

	// Assert
	if toolsErr != nil {
		t.Fatal(toolsErr)
	}
	left, _, err := m.Install.Env.Get("Path")
	if err != nil {
		t.Fatal(err)
	}
	if isNamed := len(items) == 1; isNamed != isOnePath || isOnePath && left != "" {
		t.Fatalf("found\n%s\nwith PATH %q left; want the entry named and taken off: %t", whats(items), left, isOnePath)
	}
}

// A copy of the uninstall an earlier run left is this run's own when its
// folder is named in another case exactly where the file system opens the
// one name by the other.
func TestFoldersKnowTheUninstallsOwnCopyByTheFileSystemsCase(t *testing.T) {
	// Arrange
	m := testMachine(t)
	aside, upper, isOnePath := caseSpellings(t, filepath.Dir(m.Home.Root), AsidePrefix+"abc")
	m.Aside = upper

	// Act
	items, err := m.folders(context.Background())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	isNamed := strings.Contains(whats(items), "a copy of the uninstall an earlier run left")
	if isNamed == isOnePath {
		t.Fatalf("found\n%s\nwant %s named as an earlier run's: %t", whats(items), aside, !isOnePath)
	}
}
