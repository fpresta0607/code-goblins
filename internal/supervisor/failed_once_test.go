package supervisor

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// onceSuite is the checks of one workflow run as GitHub answers the read of
// the tests that failed once: one check carrying the runner's own notice,
// and a check carrying each warning given, as title and text. run 0 is a
// suite no workflow run stands behind.
func onceSuite(run int64, warnings ...[2]string) string {
	behind := "null"
	if run != 0 {
		behind = fmt.Sprintf(`{"databaseId":%d}`, run)
	}
	var nodes []string
	for _, warning := range warnings {
		nodes = append(nodes, fmt.Sprintf(`{"title":%q,"message":%q}`, warning[0], warning[1]))
	}
	return fmt.Sprintf(`{"workflowRun":%s,"checkRuns":{"nodes":[{"annotations":{"nodes":[{"title":"","message":"Node.js 20 is deprecated."}]}},{"annotations":{"nodes":[%s]}}]}}`, behind, strings.Join(nodes, ","))
}

// onceAnswer is GitHub's answer holding the suites.
func onceAnswer(suites ...string) string {
	return `{"data":{"repository":{"object":{"checkSuites":{"nodes":[` + strings.Join(suites, ",") + `]}}}}}`
}

// A test that fails and passes on its second try leaves its run green, so
// the wake for a pull request whose checks all passed is where the CFO
// learns of it: it names each such test as its workflow named it, whether
// the warning says what happened in full or only names the test, and names
// no other warning. The tests are read once, when the wake is raised, and
// not on every poll.
func TestCIFinishedNamesEachTestThatFailedOnceOnAPullRequest(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	project := t.TempDir()
	runsWorkflows(t, project)
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs = "feat/wakes", "[]"
	forge.pulls = rollupWith("3d7072f8aa", "MERGEABLE", goTestPassed, scanPassedRun)
	forge.once = onceAnswer(onceSuite(0), onceSuite(41,
		[2]string{"Failed once", "go (rest): example.test/internal/train TestTrainLands failed once and passed on the second try"},
		[2]string{"Failed once", "browser (2/4): afk.spec.ts the switch stays on"},
		[2]string{"", "The ubuntu-latest label will migrate."}))
	s.Options.CI = forge
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	// Act
	passed := pollForge(t, s, h, &now)
	again := pollForge(t, s, h, &now)

	// Assert
	if len(passed) != 1 || len(again) != 0 {
		t.Fatalf("the finished checks woke the CFO %d times and then %d, want once: %+v", len(passed), len(again), passed)
	}
	want := "all 2 passed, and 2 failed once and passed on the second try (go (rest): example.test/internal/train TestTrainLands, browser (2/4): afk.spec.ts the switch stays on); next:"
	if !strings.Contains(passed[0].Detail, want) {
		t.Errorf("wake %q lacks %q", passed[0].Detail, want)
	}
	for _, other := range []string{"Node.js", "ubuntu-latest"} {
		if strings.Contains(passed[0].Detail, other) {
			t.Errorf("wake %q names a warning that is no test: %q", passed[0].Detail, other)
		}
	}
	if forge.onceCalls != 1 {
		t.Errorf("the tests that failed once were read %d times over two polls, want once, when the wake was raised", forge.onceCalls)
	}
}

// A pull request whose checks failed is told of the tests that failed once
// too: they are not what failed it, and they are still to be counted.
func TestARedPullRequestsWakeNamesTheTestsThatFailedOnceToo(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	project := t.TempDir()
	runsWorkflows(t, project)
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs = "feat/wakes", "[]"
	forge.pulls = rollupWith("3d7072f8aa", "MERGEABLE", strings.Replace(goTestPassed, "SUCCESS", "FAILURE", 1), scanPassedRun)
	forge.once = onceAnswer(onceSuite(41, [2]string{"Failed once", "go (conpty): example.test/internal/conpty TestKeys failed once and passed on the second try"}))
	s.Options.CI = forge
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	// Act
	failed := pollForge(t, s, h, &now)

	// Assert
	want := "1 of 2 failed (test), and 1 failed once and passed on the second try (go (conpty): example.test/internal/conpty TestKeys); next: tell cg-wakes"
	if len(failed) != 1 || !strings.Contains(failed[0].Detail, want) {
		t.Fatalf("the failed checks = %+v, want one wake holding %q", failed, want)
	}
}

// With no test that failed once the wake says nothing of them, and a read
// that could not be made is said, never passed over as none: a read that
// stopped seeing would otherwise tell the CFO that nothing failed by chance.
func TestCIFinishedSaysWhenTheTestsThatFailedOnceCouldNotBeRead(t *testing.T) {
	for name, test := range map[string]struct {
		once, failure string
		want          string
	}{
		"no test failed once":            {"", "", "all 2 passed; next:"},
		"gh fails":                       {"", "HTTP 502: Bad Gateway", "all 2 passed, and the tests that failed once could not be read (gh exited 1: HTTP 502: Bad Gateway); next:"},
		"GitHub answers an error":        {`{"errors":[{"message":"Something went wrong"}]}`, "", "all 2 passed, and the tests that failed once could not be read (GitHub answered Something went wrong); next:"},
		"GitHub lists no workflow check": {onceAnswer(), "", "all 2 passed, and the tests that failed once could not be read (GitHub listed no workflow check for 3d7072f); next:"},
		"an answer that cannot be read":  {`<html>`, "", "all 2 passed, and the tests that failed once could not be read (gh answered in a shape that cannot be read"},
		"an answer with no commit in it": {`{"data":{"repository":{"object":null}}}`, "", "all 2 passed, and the tests that failed once could not be read (GitHub listed no workflow check for 3d7072f); next:"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, h := fleetService(t)
			project := t.TempDir()
			runsWorkflows(t, project)
			liveGoblin(t, h, "cg-wakes", project)
			forge := forgeFor(project, "cg-wakes")
			forge.branch, forge.runs = "feat/wakes", "[]"
			forge.pulls = rollupWith("3d7072f8aa", "MERGEABLE", goTestPassed, scanPassedRun)
			forge.once, forge.onceFailure = test.once, test.failure
			s.Options.CI = forge
			now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

			// Act
			passed := pollForge(t, s, h, &now)

			// Assert
			if len(passed) != 1 || !strings.Contains(passed[0].Detail, test.want) {
				t.Fatalf("the finished checks = %+v, want one wake holding %q", passed, test.want)
			}
			if test.once == "" && test.failure == "" && strings.Contains(passed[0].Detail, "failed once") {
				t.Errorf("wake %q speaks of tests that failed once when none did", passed[0].Detail)
			}
		})
	}
}

// A pull request with no workflow check has no workflow that could have
// given a test a second try, so nothing is read for it.
func TestCIFinishedReadsNoTestThatFailedOnceWhereNoWorkflowReported(t *testing.T) {
	// Arrange: checks from other apps alone, in a repository that runs no
	// workflow on its pull requests.
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs, forge.pulls = "feat/wakes", "[]", passedChecks
	forge.onceFailure = "must not be asked"
	s.Options.CI = forge
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	// Act
	passed := pollForge(t, s, h, &now)

	// Assert
	if len(passed) != 1 || !strings.Contains(passed[0].Detail, "all 3 passed; next:") || forge.onceCalls != 0 {
		t.Fatalf("the finished checks = %+v after %d reads, want one wake that read nothing", passed, forge.onceCalls)
	}
}

// The wake for main's red run names the tests that failed once in that run,
// and none of another run of the same commit.
func TestMainsRedRunNamesTheTestsThatFailedOnceInThatRun(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.pulls, forge.jobs = "feat/wakes", "[]", `{"jobs":[{"name":"go (rest)","conclusion":"failure"}]}`
	forge.runs = `[{"databaseId":36740825611,"workflowName":"go","status":"completed","conclusion":"failure","headSha":"4e8bd9e536","url":"https://github.com/o/r/actions/runs/36740825611"}]`
	forge.once = onceAnswer(
		onceSuite(36740825611, [2]string{"Failed once", "go (conpty): example.test/internal/conpty TestKeys failed once and passed on the second try"}),
		onceSuite(36740825999, [2]string{"Failed once", "one-line (pwsh): install.ps1 TestInstalls failed once and passed on the second try"}))
	s.Options.CI = forge
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	// Act
	red := pollForge(t, s, h, &now)

	// Assert
	want := "workflow go, job go (rest), and 1 failed once and passed on the second try (go (conpty): example.test/internal/conpty TestKeys), run 36740825611"
	if len(red) != 1 || red[0].Key != "main:"+filepath.Base(project) || !strings.Contains(red[0].Detail, want) {
		t.Fatalf("main red = %+v, want one wake holding %q", red, want)
	}
	if strings.Contains(red[0].Detail, "TestInstalls") {
		t.Errorf("wake %q names a test of another run of the commit", red[0].Detail)
	}
}

// A paused goblin's awaited run that completed green with a test that
// failed once says so as it tells the CFO to resume the goblin.
func TestAnAwaitedRunNamesTheTestsThatFailedOnce(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	now := time.Now().UTC()
	head := strings.Repeat("a", 40)
	target := "https://github.com/owner/repo/actions/runs/42"
	pausedGoblin(t, h, "waiting-task", "ci", "run:"+target+"@"+head, now.Add(-time.Hour))
	forge := forgeFor(h.Root, "waiting-task")
	forge.jobs = fmt.Sprintf(`{"databaseId":42,"workflowName":"go","status":"completed","conclusion":"success","headSha":%q,"url":%q,"attempt":1,"startedAt":"2026-10-10T12:00:00Z","updatedAt":"2026-10-10T12:09:00Z"}`, head, target)
	forge.once = onceAnswer(onceSuite(42, [2]string{"Failed once", "go (spawn-2): example.test/internal/spawn TestSwitches failed once and passed on the second try"}), onceSuite(43, [2]string{"Failed once", "another run's test"}))
	service.Options.CI = forge
	watched := fleetWakes{}

	// Act
	if err := service.pollAwaitedRuns(t.Context(), &watched, now); err != nil {
		t.Fatal(err)
	}

	// Assert
	wakes := fleetWakeRecords(t, h, "ci")
	want := "run 42 completed with success, and 1 failed once and passed on the second try (go (spawn-2): example.test/internal/spawn TestSwitches) at " + head
	if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, want) {
		t.Fatalf("the awaited run = %+v, want one wake holding %q", wakes, want)
	}
}
