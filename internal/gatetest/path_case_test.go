package gatetest

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// casePackage makes the folder pkg in a new repository root and returns the
// root, the package in it, which embeds WEB/*, and whether the file system
// opens pkg as PKG.
func casePackage(t *testing.T) (root string, p Package, isOnePath bool) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, "pkg")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(root, "PKG"))
	return root, Package{ImportPath: "example.com/m/pkg", Dir: dir, EmbedPatterns: []string{"WEB/*"}}, err == nil
}

// isChosen reports whether reach chooses the package at importPath.
func isChosen(reach Reach, importPath string) bool {
	return slices.ContainsFunc(reach.Choices, func(choice Choice) bool { return choice.ImportPath == importPath })
}

// A changed file spelled in another case than its package's folder, or than
// the package's embed pattern, is the package's, and a contract's package
// named in another case is that package, exactly where the file system opens
// the one spelling by the other.
func TestClassifyNamesPackagesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root, p, isOnePath := casePackage(t)
	contract := Policy{Contracts: []Contract{{Paths: []string{"docs/**"}, Packages: []string{"PKG"}, Why: "reads the docs"}}, Outside: []Outside{}}
	cases := []struct {
		name   string
		file   string
		policy Policy
	}{
		{name: "its code", file: "PKG/a.go"},
		{name: "a file its embed pattern names in another case", file: "pkg/web/app.js"},
		{name: "a contract's package", file: "docs/install.md", policy: contract},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			reach := Classify(root, "example.com/m", []string{tc.file}, []Package{p}, tc.policy)

			// Assert
			if got := isChosen(reach, p.ImportPath); got != isOnePath {
				t.Fatalf("Classify(%s) = %+v, want %s chosen: %t", tc.file, reach, p.ImportPath, isOnePath)
			}
		})
	}
}

// noBase is a git that finds no file at the base.
type noBase struct{}

func (noBase) Run(context.Context, execx.Request) (execx.Result, error) {
	return execx.Result{}, nil
}

// A changed test file in a folder spelled in another case than a package's
// is that package's test exactly where the file system opens the one folder
// by the other; elsewhere it is in another folder, which is no package.
func TestEditedTestsNamesPackagesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root, p, isOnePath := casePackage(t)
	test := filepath.Join(root, "PKG", "a_test.go")
	if err := os.MkdirAll(filepath.Dir(test), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(test, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	found := findings{root: root, base: "base", changed: []string{"PKG/a_test.go"}, packages: []Package{p}}

	// Act
	edited, err := editedTests(context.Background(), noBase{}, found)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if isEdited := slices.Equal(edited[p.ImportPath], []string{"TestNew"}); isEdited != isOnePath || len(edited) > 1 {
		t.Fatalf("editedTests = %v, want TestNew of %s: %t", edited, p.ImportPath, isOnePath)
	}
}

// A directory the policy lists in another case than the package's folder is
// that package's exactly where the file system opens the one by the other.
func TestThePolicysDirectoriesNamePackagesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root, p, isOnePath := casePackage(t)
	dirs := map[string]string{p.ImportPath: "pkg"}

	// Act
	listed := pathsIn(root, []Package{p}, []string{"PKG"})
	importPath, isFound := importPathOf(root, dirs, "PKG")

	// Assert
	if listed[p.ImportPath] != isOnePath {
		t.Errorf("pathsIn(PKG) = %v, want %s listed: %t", listed, p.ImportPath, isOnePath)
	}
	if isFound != isOnePath || isFound && importPath != p.ImportPath {
		t.Errorf("importPathOf(PKG) = %q, %t; want %s found: %t", importPath, isFound, p.ImportPath, isOnePath)
	}
}

// A changed file a job's rule names in another case alters the job exactly
// where the file system of the plan's root ignores case.
func TestChooseJobsNamesTheRulesFilesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root, _, isOnePath := casePackage(t)
	plan := Plan{Root: root, Required: Affected, Changed: []string{"FRONTEND/app.ts"}}
	table := JobTable{Frontend: JobRule{Paths: []string{"frontend/**"}}}

	// Act
	jobs := ChooseJobs(plan, table, "./tools/ciplan")

	// Assert
	if jobs.Frontend != isOnePath {
		t.Fatalf("ChooseJobs(%v) frontend = %t, want %t", plan.Changed, jobs.Frontend, isOnePath)
	}
}
