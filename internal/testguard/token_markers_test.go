package testguard

import (
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func guardBody(body string) string {
	return "package x\n\nfunc TestGuardHolds(t *testing.T) {\n" + body + "}\n"
}

// A skip counts when it is code, and never when it is text inside a string
// or comment literal: in the skip-count run of 2026-09-27 tests-kept parked
// on eight lines of the checker's own test table, each a Go string holding
// skip text. The two lines that share a string with a real skip must still
// count, or a gate could hide a skip behind a string on its line.
func TestCheckCountsASkipOnlyWhenItIsCode(t *testing.T) {
	const reported = ": gate commits added 1 more skip line than they removed, still at HEAD: "
	for _, test := range []struct {
		name  string
		file  string
		base  string
		gated string
		want  []string
	}{
		{
			name:  "skip text only inside a Go string",
			file:  "guard_test.go",
			base:  guardBody(""),
			gated: guardBody("\tfixture := \"t.Skip(\\\"parked\\\")\"\n\t_ = fixture\n"),
		},
		{
			name:  "skip text inside a Go raw string spanning lines",
			file:  "guard_test.go",
			base:  guardBody(""),
			gated: guardBody("\tfixture := `first line\nt.Skip(\"parked\")\n`\n\t_ = fixture\n"),
		},
		{
			name:  "skip text in a Go comment",
			file:  "guard_test.go",
			base:  guardBody(""),
			gated: guardBody("\t// t.Skip(\"until the fix lands\")\n"),
		},
		{
			name:  "a real t.Skip sharing a line with a string that holds skip text",
			file:  "guard_test.go",
			base:  guardBody(""),
			gated: guardBody("\tt.Skip(\"was t.Skip(\\\"flaky\\\")\")\n"),
			want:  []string{"guard_test.go" + reported + "added a skip: t.Skip(\"was t.Skip(\\\"flaky\\\")\")"},
		},
		{
			name:  "a skip hidden after a string on the same line",
			file:  "guard_test.go",
			base:  guardBody(""),
			gated: guardBody("\treason := \"fixture\"; t.Skip(reason)\n"),
			want:  []string{"guard_test.go" + reported + "added a skip: reason := \"fixture\"; t.Skip(reason)"},
		},
		{
			name:  "skip text only inside a JavaScript string",
			file:  "c.test.ts",
			base:  "it(\"loads\", () => {})\n",
			gated: "const fixture = \"it.skip(\\\"x\\\")\"\nit(\"loads\", () => {})\n",
		},
		{
			name:  "a JavaScript skip whose name is a template",
			file:  "c.test.ts",
			base:  "it(\"loads\", () => {})\n",
			gated: "it.skip(`${name} loads`, () => {})\n",
			want:  []string{"c.test.ts" + reported + "added a skip: it.skip(`${name} loads`, () => {})"},
		},
		{
			name:  "skip text inside a Python docstring",
			file:  "test_x.py",
			base:  "def test_loads():\n    pass\n",
			gated: "def test_loads():\n    \"\"\"\n    @pytest.mark.skip\n    \"\"\"\n    pass\n",
		},
		{
			name:  "a Python skip decorator",
			file:  "test_x.py",
			base:  "def test_loads():\n    pass\n",
			gated: "@pytest.mark.skip(reason=\"flaky\")\ndef test_loads():\n    pass\n",
			want:  []string{"test_x.py" + reported + "added a skip: @pytest.mark.skip(reason=\"flaky\")"},
		},
		{
			name:  "a Pester skip in a comment",
			file:  "a.Tests.ps1",
			base:  "It \"loads\" {\n}\n",
			gated: "# It \"loads\" -Skip {\nIt \"loads\" {\n}\n",
		},
		{
			name:  "a Pester skip",
			file:  "a.Tests.ps1",
			base:  "It \"loads\" {\n}\n",
			gated: "It \"loads\" -Skip {\n}\n",
			want:  []string{"a.Tests.ps1" + reported + "added a skip: It \"loads\" -Skip {"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir, git, write := scratchRepo(t, "package x\n")
			write(test.file, test.base)
			git("add", ".")
			git("commit", "-qm", "feat: the test")
			write(test.file, test.gated)
			git("commit", "-qam", "no-mistakes(review): adjust the test")

			// Act
			result, err := Check(context.Background(), execx.OSRunner{}, dir)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, removal := range result.Removals {
				got = append(got, removal.File+": "+removal.What)
			}
			if strings.Join(got, "\n") != strings.Join(test.want, "\n") {
				t.Errorf("removals = %q, want %q", got, test.want)
			}
		})
	}
}
