package gatetest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// newModule makes a git repository holding a Go module with a leaf package a,
// a package b that imports it from a test, and a package c nothing imports,
// with origin/main at its first commit.
func newModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":         "module example.com/m\n\ngo 1.22\n",
		"a/a.go":         "package a\n\nfunc A() int { return 1 }\n",
		"b/b.go":         "package b\n",
		"b/b_test.go":    "package b\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/a\"\n)\n\nfunc TestB(t *testing.T) { _ = a.A() }\n",
		"c/c.go":         "package c\n",
		"docs/readme.md": "docs\n",
	}
	for name, content := range files {
		write(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "add", ".")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "start")
	git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, dir, "switch", "-q", "-c", "feature")
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// The plan reads the branch's changes from where it left main, uncommitted
// ones included, and the module's packages from go list, so a package that
// imports a changed one only from its tests is still chosen.
func TestReadChoosesTheChangedPackageAndItsTestImporter(t *testing.T) {
	// Arrange
	dir := newModule(t)
	write(t, filepath.Join(dir, "a", "a.go"), "package a\n\nfunc A() int { return 2 }\n")
	write(t, filepath.Join(dir, "docs", "readme.md"), "more docs\n")

	// Act
	plan, err := Read(context.Background(), execx.OSRunner{}, dir)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, choice := range plan.Choices {
		got = append(got, choice.String())
	}
	if want := []string{"example.com/m/a (changed)", "example.com/m/b (imports example.com/m/a)"}; plan.Everything || !slices.Equal(got, want) {
		t.Errorf("Read = %q, everything %v; want %q", got, plan.Everything, want)
	}
}

// A new package the branch never added to git is still chosen.
func TestReadChoosesAnUntrackedPackage(t *testing.T) {
	// Arrange
	dir := newModule(t)
	write(t, filepath.Join(dir, "d", "d.go"), "package d\n")

	// Act
	plan, err := Read(context.Background(), execx.OSRunner{}, dir)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, choice := range plan.Choices {
		got = append(got, choice.String())
	}
	if want := []string{"example.com/m/d (changed)"}; plan.Everything || !slices.Equal(got, want) {
		t.Errorf("Read = %q, everything %v; want %q", got, plan.Everything, want)
	}
}

// A root package is chosen for a file it embeds, read from go list, and not
// for a file in a directory no package owns.
func TestReadChoosesARootPackageOnlyForItsEmbeddedFiles(t *testing.T) {
	for file, want := range map[string][]string{
		"docs/readme.md":  {"example.com/m (changed)"},
		"frontend/app.ts": nil,
	} {
		t.Run(file, func(t *testing.T) {
			// Arrange
			dir := newModule(t)
			write(t, filepath.Join(dir, "m.go"), "package m\n\nimport _ \"embed\"\n\n//go:embed docs/readme.md\nvar Readme string\n")
			git(t, dir, "add", ".")
			git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "embed")
			git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
			write(t, filepath.Join(dir, filepath.FromSlash(file)), "changed\n")

			// Act
			plan, err := Read(context.Background(), execx.OSRunner{}, dir)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, choice := range plan.Choices {
				got = append(got, choice.String())
			}
			if plan.Everything || !slices.Equal(got, want) {
				t.Errorf("Read = %q, everything %v; want %q", got, plan.Everything, want)
			}
		})
	}
}

// A branch that changed no Go package has nothing to test.
func TestReadChoosesNothingWhenNoPackageChanged(t *testing.T) {
	// Arrange
	dir := newModule(t)
	write(t, filepath.Join(dir, "docs", "readme.md"), "more docs\n")

	// Act
	plan, err := Read(context.Background(), execx.OSRunner{}, dir)

	// Assert
	if err != nil || len(plan.Choices) != 0 || plan.Everything {
		t.Errorf("Read = %+v, %v; want nothing chosen", plan, err)
	}
}

// Without a default branch to compare against, what changed is unknown, and
// the plan says so rather than choosing nothing.
func TestReadRefusesWithoutADefaultBranch(t *testing.T) {
	// Arrange
	dir := newModule(t)
	git(t, dir, "update-ref", "-d", "refs/remotes/origin/main")

	// Act
	_, err := Read(context.Background(), execx.OSRunner{}, dir)

	// Assert
	if err == nil {
		t.Fatal("Read found a plan with no default branch to compare against")
	}
}
