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
			name: "a test file renamed out of the runner's pattern is deleted",
			diff: "diff --git a/x_test.go b/x_test.go.disabled\nsimilarity index 100%\nrename from x_test.go\nrename to x_test.go.disabled\n",
			want: []string{"x_test.go: deleted the test file, renaming it to x_test.go.disabled"},
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
		{
			name: "tests that share only an acronym are not a rename",
			diff: "diff --git a/pr_test.go b/pr_test.go\n--- a/pr_test.go\n+++ b/pr_test.go\n@@ -4 +4 @@\n-func TestPRMergeNeverForwardsDeleteBranchToGH(t *testing.T) {\n+func TestPRMergeDeletesNothingWhenTheMergeFails(t *testing.T) {\n",
			want: []string{"pr_test.go: removed the test TestPRMergeNeverForwardsDeleteBranchToGH"},
		},
		{
			name: "a JavaScript name holding an apostrophe is read whole",
			diff: "diff --git a/src/a.test.ts b/src/a.test.ts\n--- a/src/a.test.ts\n+++ b/src/a.test.ts\n@@ -3 +2,0 @@\n-it(\"doesn't crash on an empty board\", () => {\n",
			want: []string{"src/a.test.ts: removed the test doesn't crash on an empty board"},
		},
		{
			name: "a Pester name holding an apostrophe is read whole",
			diff: "diff --git a/a.Tests.ps1 b/a.Tests.ps1\n--- a/a.Tests.ps1\n+++ b/a.Tests.ps1\n@@ -3 +2,0 @@\n-    It \"doesn't write outside the worktree\" {\n",
			want: []string{"a.Tests.ps1: removed the test doesn't write outside the worktree"},
		},
		{
			name: "a skip reformatted in place is not a new skip",
			diff: "diff --git a/src/b.test.ts b/src/b.test.ts\n--- a/src/b.test.ts\n+++ b/src/b.test.ts\n@@ -3 +3 @@\n-it.skip('x',()=>{\n+it.skip(\"x\", () => {\ndiff --git a/test_y.py b/test_y.py\n--- a/test_y.py\n+++ b/test_y.py\n@@ -1 +1 @@\n-@pytest.mark.skipif(sys.platform == \"win32\", reason=\"posix\")\n+@pytest.mark.skipif(os.name == \"nt\", reason=\"posix\")\n",
			want: nil,
		},
		{
			name: "a skip added beside an edited one is still a new skip",
			diff: "diff --git a/src/b.test.ts b/src/b.test.ts\n--- a/src/b.test.ts\n+++ b/src/b.test.ts\n@@ -3 +3,2 @@\n-it.skip('x',()=>{\n+it.skip(\"x\", () => {\n+it.skip(\"y\", () => {\n",
			want: []string{`src/b.test.ts: added a skip: it.skip("y", () => {`},
		},
		{
			name: "an only is reported as focusing its file",
			diff: "diff --git a/src/c.test.ts b/src/c.test.ts\n--- a/src/c.test.ts\n+++ b/src/c.test.ts\n@@ -3 +3 @@\n-it(\"loads\", () => {\n+it.only(\"loads\", () => {\n",
			want: []string{`src/c.test.ts: focused the file on one test, which skips every other test in it: it.only("loads", () => {`},
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

// scratchRepo is a repository whose main holds guard_test.go with the given
// content, recorded as origin/main, and a feature branch checked out off it.
func scratchRepo(t *testing.T, content string) (string, func(args ...string), func(name, content string)) {
	t.Helper()
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
	write("guard_test.go", content)
	git("add", ".")
	git("commit", "-qm", "base")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("switch", "-qc", "feature")
	return dir, git, write
}

// The PrecisionDocs #1272 shape end to end: a gate's test step deletes a
// failing test in a commit of its own. The goblin's own deletion on the same
// branch is its author's call and is not reported.
func TestCheckReportsOnlyTheGatesOwnDeletions(t *testing.T) {
	dir, git, write := scratchRepo(t, "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n\nfunc TestOldBehaviour(t *testing.T) {}\n")
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

func TestCheckReportsARemovedTestWhoseNameAnotherTestExtends(t *testing.T) {
	dir, git, write := scratchRepo(t, "package x\n\nfunc TestParse(t *testing.T) {}\n\nfunc TestParseFails(t *testing.T) {}\n")
	write("guard_test.go", "package x\n\nfunc TestParseFails(t *testing.T) {}\n")
	git("commit", "-qam", "no-mistakes(test): fix failing parse test")

	result, err := Check(context.Background(), execx.OSRunner{}, dir)

	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removals) != 1 || result.Removals[0].What != "removed the test TestParse" {
		t.Fatalf("removals = %+v, want the deletion of TestParse, which TestParseFails does not stand in for", result.Removals)
	}
}

// A deletion the CFO approved was pushed, then the branch was rebased onto a
// newer main for another run. The approved deletion's rebased copy is already
// on origin and must not park again; a new unpushed deletion still must.
func TestCheckSkipsGateCommitsAlreadyPushed(t *testing.T) {
	dir, git, write := scratchRepo(t, "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n\nfunc TestGuardWarns(t *testing.T) {}\n")
	write("guard_test.go", "package x\n\nfunc TestGuardWarns(t *testing.T) {}\n")
	git("commit", "-qam", "no-mistakes(test): drop the flaky guard test")
	git("update-ref", "refs/remotes/origin/feature", "HEAD")
	git("switch", "-q", "main")
	write("other.go", "package x\n")
	git("add", ".")
	git("commit", "-qm", "main moves on")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("switch", "-q", "feature")
	git("rebase", "-q", "main")
	write("guard_test.go", "package x\n")
	git("commit", "-qam", "no-mistakes(review): drop the warning test")

	result, err := Check(context.Background(), execx.OSRunner{}, dir)

	if err != nil {
		t.Fatal(err)
	}
	if result.Commits != 1 || len(result.Removals) != 1 || result.Removals[0].What != "removed the test TestGuardWarns" {
		t.Fatalf("read %d gate commit(s), removals %+v; want only the unpushed deletion of TestGuardWarns", result.Commits, result.Removals)
	}
}

func TestCheckReportsARemovedTestWhoseNameAnotherTestsNameContains(t *testing.T) {
	dir, git, write := scratchRepo(t, "package x\n")
	write("a.test.ts", "it(\"returns 404\", () => {})\n")
	write("b.test.ts", "it(\"returns 404 for a missing project\", () => {})\n")
	git("add", ".")
	git("commit", "-qm", "feat: cover the missing route")
	write("a.test.ts", "\n")
	git("commit", "-qam", "no-mistakes(test): fix the failing route test")

	result, err := Check(context.Background(), execx.OSRunner{}, dir)

	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removals) != 1 || result.Removals[0].What != "removed the test returns 404" {
		t.Fatalf("removals = %+v, want the deletion of \"returns 404\", which a longer test name does not stand in for", result.Removals)
	}
}

// The deletion approved and pushed on one branch is not approved on another:
// the same gate edit on a sibling branch, checked from the detached HEAD a
// no-mistakes run worktree has, still parks.
func TestCheckParksTheSameDeletionOnASiblingBranch(t *testing.T) {
	dir, git, write := scratchRepo(t, "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n\nfunc TestGuardWarns(t *testing.T) {}\n")
	write("guard_test.go", "package x\n\nfunc TestGuardWarns(t *testing.T) {}\n")
	git("commit", "-qam", "no-mistakes(test): drop the flaky guard test")
	git("update-ref", "refs/remotes/origin/feature", "HEAD")
	git("switch", "-qc", "sibling", "main")
	write("other.go", "package x\n")
	git("add", ".")
	git("commit", "-qm", "feat: add other")
	git("update-ref", "refs/remotes/origin/sibling", "HEAD")
	write("guard_test.go", "package x\n\nfunc TestGuardWarns(t *testing.T) {}\n")
	git("commit", "-qam", "no-mistakes(test): drop the flaky guard test")
	git("switch", "-q", "--detach")

	result, err := Check(context.Background(), execx.OSRunner{}, dir)

	if err != nil {
		t.Fatal(err)
	}
	if result.Commits != 1 || len(result.Removals) != 1 || result.Removals[0].What != "removed the test TestGuardHolds" {
		t.Fatalf("read %d gate commit(s), removals %+v; want the sibling's own deletion of TestGuardHolds", result.Commits, result.Removals)
	}

	git("switch", "-q", "--detach", "feature")
	pushed, err := Check(context.Background(), execx.OSRunner{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if pushed.Commits != 0 || len(pushed.Removals) != 0 {
		t.Fatalf("detached at the pushed feature branch: read %d gate commit(s), removals %+v; want its pushed deletion skipped", pushed.Commits, pushed.Removals)
	}
}
