package tickets

import (
	"slices"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)

func daysAgo(days float64) time.Time {
	return testNow.Add(-time.Duration(days * 24 * float64(time.Hour)))
}

func overlordActivity() Activity {
	return Activity{
		Repository:    "fpresta0607/northwind-api",
		Viewer:        Actor{Login: "fpresta0607", Name: "Franco Presta"},
		DefaultBranch: "main",
		DefaultHead:   "aaaa",
		Events: []Event{
			{Kind: EventCommit, Author: Actor{Login: "fpresta0607"}, At: daysAgo(1)},
			{Kind: EventPullRequest, Number: 420, Author: Actor{Login: "fpresta0607"}, At: daysAgo(1)},
		},
		Branches: []Branch{
			{Name: "main", Head: "aaaa", Author: Actor{Login: "fpresta0607"}, CommittedAt: daysAgo(0.1)},
		},
	}
}

func TestBuildCountsOnlyRecentPeopleOtherThanTheOverlordAsCollaborators(t *testing.T) {
	cases := []struct {
		name              string
		event             *Event
		branch            *Branch
		wantCollaborative bool
	}{
		{name: "a teammate's issue within 30 days", event: &Event{Kind: EventIssue, Number: 414, Author: Actor{Login: "ana-teammate"}, At: daysAgo(10)}, wantCollaborative: true},
		{name: "a teammate's pull request within 30 days", event: &Event{Kind: EventPullRequest, Number: 413, Author: Actor{Login: "ana-teammate"}, At: daysAgo(2)}, wantCollaborative: true},
		{name: "a teammate's commit within 30 days", event: &Event{Kind: EventCommit, Author: Actor{Login: "ben-teammate"}, At: daysAgo(29)}, wantCollaborative: true},
		{name: "a teammate's branch pushed within 30 days", branch: &Branch{Name: "fix/tax-rounding", Head: "bbbb", Author: Actor{Login: "ben-teammate"}, CommittedAt: daysAgo(20)}, wantCollaborative: true},
		{name: "a commit by an unlinked author who is not the Overlord", event: &Event{Kind: EventCommit, Author: Actor{Name: "Ben Teammate"}, At: daysAgo(3)}, wantCollaborative: true},
		{name: "a teammate's issue older than 30 days", event: &Event{Kind: EventIssue, Number: 300, Author: Actor{Login: "ana-teammate"}, At: daysAgo(31)}},
		{name: "a fork-history author's old commit", event: &Event{Kind: EventCommit, Author: Actor{Login: "fork-author"}, At: daysAgo(60)}},
		{name: "a fork-history author's old branch", branch: &Branch{Name: "upstream/feature", Head: "cccc", Author: Actor{Login: "fork-author"}, CommittedAt: daysAgo(45)}},
		{name: "a bot GitHub types as a bot", event: &Event{Kind: EventPullRequest, Number: 7, Author: Actor{Login: "dependabot", IsBot: true}, At: daysAgo(1)}},
		{name: "a bot named with the [bot] suffix", event: &Event{Kind: EventPullRequest, Number: 8, Author: Actor{Login: "cursor[bot]"}, At: daysAgo(1)}},
		{name: "a bot GitHub types as a user: claude", event: &Event{Kind: EventCommit, Author: Actor{Login: "claude"}, At: daysAgo(1)}},
		{name: "a bot GitHub types as a user: cursoragent", event: &Event{Kind: EventCommit, Author: Actor{Login: "cursoragent"}, At: daysAgo(1)}},
		{name: "an unlinked bot commit", event: &Event{Kind: EventCommit, Author: Actor{Name: "devin-ai-integration[bot]"}, At: daysAgo(1)}},
		{name: "the Overlord's commit under his git name", event: &Event{Kind: EventCommit, Author: Actor{Name: "Franco Presta"}, At: daysAgo(1)}},
		{name: "the Overlord's commit under his login as git name", event: &Event{Kind: EventCommit, Author: Actor{Name: "fpresta0607"}, At: daysAgo(1)}},
		{name: "the Overlord's login in another case", event: &Event{Kind: EventIssue, Number: 5, Author: Actor{Login: "FPresta0607"}, At: daysAgo(1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			activity := overlordActivity()
			if tc.event != nil {
				activity.Events = append(activity.Events, *tc.event)
			}
			if tc.branch != nil {
				activity.Branches = append(activity.Branches, *tc.branch)
			}

			// Act
			report := Build(activity, testNow, nil)

			// Assert
			if report.Collaborative != tc.wantCollaborative {
				t.Fatalf("collaborative = %v, want %v (contributors %+v)", report.Collaborative, tc.wantCollaborative, report.Contributors)
			}
			if got := len(report.Contributors) > 0; got != tc.wantCollaborative {
				t.Fatalf("contributors %+v disagree with collaborative %v", report.Contributors, tc.wantCollaborative)
			}
		})
	}
}

func TestBuildTalliesEachContributorWithAvatarAndLastActivity(t *testing.T) {
	// Arrange
	activity := overlordActivity()
	avatar := "https://avatars.githubusercontent.com/u/1?v=4"
	activity.Events = append(activity.Events,
		Event{Kind: EventIssue, Number: 414, Author: Actor{Login: "ana-teammate", AvatarURL: avatar}, At: daysAgo(5)},
		Event{Kind: EventIssue, Number: 415, Author: Actor{Login: "ana-teammate", AvatarURL: avatar}, At: daysAgo(4)},
		Event{Kind: EventPullRequest, Number: 413, Author: Actor{Login: "ana-teammate", AvatarURL: avatar}, At: daysAgo(0.5)},
		Event{Kind: EventIssue, Number: 390, Author: Actor{Login: "ben-teammate"}, At: daysAgo(12)},
	)
	activity.Branches = append(activity.Branches, Branch{Name: "fix/refunds", Head: "dddd", Author: Actor{Login: "ana-teammate"}, CommittedAt: daysAgo(0.4)})

	// Act
	report := Build(activity, testNow, nil)

	// Assert
	if len(report.Contributors) != 2 {
		t.Fatalf("contributors = %+v, want ana-teammate and ben-teammate", report.Contributors)
	}
	first := report.Contributors[0]
	if first.Login != "ana-teammate" || first.AvatarURL != avatar || first.Issues != 2 || first.PullRequests != 1 || first.Branches != 1 || !first.LastActive.Equal(daysAgo(0.4)) {
		t.Fatalf("first contributor = %+v", first)
	}
	if report.Contributors[1].Login != "ben-teammate" || report.Contributors[1].Issues != 1 {
		t.Fatalf("second contributor = %+v", report.Contributors[1])
	}
	if report.Overlord != "fpresta0607" || report.Repository != "fpresta0607/northwind-api" || !report.ReadAt.Equal(testNow) {
		t.Fatalf("report header = %q %q %v", report.Repository, report.Overlord, report.ReadAt)
	}
}

func TestBuildListsOnlyOthersBranchesFromTheLastFourteenDays(t *testing.T) {
	// Arrange
	activity := overlordActivity()
	activity.Branches = append(activity.Branches,
		Branch{Name: "fix/refund-currency-defects", Head: "e1", Author: Actor{Login: "ana-teammate"}, CommittedAt: daysAgo(0.3)},
		Branch{Name: "fix/118-tax-rounding", Head: "e2", Author: Actor{Login: "ben-teammate"}, CommittedAt: daysAgo(13)},
		Branch{Name: "fix/checkout-session-defects", Head: "e3", Author: Actor{Login: "ana-teammate"}, CommittedAt: daysAgo(15)},
		Branch{Name: "fix/sync-says-why", Head: "e4", Author: Actor{Login: "fpresta0607"}, CommittedAt: daysAgo(0.2)},
		Branch{Name: "cursor/agent-fix", Head: "e5", Author: Actor{Login: "cursor[bot]"}, CommittedAt: daysAgo(1)},
	)
	activity.PullRequests = []PullRequest{
		{Number: 413, HeadRef: "fix/refund-currency-defects", Author: Actor{Login: "ana-teammate"}},
		{Number: 500, HeadRef: "fix/118-tax-rounding", FromFork: true, Author: Actor{Login: "someone"}},
	}

	// Act
	report := Build(activity, testNow, nil)

	// Assert
	var names []string
	for _, branch := range report.Branches {
		names = append(names, branch.Name)
	}
	want := []string{"fix/refund-currency-defects", "cursor/agent-fix", "fix/118-tax-rounding"}
	if !slices.Equal(names, want) {
		t.Fatalf("branches = %v, want %v (newest first; not the Overlord's, the default branch or older than 14 days)", names, want)
	}
	if report.Branches[0].PullRequest != 413 {
		t.Fatalf("branch %s pull request = %d, want 413", report.Branches[0].Name, report.Branches[0].PullRequest)
	}
	if report.Branches[2].PullRequest != 0 {
		t.Fatalf("a fork's pull request with the same head name was linked to branch %s", report.Branches[2].Name)
	}
}

func TestBranchesToCompareAreTheListedOnesNoPullRequestHeads(t *testing.T) {
	// Arrange
	activity := overlordActivity()
	activity.Branches = append(activity.Branches,
		Branch{Name: "fix/with-pr", Head: "f1", Author: Actor{Login: "ana-teammate"}, CommittedAt: daysAgo(1)},
		Branch{Name: "fix/no-pr", Head: "f2", Author: Actor{Login: "ana-teammate"}, CommittedAt: daysAgo(1)},
		Branch{Name: "fix/old", Head: "f3", Author: Actor{Login: "ana-teammate"}, CommittedAt: daysAgo(20)},
		Branch{Name: "fix/mine", Head: "f4", Author: Actor{Login: "fpresta0607"}, CommittedAt: daysAgo(1)},
	)
	activity.PullRequests = []PullRequest{{Number: 1, HeadRef: "fix/with-pr"}}

	// Act
	got := BranchesToCompare(activity, testNow)

	// Assert
	if !slices.Equal(got, []string{"fix/no-pr"}) {
		t.Fatalf("branches to compare = %v, want [fix/no-pr]", got)
	}
}

func TestBuildNamesNoOverlapsWithoutAnArea(t *testing.T) {
	report := Build(overlordActivity(), testNow, nil)
	if report.Overlaps != nil {
		t.Fatalf("overlaps = %+v, want none without --brief or --files", report.Overlaps)
	}
}

const syncBrief = "# Brief nw-sync-mismatch\n\n" +
	"## Project\n\nC:\\dev\\northwind-api\n\n" +
	"## Task\n\n" +
	"Diagnose why every billing sync sends a mismatch email for the archived invoice batch and fix the cause.\n" +
	"- tasks/billing_sync.py `_classify_batch_outcome` marks archived invoices missing.\n" +
	"- `_notify_sync_mismatch_if_needed` (api/routes_orders.py) emails at the first terminal status.\n" +
	"- The retry lives in services\\retry, see https://github.com/fpresta0607/northwind-api/pull/405 and C:\\dev\\northwind-api\\api\\main.py.\n" +
	"- Update invoice_service.py so the ceiling comes from measured latency.\n\n" +
	"## Acceptance criteria\n\n- A red-first regression test per fix, in tests/invoices.\n\n" +
	"## Constraints\n\n- nw-quickstart owns services/discovery.py and the ledger reconciliation drift report.\n"

// syncCheckout has the folders syncBrief names.
func syncCheckout(repositoryPath string) bool {
	return repositoryPath == "services/retry" || repositoryPath == "tests/invoices"
}

func noFolders(string) bool { return false }

func TestBriefAreaReadsPathsFromTaskAndAcceptanceOnly(t *testing.T) {
	// Act
	area := BriefArea(syncBrief, syncCheckout)

	// Assert
	want := []string{"api/routes_orders.py", "invoice_service.py", "services/retry", "tasks/billing_sync.py", "tests/invoices"}
	if !slices.Equal(area.Paths, want) {
		t.Fatalf("area paths = %v, want %v (no URL, no absolute path, nothing from Constraints)", area.Paths, want)
	}
}

func TestBriefAreaNeverReadsConstraints(t *testing.T) {
	cases := []struct {
		name      string
		brief     string
		wantPaths []string
	}{
		{
			name:      "a Tasks heading with a colon",
			brief:     "## Tasks:\n\nRetry the refund in api/refunds.py.\n\n## Constraints\n\n- nw-ledger owns services/ledger.py and the reconciliation drift report.\n",
			wantPaths: []string{"api/refunds.py"},
		},
		{
			name:      "a brief with no Task or Acceptance section",
			brief:     "## Goal\n\nRetry the refund in api/refunds.py.\n\n## Constraints\n\n- nw-ledger owns services/ledger.py and the reconciliation drift report.\n\n## Notes\n\nSee docs/refunds.md.\n",
			wantPaths: []string{"api/refunds.py", "docs/refunds.md"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			area := BriefArea(tc.brief, noFolders)

			// Assert
			if !slices.Equal(area.Paths, tc.wantPaths) {
				t.Fatalf("area paths = %v, want %v", area.Paths, tc.wantPaths)
			}
			for _, word := range []string{"ledger", "reconciliation", "drift"} {
				if area.words[word] {
					t.Fatalf("area words include %q, which only Constraints uses", word)
				}
			}
			if !area.words["refund"] {
				t.Fatalf("area words %v lack the task's word refund", area.words)
			}
		})
	}
}

func TestBriefAreaKeepsAFolderPathOnlyWhenTheCheckoutHasIt(t *testing.T) {
	// Arrange
	brief := "## Task\n\nRetry and/or refund the charge in services/retry, keep CI/CD green, and say why in api/refunds.py and web/missing.\n"

	// Act
	area := BriefArea(brief, syncCheckout)

	// Assert
	want := []string{"api/refunds.py", "services/retry"}
	if !slices.Equal(area.Paths, want) {
		t.Fatalf("area paths = %v, want %v (prose slash words and folders the checkout lacks are not paths)", area.Paths, want)
	}
}

func TestBuildNamesEveryPullRequestAndBranchThatChangesTheArea(t *testing.T) {
	// Arrange
	activity := overlordActivity()
	activity.PullRequests = []PullRequest{
		{Number: 412, URL: "https://github.com/fpresta0607/northwind-api/pull/412", HeadRef: "fix/order-session-defects", Author: Actor{Login: "ana-teammate"},
			Files: []string{"api/routes_orders.py", "tasks/billing_sync.py", "web/src/App.tsx"}},
		{Number: 409, HeadRef: "fix/invoice-totals", Author: Actor{Login: "ana-teammate"},
			Files: []string{"services/retry/policy.py", "services/invoice_service.py", "tests/invoices/test_totals.py"}},
		{Number: 408, HeadRef: "fix/retry-backoff", Author: Actor{Login: "ana-teammate"},
			Files: []string{"services/retryable.py", "api/routes_orders.pyc", "tests/invoices_extra/test.py"}},
	}
	activity.Branches = append(activity.Branches, Branch{Name: "fix/no-pr", Head: "f2", Author: Actor{Login: "ben-teammate"}, CommittedAt: daysAgo(1),
		Files: []string{"API/ROUTES_ORDERS.py"}})
	area := BriefArea(syncBrief, syncCheckout)

	// Act
	report := Build(activity, testNow, &area)

	// Assert
	if report.Overlaps == nil {
		t.Fatal("no overlaps reported")
	}
	got := map[string][]string{}
	for _, overlap := range report.Overlaps.Files {
		got[overlap.Branch] = overlap.Files
	}
	want := map[string][]string{
		"fix/order-session-defects": {"api/routes_orders.py", "tasks/billing_sync.py"},
		"fix/invoice-totals":        {"services/invoice_service.py", "services/retry/policy.py", "tests/invoices/test_totals.py"},
		"fix/no-pr":                 {"API/ROUTES_ORDERS.py"},
	}
	if len(got) != len(want) {
		t.Fatalf("file overlaps = %v, want %v (a sibling path sharing a prefix is not the area)", got, want)
	}
	for branch, files := range want {
		if !slices.Equal(got[branch], files) {
			t.Fatalf("overlap for %s = %v, want %v", branch, got[branch], files)
		}
	}
	if !slices.Equal(report.Overlaps.Area, area.Paths) {
		t.Fatalf("overlaps area = %v, want %v", report.Overlaps.Area, area.Paths)
	}
}

func TestBuildMatchesIssuesThatNameAnAreaPathOrShareTheBriefsWords(t *testing.T) {
	// Arrange
	activity := overlordActivity()
	activity.Issues = []Issue{
		{Number: 414, Title: "Support email quotes an old total", Body: "See tasks/billing_sync.py line 40.", Author: Actor{Login: "ana-teammate"}},
		{Number: 415, Title: "Archived invoice batch always reports a mismatch after sync", Author: Actor{Login: "ana-teammate"}},
		{Number: 416, Title: "Archived tab looks blue on the dashboard", Author: Actor{Login: "ana-teammate"}},
		{Number: 417, Title: "Ledger reconciliation drift report lags", Author: Actor{Login: "ben-teammate"}},
		{Number: 418, Title: "Rename routes_orders.py handlers", Author: Actor{Login: "ben-teammate"}},
		{Number: 419, Title: "Terminal status email lags behind the retry queue for large exports and archived projects", Author: Actor{Login: "ben-teammate"}},
	}
	area := BriefArea(syncBrief, syncCheckout)

	// Act
	report := Build(activity, testNow, &area)

	// Assert
	if report.Overlaps == nil {
		t.Fatal("no overlaps reported")
	}
	matched := map[int]IssueMatch{}
	for _, match := range report.Overlaps.Issues {
		matched[match.Number] = match
	}
	if got := matched[414].Paths; !slices.Equal(got, []string{"tasks/billing_sync.py"}) {
		t.Fatalf("issue 414 paths = %v, want the billing sync file its body names", got)
	}
	if got := matched[415].Words; !slices.Equal(got, []string{"archived", "batch", "invoice", "mismatch", "sync"}) {
		t.Fatalf("issue 415 words = %v, want the five title words the brief's task uses", got)
	}
	if got := matched[418].Paths; !slices.Equal(got, []string{"api/routes_orders.py"}) {
		t.Fatalf("issue 418 paths = %v, want the routes file its title names by base name", got)
	}
	if _, ok := matched[416]; ok {
		t.Fatal("issue 416 shares one word with the brief and matched")
	}
	if _, ok := matched[417]; ok {
		t.Fatal("issue 417 matched words that only the brief's Constraints section uses")
	}
	if _, ok := matched[419]; ok {
		t.Fatalf("issue 419 shares five of its eleven title words with a long brief and matched: %+v", matched[419])
	}
	if len(matched) != 3 {
		t.Fatalf("issue matches = %+v, want 414, 415 and 418", report.Overlaps.Issues)
	}
}

func TestWithPathsKeepsEveryRelativeRepositoryPath(t *testing.T) {
	// Arrange
	given := []string{"web", "Dockerfile", ".github", `scripts\Makefile`, `C:\dev\northwind-api\api\x.py`, "https://github.com/fpresta0607/northwind-api/blob/main/api/x.py", "/srv/northwind-api/api/y.py", `\\fileserver\northwind-api\z.py`, "../northwind-web/app.ts", "web/./src"}

	// Act
	area, ignored := Area{}.WithPaths(given...)

	// Assert
	if want := []string{".github", "Dockerfile", "scripts/Makefile", "web"}; !slices.Equal(area.Paths, want) {
		t.Fatalf("area paths = %v, want %v", area.Paths, want)
	}
	if want := given[4:]; !slices.Equal(ignored, want) {
		t.Fatalf("ignored = %v, want %v", ignored, want)
	}
}

func TestBuildMatchesTopLevelPathsGivenByHand(t *testing.T) {
	// Arrange
	activity := overlordActivity()
	activity.PullRequests = []PullRequest{{Number: 9, HeadRef: "chore/ci", Files: []string{".github/workflows/ci.yml", "Dockerfile", "web/src/App.tsx", "api/main.py"}}}
	area, _ := Area{}.WithPaths("web", "Dockerfile", ".github")

	// Act
	report := Build(activity, testNow, &area)

	// Assert
	if report.Overlaps == nil || len(report.Overlaps.Files) != 1 {
		t.Fatalf("overlaps = %+v", report.Overlaps)
	}
	if got, want := report.Overlaps.Files[0].Files, []string{".github/workflows/ci.yml", "Dockerfile", "web/src/App.tsx"}; !slices.Equal(got, want) {
		t.Fatalf("overlapping files = %v, want %v", got, want)
	}
}

func TestBuildMatchesGivenFilesWithoutABrief(t *testing.T) {
	// Arrange
	activity := overlordActivity()
	activity.PullRequests = []PullRequest{{Number: 9, HeadRef: "fix/x", Files: []string{"internal/spawn/spawn.go", "README.md"}}}
	activity.Issues = []Issue{{Number: 3, Title: "Spawn refuses twice", Body: "internal/spawn/spawn.go retries"}}
	area := Area{Paths: []string{"internal/spawn"}}

	// Act
	report := Build(activity, testNow, &area)

	// Assert
	if report.Overlaps == nil {
		t.Fatal("no overlaps reported")
	}
	if len(report.Overlaps.Files) != 1 || !slices.Equal(report.Overlaps.Files[0].Files, []string{"internal/spawn/spawn.go"}) {
		t.Fatalf("file overlaps = %+v", report.Overlaps.Files)
	}
	if len(report.Overlaps.Issues) != 1 || !slices.Equal(report.Overlaps.Issues[0].Paths, []string{"internal/spawn"}) {
		t.Fatalf("issue overlaps = %+v", report.Overlaps.Issues)
	}
}
