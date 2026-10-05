package gatetest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
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
	plan, err := Read(context.Background(), execx.OSRunner{}, dir, "")

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
	plan, err := Read(context.Background(), execx.OSRunner{}, dir, "")

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
			plan, err := Read(context.Background(), execx.OSRunner{}, dir, "")

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

// A file only an external test embeds, outside testdata, still chooses its
// package and the packages that import it.
func TestReadChoosesAPackageForAFileItsExternalTestEmbeds(t *testing.T) {
	// Arrange
	dir := newModule(t)
	write(t, filepath.Join(dir, "a", "fixtures", "x.json"), "{}\n")
	write(t, filepath.Join(dir, "a", "a_x_test.go"), "package a_test\n\nimport _ \"embed\"\n\n//go:embed fixtures/x.json\nvar fixture string\n")
	git(t, dir, "add", ".")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "fixture")
	git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	write(t, filepath.Join(dir, "a", "fixtures", "x.json"), "{\"changed\": true}\n")

	// Act
	plan, err := Read(context.Background(), execx.OSRunner{}, dir, "")

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

// A branch that deletes a package still tests the package whose test imports
// it, so the stale import fails here rather than only in CI.
func TestReadChoosesTheTestImporterOfADeletedPackage(t *testing.T) {
	// Arrange
	dir := newModule(t)
	if err := os.RemoveAll(filepath.Join(dir, "a")); err != nil {
		t.Fatal(err)
	}

	// Act
	plan, err := Read(context.Background(), execx.OSRunner{}, dir, "")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, choice := range plan.Choices {
		got = append(got, choice.String())
	}
	if want := []string{"example.com/m/b (imports example.com/m/a)"}; plan.Everything || !slices.Equal(got, want) {
		t.Errorf("Read = %q, everything %v; want %q", got, plan.Everything, want)
	}
}

// A branch that changed no Go package has nothing to test.
func TestReadChoosesNothingWhenNoPackageChanged(t *testing.T) {
	// Arrange
	dir := newModule(t)
	write(t, filepath.Join(dir, "docs", "readme.md"), "more docs\n")

	// Act
	plan, err := Read(context.Background(), execx.OSRunner{}, dir, "")

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
	_, err := Read(context.Background(), execx.OSRunner{}, dir, "")

	// Assert
	if err == nil {
		t.Fatal("Read found a plan with no default branch to compare against")
	}
}

// commitToMain commits the working tree and moves the default branch to it.
func commitToMain(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "add", ".")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "more")
	git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	out, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// The policy that applies is the default branch's, read from where the branch
// left it: a branch that commits an edit to its own copy, as a fix commit
// under a gate could, is still planned by the reviewed one.
func TestReadTakesThePolicyFromWhereTheBranchLeftTheDefaultBranch(t *testing.T) {
	// Arrange
	dir := newModule(t)
	write(t, filepath.Join(dir, "config", "verify.json"), `{"version": 1, "slow_packages": ["a"]}`)
	commitToMain(t, dir)
	base := gitOutput(t, dir, "rev-parse", "HEAD")
	write(t, filepath.Join(dir, "config", "verify.json"), `{"version": 1, "slow_packages": []}`)
	git(t, dir, "add", ".")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "loosen the policy")
	write(t, filepath.Join(dir, "a", "a.go"), "package a\n\nfunc A() int { return 2 }\n")

	// Act
	plan, err := Read(context.Background(), execx.OSRunner{}, dir, Fast)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	wantLeft := []string{"tests of example.com/m/a (a slow package)", "tests of example.com/m/b (imports example.com/m/a)"}
	if len(plan.Tests) != 0 || !slices.Equal(deferred(plan.Left), wantLeft) {
		t.Errorf("Read at fast = tests %q, left %q; want no tests and %q, as the default branch's policy lists a as slow", plan.Tests, deferred(plan.Left), wantLeft)
	}
	if want := "config/verify.json version 1 at " + base[:8]; plan.Policy != want {
		t.Errorf("plan.Policy = %q; want %q", plan.Policy, want)
	}
}

// The plan says exactly what it is a plan for: the commit checked out, how
// many files differ from it uncommitted or untracked, and the toolchain.
func TestReadRecordsTheCommitTheUncommittedFilesAndTheToolchain(t *testing.T) {
	// Arrange
	dir := newModule(t)
	write(t, filepath.Join(dir, "a", "a.go"), "package a\n\nfunc A() int { return 2 }\n")
	git(t, dir, "add", ".")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "change")
	write(t, filepath.Join(dir, "c", "c.go"), "package c\n\nconst C = 1\n")
	write(t, filepath.Join(dir, "d", "d.go"), "package d\n")

	// Act
	plan, err := Read(context.Background(), execx.OSRunner{}, dir, "")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if want := gitOutput(t, dir, "rev-parse", "HEAD"); plan.Commit != want || plan.Uncommitted != 2 {
		t.Errorf("plan.Commit = %q with %d uncommitted; want %q with 2 (c/c.go changed, d/d.go untracked)", plan.Commit, plan.Uncommitted, want)
	}
	if want := runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH; plan.Toolchain != want || plan.Module != "example.com/m" {
		t.Errorf("plan.Toolchain = %q, plan.Module = %q; want %q and example.com/m", plan.Toolchain, plan.Module, want)
	}
	if !fsx.SamePath(plan.Root, dir) {
		t.Errorf("plan.Root = %q; want the repository's top directory %q", plan.Root, dir)
	}
}

func TestReadReachesProductionAndExternalTestImportersTransitively(t *testing.T) {
	// Arrange
	dir := newModule(t)
	write(t, filepath.Join(dir, "d", "d.go"), "package d\n\nimport _ \"example.com/m/b\"\n\nfunc D() int { return 1 }\n")
	write(t, filepath.Join(dir, "e", "e.go"), "package e\n")
	write(t, filepath.Join(dir, "e", "e_test.go"), "package e_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/d\"\n)\n\nfunc TestE(t *testing.T) { _ = d.D() }\n")
	commitToMain(t, dir)
	write(t, filepath.Join(dir, "a", "a.go"), "package a\n\nfunc A() int { return 2 }\n")

	// Act
	plan, err := Read(context.Background(), execx.OSRunner{}, dir, "")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	wantChoices := []Choice{
		{ImportPath: "example.com/m/a"},
		{ImportPath: "example.com/m/b", Imports: "example.com/m/a"},
		{ImportPath: "example.com/m/d", Imports: "example.com/m/b"},
		{ImportPath: "example.com/m/e", Imports: "example.com/m/d"},
	}
	wantPackages := []string{"example.com/m/a", "example.com/m/b", "example.com/m/d", "example.com/m/e"}
	if plan.Everything || plan.Level != Affected || plan.Required != Affected || !slices.Equal(plan.Choices, wantChoices) ||
		!slices.Equal(plan.Vet, wantPackages) || !slices.Equal(plan.Tests, wantPackages) || len(plan.Left) != 0 {
		t.Errorf("Read = choices %v, everything %v, level %s, required %s, vet %q, tests %q, left %v; want %v, false, affected, affected, %q for vet and tests, nothing left",
			plan.Choices, plan.Everything, plan.Level, plan.Required, plan.Vet, plan.Tests, plan.Left, wantChoices, wantPackages)
	}
}

// printsVersion is a runner whose every command prints the version it was
// given, as node --version prints its own.
type printsVersion string

func (version printsVersion) Run(context.Context, execx.Request) (execx.Result, error) {
	return execx.Result{Stdout: []byte(string(version) + "\n")}, nil
}

// An install is in place only when its output holds the stamp of an install
// made from the same command, the same inputs and the same versions: an
// output with no stamp, or a change to any of those, runs it again, and the
// stamp a run writes once the install passed is read back.
func TestAnInstallIsInPlaceOnlyWhenMadeFromTheSameCommandInputsAndVersions(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	write(t, filepath.Join(dir, "web", "package-lock.json"), "lock 1\n")
	check := func(command ...string) Check {
		return Check{Name: "web", Dir: "web", Install: &Install{Command: command, Inputs: []string{"web/package-lock.json"}, Versions: [][]string{{"node", "--version"}}, Output: "web/node_modules"}}
	}
	plan := func(check Check, version string) Planned {
		t.Helper()
		planned := Planned{Check: check}
		if err := installed(context.Background(), printsVersion(version), dir, &planned); err != nil {
			t.Fatal(err)
		}
		return planned
	}

	// Act
	fresh := plan(check("npm", "ci"), "v24.13.0")
	if err := os.MkdirAll(filepath.Join(dir, "web", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MarkInstalled(dir, fresh); err != nil {
		t.Fatal(err)
	}
	same := plan(check("npm", "ci"), "v24.13.0")
	otherVersion := plan(check("npm", "ci"), "v25.0.0")
	otherCommand := plan(check("npm", "install"), "v24.13.0")
	write(t, filepath.Join(dir, "web", "package-lock.json"), "lock 2\n")
	otherInputs := plan(check("npm", "ci"), "v24.13.0")

	// Assert
	if fresh.IsInstalled || fresh.Fingerprint == "" {
		t.Errorf("an output with no stamp = installed %v, fingerprint %q; want not installed, with a fingerprint to stamp", fresh.IsInstalled, fresh.Fingerprint)
	}
	if !same.IsInstalled || same.Fingerprint != fresh.Fingerprint {
		t.Errorf("the same command, inputs and version after the stamp = installed %v; want installed", same.IsInstalled)
	}
	for name, other := range map[string]Planned{"another version": otherVersion, "another command": otherCommand, "other inputs": otherInputs} {
		if other.IsInstalled || other.Fingerprint == fresh.Fingerprint {
			t.Errorf("%s = installed %v, fingerprint %q; want not installed, under a fingerprint of its own", name, other.IsInstalled, other.Fingerprint)
		}
	}
}

// An install input that is not there is a policy that names the wrong file,
// and the plan says which, rather than fingerprinting what is missing.
func TestAnInstallRefusesAnInputThatIsNotThere(t *testing.T) {
	// Arrange
	planned := Planned{Check: Check{Name: "web", Dir: "web", Install: &Install{Command: []string{"npm", "ci"}, Inputs: []string{"web/package-lock.json"}, Output: "web/node_modules"}}}

	// Act
	err := installed(context.Background(), printsVersion("v24.13.0"), t.TempDir(), &planned)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "web/package-lock.json, an input of the web check's install") {
		t.Errorf("installed = %v; want an error naming the missing input and its check", err)
	}
}
