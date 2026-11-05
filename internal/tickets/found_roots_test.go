package tickets

import (
	"strings"
	"testing"
)

func TestFailedOnceRootIsKeyedByItsTestWhateverJobRanIt(t *testing.T) {
	// Arrange
	named := []string{
		"go (cmd-cfo-2): github.com/acme/widgets/internal/conpty TestInputLeftUnread",
		"go (rest): github.com/acme/widgets/internal/conpty TestInputLeftUnread",
	}

	// Act
	first, second := FailedOnceRoot(named[0], "run 1"), FailedOnceRoot(named[1], "run 2")

	// Assert
	if first.RootKey() != second.RootKey() || first.RootKey() != "test:github.com/acme/widgets/internal/conpty testinputleftunread" {
		t.Fatalf("keys = %q and %q, want one key for the test", first.RootKey(), second.RootKey())
	}
	if first.Title != "conpty TestInputLeftUnread fails by chance: it failed once and passed on its second try" {
		t.Fatalf("title = %q", first.Title)
	}
	if !strings.Contains(first.Body, "`TestInputLeftUnread` in `github.com/acme/widgets/internal/conpty` failed once") || first.Evidence != "run 1" {
		t.Fatalf("root = %+v", first)
	}
}

func TestFailedOnceRootTakesATestWithNoSuiteOrJob(t *testing.T) {
	root := FailedOnceRoot("TestAlone", "")
	if root.Title != "TestAlone fails by chance: it failed once and passed on its second try" || root.RootKey() != "test:testalone" {
		t.Fatalf("root = %+v", root)
	}
}

func TestRedMainRootIsKeyedByItsWorkflowAndJobs(t *testing.T) {
	root := RedMainRoot("go", "job go (rest)", "run 9")
	if root.RootKey() != "ci:go: job go (rest)" || root.Title != "go is red on the default branch: job go (rest)" || root.Evidence != "run 9" {
		t.Fatalf("root = %+v", root)
	}
}

func TestErrorClassTakesOutWhatChangesBetweenSightings(t *testing.T) {
	cases := map[string]string{
		`pause condition for t1: open C:\Users\x\state\t1.json: denied after 3 tries`: "pause condition for tN: open <path> denied after N tries",
		`git rev-parse "abc" at d1faafe41 exited 128`:                                 "git rev-parse <text> at <hash> exited N",
		`read /c/dev/repo/file failed`:                                                "read <path> failed",
	}
	for line, want := range cases {
		if got := ErrorClass(line, nil); got != want {
			t.Errorf("ErrorClass(%q) = %q, want %q", line, got, want)
		}
	}
	if ErrorClass("pid 4012 exited", nil) != ErrorClass("pid 977 exited", nil) {
		t.Error("two lines that differ by a number are two classes")
	}
}

// On 2026-11-02 one progress-read timeout opened an ops-root issue for each
// goblin it met, since each line named its own goblin (#1950).
func TestErrorClassTakesOutAnyTaskIDItNames(t *testing.T) {
	tasks := []string{"cg-remote-toggle", "cg-goblin-typing-and-header", "root-code-goblins-847", "toggle"}
	cases := map[string]string{
		"progress for cg-remote-toggle: context deadline exceeded":            "progress for <task>: context deadline exceeded",
		"progress for cg-goblin-typing-and-header: context deadline exceeded": "progress for <task>: context deadline exceeded",
		"progress for root-code-goblins-847: context deadline exceeded":       "progress for <task>: context deadline exceeded",
		"push progress for CG-Remote-Toggle: git for-each-ref exited 128":     "push progress for <task>: git for-each-ref exited N",
		"evaluation of cg-remote-toggle after its gate run moved: denied":     "evaluation of <task> after its gate run moved: denied",
		"the gate stopped for cg-remote-toggle.":                              "the gate stopped for <task>.",
		"progress for cg-ended-task: context deadline exceeded":               "progress for cg-ended-task: context deadline exceeded",
		"read cg-remote-toggles failed":                                       "read cg-remote-toggles failed",
	}
	for line, want := range cases {
		if got := ErrorClass(line, tasks); got != want {
			t.Errorf("ErrorClass(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestIsLostConnectionTellsAnUnreachableHostFromAnyOtherError(t *testing.T) {
	cases := map[string]bool{
		"root issues of code-goblins: gh api GET repos/acme/widgets/issues: error connecting to api.github.com": true,
		"check your internet connection or https://githubstatus.com":                                            true,
		"fatal: unable to access 'https://github.com/acme/widgets/': Could not resolve host: github.com":        true,
		"fatal: unable to access 'https://github.com/acme/widgets/': Failed to connect to github.com port 443":  true,
		"Get \"https://api.github.com\": dial tcp: lookup api.github.com: no such host":                         true,
		"dial tcp 140.82.112.6:443: connect: network is unreachable":                                            true,
		"gh api GET repos/acme/widgets/issues: Bad credentials (HTTP 401)":                                      false,
		"error connecting to the board: herdr exited 1":                                                         false,
		"pause condition for t1: open state/t1.json: denied after 3 tries":                                      false,
		"": false,
	}
	for line, want := range cases {
		if got := IsLostConnection(line); got != want {
			t.Errorf("IsLostConnection(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestRepeatedErrorRootCutsALongClassInItsTitleOnly(t *testing.T) {
	class := strings.Repeat("word ", 30)
	root := RepeatedErrorRoot(strings.TrimSpace(class), "")
	if len(root.Title) > len("The supervisor meets the same error again: ")+83 || !strings.HasSuffix(root.Title, "...") {
		t.Fatalf("title = %q", root.Title)
	}
	if root.Key != "error:"+strings.TrimSpace(class) || !strings.Contains(root.Body, strings.TrimSpace(class)) {
		t.Fatalf("root = %+v", root)
	}
}
