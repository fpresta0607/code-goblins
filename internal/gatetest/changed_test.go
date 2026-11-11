package gatetest

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// baseTests is b's test file as the default branch has it: TestB with a
// comment, and TestKept, which no row edits.
const baseTests = "package b\n\nimport (\n\t\"testing\"\n\n\t\"example.com/m/a\"\n)\n\n// TestB checks b.\nfunc TestB(t *testing.T) { _ = a.A() }\n\nfunc TestKept(t *testing.T) {}\n"

// The changed level vets what the change requires and runs, by name, only
// the tests the change added or edited, each package's in a command of its
// own, and names every other test it leaves to CI.
func TestReadAtTheChangedLevelRunsOnlyTheTestsTheChangeAddedOrEdited(t *testing.T) {
	for _, row := range []struct {
		name  string
		files map[string]string
		named []string
		left  []string
	}{
		{
			name:  "an edited test and an added one",
			files: map[string]string{"b/b_test.go": strings.Replace(baseTests, "_ = a.A() }", "_ = a.A() + 1 }", 1) + "\nfunc TestB2(t *testing.T) {}\n"},
			named: []string{"go test -json -count=1 -p 2 -timeout 45m -run ^(TestB|TestB2)$ example.com/m/b"},
			left:  []string{"the other tests of example.com/m/b (CI runs them before the merge)"},
		},
		{
			name:  "a new test file",
			files: map[string]string{"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n\nfunc TestMain(m *testing.M) { m.Run() }\n\nfunc Testable(t *testing.T) {}\n"},
			named: []string{"go test -json -count=1 -p 2 -timeout 45m -run ^(TestA)$ example.com/m/a"},
			left:  []string{"the other tests of example.com/m/a (CI runs them before the merge)", "tests of example.com/m/b (CI runs them before the merge)"},
		},
		{
			name:  "a comment and a helper alone",
			files: map[string]string{"b/b_test.go": strings.Replace(baseTests, "// TestB checks b.", "// TestB checks b, as before.", 1) + "\nfunc helper() {}\n"},
			left:  []string{"tests of example.com/m/b (CI runs them before the merge)"},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			// Arrange
			dir := newModule(t)
			write(t, filepath.Join(dir, "b", "b_test.go"), baseTests)
			git(t, dir, "add", ".")
			git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "base tests")
			git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
			for name, content := range row.files {
				write(t, filepath.Join(dir, filepath.FromSlash(name)), content)
			}

			// Act
			plan, err := Read(context.Background(), execx.OSRunner{}, dir, Changed)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			var named []string
			for _, step := range plan.Named {
				named = append(named, strings.Join(step.Command, " "))
			}
			if plan.Level != Changed || plan.Required != Affected || len(plan.Tests) != 0 || len(plan.Alone) != 0 || !slices.Equal(named, row.named) {
				t.Errorf("plan at %s requiring %s tests %q, alone %q, named %q; want only %q", plan.Level, plan.Required, plan.Tests, plan.Alone, named, row.named)
			}
			wantVet, _, _ := scope(Affected, plan.Choices, plan.Everything, nil)
			if !slices.Equal(plan.Vet, wantVet) || len(plan.Vet) == 0 {
				t.Errorf("vet = %q; want the affected level's %q", plan.Vet, wantVet)
			}
			if got := deferred(plan.Left); !slices.Equal(got, row.left) {
				t.Errorf("left = %q; want %q", got, row.left)
			}
		})
	}
}
