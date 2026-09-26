package testguard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestScanFindsDeletedAndSkippedTests(t *testing.T) {
	cases := []struct {
		name string
		diff string
		want []string
	}{
		{
			name: "a Go test removed",
			diff: "diff --git a/internal/a_test.go b/internal/a_test.go\n--- a/internal/a_test.go\n+++ b/internal/a_test.go\n@@ -10,4 +9,0 @@\n-func TestKeepsSecrets(t *testing.T) {\n-\tcheck(t)\n-}\n",
			want: []string{"internal/a_test.go: removed the test TestKeepsSecrets"},
		},
		{
			name: "the whole test file deleted",
			diff: "diff --git a/app/tests/test_macros.py b/app/tests/test_macros.py\ndeleted file mode 100644\n--- a/app/tests/test_macros.py\n+++ /dev/null\n@@ -1,3 +0,0 @@\n-def test_document_macros_are_disabled_during_real_index_update():\n-    assert disabled()\n",
			want: []string{"app/tests/test_macros.py: deleted the test file"},
		},
		{
			name: "a skip added",
			diff: "diff --git a/src/board.test.ts b/src/board.test.ts\n--- a/src/board.test.ts\n+++ b/src/board.test.ts\n@@ -3 +3 @@\n-it(\"shows the card\", () => {\n+it.skip(\"shows the card\", () => {\n",
			want: []string{`src/board.test.ts: added a skip: it.skip("shows the card", () => {`},
		},
		{
			name: "a Go skip added",
			diff: "diff --git a/x_test.go b/x_test.go\n--- a/x_test.go\n+++ b/x_test.go\n@@ -5,0 +6 @@\n+\tt.Skip(\"flaky under load\")\n",
			want: []string{`x_test.go: added a skip: t.Skip("flaky under load")`},
		},
		{
			name: "a test moved between files is not a removal",
			diff: "diff --git a/a_test.go b/a_test.go\n--- a/a_test.go\n+++ b/a_test.go\n@@ -1 +0,0 @@\n-func TestMoved(t *testing.T) {}\ndiff --git a/b_test.go b/b_test.go\n--- a/b_test.go\n+++ b/b_test.go\n@@ -0,0 +1 @@\n+func TestMoved(t *testing.T) {}\n",
			want: nil,
		},
		{
			name: "a test description reworded in place is a rename",
			diff: "diff --git a/src/boardUpdate.test.ts b/src/boardUpdate.test.ts\n--- a/src/boardUpdate.test.ts\n+++ b/src/boardUpdate.test.ts\n@@ -9 +9 @@\n-test(\"reloads only while hidden and idle\", () => {\n+test(\"reloads only while hidden, idle and live\", () => {\n",
			want: nil,
		},
		{
			name: "a Go test renamed in place is a rename",
			diff: "diff --git a/guard_test.go b/guard_test.go\n--- a/guard_test.go\n+++ b/guard_test.go\n@@ -4 +4 @@\n-func TestGuardRefusesTheCFO(t *testing.T) {\n+func TestGuardRefusesTheRegisteredCFO(t *testing.T) {\n",
			want: nil,
		},
		{
			// PrecisionDocs, 2026-09-26: a review fix renamed one test, and in
			// another file dropped a test while adding an unrelated one.
			name: "a deleted test swapped for an unrelated one is still a deletion",
			diff: "diff --git a/app/tests/test_openrouter_usage.py b/app/tests/test_openrouter_usage.py\n--- a/app/tests/test_openrouter_usage.py\n+++ b/app/tests/test_openrouter_usage.py\n@@ -40 +40 @@\n-def test_application_identity_exception_is_exclusive_to_images(endpoint):\n+def test_application_identity_exception_is_exclusive_to_attempt_keyed_endpoints(endpoint):\ndiff --git a/app/tests/test_v1_route.py b/app/tests/test_v1_route.py\n--- a/app/tests/test_v1_route.py\n+++ b/app/tests/test_v1_route.py\n@@ -90,0 +91 @@\n+async def test_an_unrecordable_cost_still_returns_the_answer(monkeypatch, ledger):\n@@ -120 +120,0 @@\n-def test_the_app_image_ships_the_intents_file_the_router_reads():\n",
			want: []string{"app/tests/test_v1_route.py: removed the test test_the_app_image_ships_the_intents_file_the_router_reads"},
		},
		{
			name: "a removed function outside a test file",
			diff: "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +0,0 @@\n-func TestLooking(x int) {}\n",
			want: nil,
		},
		{
			name: "a skip removed is a test restored",
			diff: "diff --git a/test_x.py b/test_x.py\n--- a/test_x.py\n+++ b/test_x.py\n@@ -1 +0,0 @@\n-@pytest.mark.skip(reason=\"later\")\n",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, finding := range Scan(tc.diff) {
				got = append(got, finding.File+": "+finding.What)
			}
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("Scan = %q, want %q", got, tc.want)
			}
		})
	}
}

// The PrecisionDocs #1272 shape end to end: a gate's test step deletes a
// failing test in a commit of its own. The goblin's own deletion on the same
// branch is its author's call and is not reported.
func TestCheckReportsOnlyTheGatesOwnDeletions(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "--initial-branch=main")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "t")
	write("guard_test.go", "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n\nfunc TestOldBehaviour(t *testing.T) {}\n")
	git("add", ".")
	git("commit", "-qm", "base")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("switch", "-qc", "feature")
	write("guard_test.go", "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n")
	git("commit", "-qam", "feat: retire the old behaviour and its test")
	write("guard_test.go", "package x\n")
	git("commit", "-qam", "no-mistakes(test): fix failing live test")

	result, err := Check(context.Background(), execx.OSRunner{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Commits != 1 {
		t.Errorf("gate commits read = %d, want the one no-mistakes commit", result.Commits)
	}
	if len(result.Removals) != 1 || result.Removals[0].What != "removed the test TestGuardHolds" || !strings.HasPrefix(result.Removals[0].Subject, "no-mistakes(test)") {
		t.Fatalf("removals = %+v, want only the gate's deletion of TestGuardHolds", result.Removals)
	}

	// The gate's fix turn puts the test back in a commit of its own, and the
	// check must then pass, or the gate could never be satisfied.
	write("guard_test.go", "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n")
	git("commit", "-qam", "no-mistakes(gate): restore the deleted test")
	restored, err := Check(context.Background(), execx.OSRunner{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Removals) != 0 || restored.Commits != 2 {
		t.Fatalf("after the restore: %d gate commits, removals %+v; want both read and nothing standing", restored.Commits, restored.Removals)
	}
}
