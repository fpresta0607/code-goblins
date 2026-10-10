package train

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// onceAnswer is GitHub's answer to the read of the tests that failed once
// for a commit with one workflow run: a check carrying the runner's own
// notice, and a check carrying a warning titled Failed once for each test
// given.
func onceAnswer(tests ...string) string {
	var warnings []string
	for _, test := range tests {
		warnings = append(warnings, fmt.Sprintf(`{"title":"Failed once","message":%q}`, test+" failed once and passed on the second try"))
	}
	return `{"data":{"repository":{"object":{"checkSuites":{"nodes":[{"workflowRun":{"databaseId":101},"checkRuns":{"nodes":[` +
		`{"annotations":{"nodes":[{"title":"","message":"Node.js 20 is deprecated."}]}},{"annotations":{"nodes":[` + strings.Join(warnings, ",") + `]}}]}}]}}}}}`
}

// landsTwo starts a train of two pull requests whose run passes and takes
// its one step.
func landsTwo(t *testing.T, arrange func(*fakeGitHub)) (*scratch, *fakeGitHub, Train) {
	t.Helper()
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	arrange(gh)
	riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	return s, gh, s.step(engine, started.ID)
}

// A test that fails and passes on its second try inside its job leaves its
// check green, so the train's own second try of a red run never sees it. The
// train that lands on such a run names each test in its record and in its
// message to the CFO, as the workflow named it, and names no other warning.
func TestATrainThatLandsNamesTheTestsThatFailedOnceInsideTheirJob(t *testing.T) {
	t.Parallel()
	// Arrange
	tests := []string{"go (rest): example.test/internal/train TestTrainLands", "browser (2/4): afk.spec.ts the switch stays on"}

	// Act
	s, gh, landed := landsTwo(t, func(gh *fakeGitHub) { gh.once = onceAnswer(tests...) })

	// Assert
	if got := carStates(landed); landed.State != StateLanded || !slices.Equal(got, []string{"#11=landed", "#12=landed"}) {
		t.Fatalf("train %s, cars %v, want both landed", landed.State, got)
	}
	if run := landed.History[0]; !slices.Equal(run.OnceTests, tests) || run.OnceUnread != "" || len(run.FailedOnce) != 0 {
		t.Errorf("run %+v, want the two tests that failed once and no check that failed", run)
	}
	last := s.cfo[len(s.cfo)-1]
	want := "tests that failed once and passed on the second try inside their job: " + strings.Join(tests, ", ") + " in run 1"
	if !strings.Contains(last, "landed #11, #12") || !strings.Contains(last, want) || strings.Contains(last, "Node.js") {
		t.Errorf("CFO told %q, want the landing, %q and no other warning", last, want)
	}
	if gh.onceReads != 1 {
		t.Errorf("the tests that failed once were read %d times, want once, as the run passed", gh.onceReads)
	}
}

// With no test that failed once the train says nothing of them.
func TestATrainThatLandsWithNoTestThatFailedOnceSaysNothingOfThem(t *testing.T) {
	t.Parallel()
	// Act
	s, _, landed := landsTwo(t, func(*fakeGitHub) {})

	// Assert
	last := s.cfo[len(s.cfo)-1]
	if landed.State != StateLanded || !strings.Contains(last, "landed #11, #12") || strings.Contains(last, "failed once") {
		t.Errorf("train %s told the CFO %q, want the landing and no word of tests that failed once", landed.State, last)
	}
	if run := landed.History[0]; len(run.OnceTests) != 0 || run.OnceUnread != "" {
		t.Errorf("run %+v, want no test that failed once and nothing unread", run)
	}
}

// A read that fails never stops a landing, since what the run proved does
// not wait on it, and it is said, never taken for none: a read that stopped
// seeing would otherwise tell the CFO that nothing failed by chance.
func TestATrainLandsAndSaysSoWhenTheTestsThatFailedOnceCannotBeRead(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		arrange func(*fakeGitHub)
		want    string
	}{
		"gh fails":                       {func(gh *fakeGitHub) { gh.onceFailure = "HTTP 502: Bad Gateway" }, "gh exited 1: HTTP 502: Bad Gateway"},
		"GitHub lists no workflow check": {func(gh *fakeGitHub) { gh.once = `{"data":{"repository":{"object":{"checkSuites":{"nodes":[]}}}}}` }, "GitHub listed no workflow check for "},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Act
			s, _, landed := landsTwo(t, test.arrange)

			// Assert
			if got := carStates(landed); landed.State != StateLanded || !slices.Equal(got, []string{"#11=landed", "#12=landed"}) {
				t.Fatalf("train %s, cars %v, want both landed whatever became of the read", landed.State, got)
			}
			last := s.cfo[len(s.cfo)-1]
			if want := "the tests that failed once could not be read: " + test.want; !strings.Contains(last, want) || !strings.Contains(last, " in run 1") {
				t.Errorf("CFO told %q, want %q with its run", last, want)
			}
			if run := landed.History[0]; !strings.Contains(run.OnceUnread, test.want) || len(run.OnceTests) != 0 {
				t.Errorf("run %+v, want why the read failed kept, and no test", run)
			}
		})
	}
}
