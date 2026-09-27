package testguard

import (
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

const guardWithoutSkip = "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n"

func guardSkipping(skip string) string {
	return "package x\n\nfunc TestGuardHolds(t *testing.T) {\n\t" + skip + "\n}\n"
}

// guardPair is a test file with two tests far enough apart that a change to
// one and a change to the other merge cleanly.
func guardPair(holds, stands string) string {
	return "package x\n\nfunc TestGuardHolds(t *testing.T) {\n\t" + holds + "\n}\n\nfunc TestGuardStands(t *testing.T) {\n\t" + stands + "\n}\n"
}

// skipStep is one commit on the branch: files written, then a test file
// moved, under a subject that says whose commit it is. A step with main
// instead commits those files on main and merges main into the branch.
type skipStep struct {
	write   map[string]string
	move    [2]string
	main    map[string]string
	subject string
}

// The adversary is the gate's own fixer. In the gate test step's run of
// 2026-09-27 a gate commit added a skip and a later gate commit reworded it,
// and the check passed because it looked for the added line itself at HEAD;
// moving the test file is the next way round. Skips are counted instead, from
// each gate commit's own diff: per test file, the lines that skip or narrow
// tests it added minus those it removed, carried along a rename. A file whose
// total is positive is reported only while HEAD still has skip lines gate
// commits added in it. Merges
// and the goblin's commits are never read, so main's changes never count.
func TestCheckCountsTheSkipsGateCommitsLeaveInEachTestFile(t *testing.T) {
	for _, test := range []struct {
		name  string
		base  string
		steps []skipStep
		want  []string
	}{
		{
			name:  "a skip a gate commit added",
			base:  guardWithoutSkip,
			steps: []skipStep{{write: map[string]string{"guard_test.go": guardSkipping(`t.Skip("flaky")`)}, subject: "no-mistakes(test): tolerate the flaky guard"}},
			want:  []string{`guard_test.go: gate commits added 1 more skip line than they removed, still at HEAD: added a skip: t.Skip("flaky")`},
		},
		{
			name: "a skip a gate commit added and a later gate commit reworded",
			base: guardWithoutSkip,
			steps: []skipStep{
				{write: map[string]string{"guard_test.go": guardSkipping(`t.Skip("flaky")`)}, subject: "no-mistakes(review): tolerate the flaky guard"},
				{write: map[string]string{"guard_test.go": guardSkipping(`t.Skipf("the volume has no %s", "name")`)}, subject: "no-mistakes(gate.lint.tests-kept): reword the skip"},
			},
			want: []string{`guard_test.go: gate commits added 1 more skip line than they removed, still at HEAD: added a skip: t.Skipf("the volume has no %s", "name")`},
		},
		{
			name: "a skip a gate commit added and a later gate commit moved with its file",
			base: guardWithoutSkip,
			steps: []skipStep{
				{write: map[string]string{"guard_test.go": guardSkipping(`t.Skip("flaky")`)}, subject: "no-mistakes(review): tolerate the flaky guard"},
				{move: [2]string{"guard_test.go", "holds_test.go"}, subject: "no-mistakes(review): name the file after its test"},
			},
			want: []string{`holds_test.go: gate commits added 1 more skip line than they removed, still at HEAD: added a skip: t.Skip("flaky")`},
		},
		{
			name:  "a skip the branch already had, reworded by a gate commit",
			base:  guardSkipping(`t.Skip("flaky")`),
			steps: []skipStep{{write: map[string]string{"guard_test.go": guardSkipping(`t.Skip("flaky under load")`)}, subject: "no-mistakes(review): say why it is skipped"}},
			want:  nil,
		},
		{
			name: "a goblin's own skip before the gate commits",
			base: guardWithoutSkip,
			steps: []skipStep{
				{write: map[string]string{"guard_test.go": guardSkipping(`t.Skip("mine")`)}, subject: "feat: skip my own flaky guard"},
				{write: map[string]string{"guard_test.go": guardSkipping(`t.Skip("mine, until the fix lands")`)}, subject: "no-mistakes(review): say why it is skipped"},
			},
			want: nil,
		},
		{
			name: "a skip a gate commit added and the goblin removed",
			base: guardWithoutSkip,
			steps: []skipStep{
				{write: map[string]string{"guard_test.go": guardSkipping(`t.Skip("flaky")`)}, subject: "no-mistakes(test): tolerate the flaky guard"},
				{write: map[string]string{"guard_test.go": guardWithoutSkip}, subject: "fix: make the guard deterministic"},
			},
			want: nil,
		},
		{
			name: "a skip a gate commit added and the goblin removed, beside a skip the branch already had",
			base: guardPair(`t.Log("holds")`, `t.Skip("parked")`),
			steps: []skipStep{
				{write: map[string]string{"guard_test.go": guardPair(`t.Skip("flaky")`, `t.Skip("parked")`)}, subject: "no-mistakes(test): tolerate the flaky guard"},
				{write: map[string]string{"guard_test.go": guardPair(`t.Log("holds")`, `t.Skip("parked")`)}, subject: "fix: make the guard deterministic"},
			},
			want: nil,
		},
		{
			name: "a skip added beside an edited one",
			base: "package x\n",
			steps: []skipStep{
				{write: map[string]string{"b.test.ts": "it.skip('x',()=>{})\n"}, subject: "feat: park x"},
				{write: map[string]string{"b.test.ts": "it.skip(\"x\", () => {})\nit.skip(\"y\", () => {})\n"}, subject: "no-mistakes(lint): format the tests"},
			},
			want: []string{`b.test.ts: gate commits added 1 more skip line than they removed, still at HEAD: added a skip: it.skip("x", () => {}); added a skip: it.skip("y", () => {})`},
		},
		{
			name: "a skip added in the same hunk as a reworded existing one",
			base: "package x\n",
			steps: []skipStep{
				{write: map[string]string{"d.test.ts": "it(\"saves\", () => {})\nit.skip(\"loads\", () => {})\n"}, subject: "feat: cover saving"},
				{write: map[string]string{"d.test.ts": "it.skip(\"saves\", () => {})\nit.skip(\"loads, flaky\", () => {})\n"}, subject: "no-mistakes(test): park saving"},
			},
			want: []string{`d.test.ts: gate commits added 1 more skip line than they removed, still at HEAD: added a skip: it.skip("saves", () => {}); added a skip: it.skip("loads, flaky", () => {})`},
		},
		{
			name: "an only",
			base: "package x\n",
			steps: []skipStep{
				{write: map[string]string{"c.test.ts": "it(\"loads\", () => {})\nit(\"saves\", () => {})\n"}, subject: "feat: cover loading"},
				{write: map[string]string{"c.test.ts": "it.only(\"loads\", () => {})\nit(\"saves\", () => {})\n"}, subject: "no-mistakes(test): focus the failing case"},
			},
			want: []string{`c.test.ts: gate commits added 1 more skip line than they removed, still at HEAD: focused the file on one test, which skips every other test in it: it.only("loads", () => {})`},
		},
		{
			name: "a skip main added to a file a gate commit touched, merged in",
			base: guardPair(`t.Log("holds")`, `t.Log("stands")`),
			steps: []skipStep{
				{write: map[string]string{"guard_test.go": guardPair(`t.Log("holds, checked")`, `t.Log("stands")`)}, subject: "no-mistakes(review): say what holds"},
				{main: map[string]string{"guard_test.go": guardPair(`t.Log("holds")`, `t.Skip("parked on main")`)}, subject: "chore: park the stands test"},
			},
			want: nil,
		},
		{
			name: "a skip a gate commit added after main removed an existing one, merged in",
			base: guardPair(`t.Log("holds")`, `t.Skip("parked")`),
			steps: []skipStep{
				{write: map[string]string{"guard_test.go": guardPair(`t.Skip("flaky")`, `t.Skip("parked")`)}, subject: "no-mistakes(test): tolerate the flaky guard"},
				{main: map[string]string{"guard_test.go": guardPair(`t.Log("holds")`, `t.Log("stands")`)}, subject: "fix: unpark the stands test"},
			},
			want: []string{`guard_test.go: gate commits added 1 more skip line than they removed, still at HEAD: added a skip: t.Skip("flaky")`},
		},
		{
			name:  "a skip a gate commit added to a test file with a non-ASCII name",
			base:  guardWithoutSkip,
			steps: []skipStep{{write: map[string]string{"guärd_test.go": guardSkipping(`t.Skip("flaky")`)}, subject: "no-mistakes(test): tolerate the flaky guard"}},
			want:  []string{"guärd_test.go: gate commits added 1 more skip line than they removed, still at HEAD: added a skip: t.Skip(\"flaky\")"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir, git, write := scratchRepo(t, test.base)
			for _, step := range test.steps {
				if step.main != nil {
					git("switch", "-q", "main")
					for name, content := range step.main {
						write(name, content)
					}
					git("commit", "-qam", step.subject)
					git("update-ref", "refs/remotes/origin/main", "HEAD")
					git("switch", "-q", "feature")
					git("merge", "-q", "--no-edit", "main")
					continue
				}
				for name, content := range step.write {
					write(name, content)
				}
				if step.move[0] != "" {
					git("mv", step.move[0], step.move[1])
				}
				git("add", ".")
				git("commit", "-qm", step.subject)
			}

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
