package testguard

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func revParse(t *testing.T, dir, rev string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", rev).Output()
	if err != nil {
		t.Fatalf("git rev-parse %s: %v", rev, err)
	}
	return strings.TrimSpace(string(out))
}

func removalWhats(removals []Removal) []string {
	var whats []string
	for _, removal := range removals {
		whats = append(whats, removal.File+": "+removal.What)
	}
	return whats
}

// A person approved a park in which a gate commit deleted a test; the goblin
// then fixed CI in a commit of its own and gated again. The approved commit
// is not reported again, and the check says it left it out.
func TestCheckLeavesOutGateCommitsUnderAnApprovedHead(t *testing.T) {
	// Arrange
	dir, git, write := scratchRepo(t, "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n\nfunc TestGuardStands(t *testing.T) {}\n")
	write("guard_test.go", "package x\n\nfunc TestGuardStands(t *testing.T) {}\n")
	git("commit", "-qam", "no-mistakes(test): drop the failing live test")
	approved := revParse(t, dir, "HEAD")
	write("ci.go", "package x\n")
	git("add", ".")
	git("commit", "-qm", "fix: the CI failure")
	unapproved, err := Check(context.Background(), execx.OSRunner{}, dir, nil)
	if err != nil || len(unapproved.Removals) != 1 {
		t.Fatalf("premise: with no approval the deletion is reported, got %+v %v", unapproved.Removals, err)
	}

	// Act
	result, err := Check(context.Background(), execx.OSRunner{}, dir, []string{approved})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removals) != 0 || result.Commits != 0 || result.Approved != 1 {
		t.Fatalf("removals %q, %d gate commits read, %d left out; want nothing reported, none read and the approved one left out", removalWhats(result.Removals), result.Commits, result.Approved)
	}
	if result.Head != revParse(t, dir, "HEAD") {
		t.Fatalf("head = %q, want the HEAD the check read", result.Head)
	}
}

// Pushed is not approved: a parked head pinned or pushed as a backup puts
// gate commits nobody approved on remote branches, and a sibling branch's
// gate may have made the identical deletion and had it approved. Neither
// stands for an approval of this branch's commit.
func TestCheckStillReportsAGateCommitThatWasPushedButNotApproved(t *testing.T) {
	// Arrange
	dir, git, write := scratchRepo(t, "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n")
	git("switch", "-qc", "sibling", "main")
	write("guard_test.go", "package x\n")
	git("commit", "-qam", "no-mistakes(test): fix the sibling's failing live test")
	siblingApproved := revParse(t, dir, "HEAD")
	git("switch", "-q", "feature")
	write("guard_test.go", "package x\n")
	git("commit", "-qam", "no-mistakes(test): fix failing live test")
	git("update-ref", "refs/remotes/origin/feature", "HEAD")
	git("update-ref", "refs/remotes/origin/feature-wip", "HEAD")
	git("update-ref", "refs/heads/archive/feature", "HEAD")

	// Act
	result, err := Check(context.Background(), execx.OSRunner{}, dir, []string{siblingApproved})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := removalWhats(result.Removals); len(got) != 1 || got[0] != "guard_test.go: removed the test TestGuardHolds" || result.Approved != 0 {
		t.Fatalf("removals %q, %d left out; want this branch's deletion reported and nothing left out", got, result.Approved)
	}
}

// What a gate commit does after the approved head is read as before, in the
// same test file too: a later skip is counted on its own, and a later
// deletion (a CI fix, say) is reported while the approved one is not.
func TestCheckReadsGateCommitsMadeAfterTheApprovedHead(t *testing.T) {
	for _, test := range []struct {
		name   string
		base   string
		before string
		after  string
		want   []string
	}{
		{
			name:   "a later skip in the file whose skip a person approved",
			base:   guardPair("t.Log(1)", "t.Log(2)"),
			before: guardPair(`t.Skip("flaky")`, "t.Log(2)"),
			after:  guardPair(`t.Skip("flaky")`, `t.Skip("slow")`),
			want:   []string{`guard_test.go: gate commits added 1 more skip line than they removed, still at HEAD: added a skip: t.Skip("slow")`},
		},
		{
			name:   "a later deletion in the file whose deletion a person approved",
			base:   "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n\nfunc TestGuardStands(t *testing.T) {}\n",
			before: "package x\n\nfunc TestGuardStands(t *testing.T) {}\n",
			after:  "package x\n",
			want:   []string{"guard_test.go: removed the test TestGuardStands"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir, git, write := scratchRepo(t, test.base)
			write("guard_test.go", test.before)
			git("commit", "-qam", "no-mistakes(review): tolerate the flaky guard")
			approved := revParse(t, dir, "HEAD")
			write("ci.go", "package x\n")
			git("add", ".")
			git("commit", "-qm", "fix: the CI failure")
			write("guard_test.go", test.after)
			git("commit", "-qam", "no-mistakes(test): fix the next failure")

			// Act
			result, err := Check(context.Background(), execx.OSRunner{}, dir, []string{approved})

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if got := removalWhats(result.Removals); strings.Join(got, "\n") != strings.Join(test.want, "\n") || result.Commits != 1 || result.Approved != 1 {
				t.Fatalf("removals %q, %d read, %d left out; want %q from the one later commit", got, result.Commits, result.Approved, test.want)
			}
		})
	}
}

// A gate commit approved on one branch and carried into another, by a merge
// or a sync branch, was approved: the approval is of the commit.
func TestCheckLeavesOutAnApprovedCommitCarriedIntoAnotherBranch(t *testing.T) {
	// Arrange
	dir, git, write := scratchRepo(t, "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n")
	git("switch", "-qc", "first", "main")
	write("guard_test.go", "package x\n")
	git("commit", "-qam", "no-mistakes(review): drop the replaced test")
	approved := revParse(t, dir, "HEAD")
	git("switch", "-q", "feature")
	write("sync.go", "package x\n")
	git("add", ".")
	git("commit", "-qm", "feat: sync the first branch's work")
	git("merge", "-q", "--no-edit", "first")

	// Act
	unapproved, err := Check(context.Background(), execx.OSRunner{}, dir, nil)
	if err != nil || len(unapproved.Removals) != 1 {
		t.Fatalf("premise: unapproved, the carried deletion is reported, got %+v %v", unapproved.Removals, err)
	}
	result, err := Check(context.Background(), execx.OSRunner{}, dir, []string{approved})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removals) != 0 || result.Approved != 1 {
		t.Fatalf("removals %q, %d left out; want the carried approved commit left out", removalWhats(result.Removals), result.Approved)
	}
}

// An approved head from another repository's gate is not an object here, and
// changes nothing.
func TestCheckIgnoresAnApprovedHeadThisRepositoryLacks(t *testing.T) {
	// Arrange
	dir, git, write := scratchRepo(t, "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n")
	write("guard_test.go", "package x\n")
	git("commit", "-qam", "no-mistakes(test): fix failing live test")

	// Act
	result, err := Check(context.Background(), execx.OSRunner{}, dir, []string{strings.Repeat("0123456789", 4)})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removals) != 1 || result.Approved != 0 {
		t.Fatalf("removals %q, %d left out; want the deletion reported", removalWhats(result.Removals), result.Approved)
	}
}

func TestApprovedHeadsReadsOnlyTheCheckedLineAtTheTop(t *testing.T) {
	head := strings.Repeat("0123456789abcdef", 3)[:40]
	for _, test := range []struct {
		name    string
		summary string
		want    []string
	}{
		{name: "the line a park printed", summary: CheckedLine(head) + "\nA gate fix commit deleted or skipped 1 test(s)...\n", want: []string{head}},
		{name: "a Windows line ending", summary: CheckedLine(head) + "\r\n- x\r\n", want: []string{head}},
		{name: "a head named below the first line, as a commit subject could", summary: "A gate fix commit deleted or skipped 1 test(s)...\n" + CheckedLine(head) + "\n", want: nil},
		{name: "text after the head", summary: CheckedLine(head) + " and more\n", want: nil},
		{name: "a short head", summary: CheckedLine(head[:39]) + "\n", want: nil},
		{name: "an uppercase head", summary: CheckedLine(strings.ToUpper(head)) + "\n", want: nil},
		{name: "another gate's output", summary: "lint failed\n", want: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ApprovedHeads([]string{test.summary}); strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("ApprovedHeads = %q, want %q", got, test.want)
			}
		})
	}
}
