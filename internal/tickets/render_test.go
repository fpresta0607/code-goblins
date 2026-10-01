package tickets

import (
	"encoding/json"
	"strings"
	"testing"
)

func teammateActivity() Activity {
	activity := overlordActivity()
	ana := Actor{Login: "ana-teammate", AvatarURL: "https://avatars.githubusercontent.com/u/9?v=4"}
	activity.Events = append(activity.Events,
		Event{Kind: EventPullRequest, Number: 413, Author: ana, At: daysAgo(0.25)},
		Event{Kind: EventIssue, Number: 415, Author: ana, At: daysAgo(0.25)},
	)
	activity.PullRequests = []PullRequest{
		{Number: 413, Title: "fix(refunds): honest currency totals", URL: "https://github.com/o/r/pull/413", Author: ana, HeadRef: "fix/refunds", CreatedAt: daysAgo(0.25),
			Files: []string{"a/1.py", "a/2.py", "a/3.py", "a/4.py", "a/5.py", "a/6.py", "a/7.py", "a/8.py", "a/9.py", "api/routes_orders.py"}},
		{Number: 409, Title: "fix(cart): checkout defects", URL: "https://github.com/o/r/pull/409", Author: ana, HeadRef: "fix/cart", IsDraft: true, CreatedAt: daysAgo(3), Files: []string{"b.py"}},
	}
	activity.Issues = []Issue{{Number: 415, Title: "Archived invoice batch always reports a mismatch after sync", URL: "https://github.com/o/r/issues/415", Author: ana, CreatedAt: daysAgo(0.25), Body: "private detail", Assignees: []string{}, Labels: []string{"enhancement"}}}
	activity.Branches = append(activity.Branches, Branch{Name: "fix/refunds", Head: "t1", Author: ana, CommittedAt: daysAgo(0.25)})
	activity.Unread = []string{"open branches past the first 10 pages"}
	return activity
}

func TestRenderTextShowsTeammatesWorkAndWhereItOverlaps(t *testing.T) {
	// Arrange
	area := BriefArea(syncBrief)
	report := Build(teammateActivity(), testNow, &area)
	var out strings.Builder

	// Act
	if err := RenderText(&out, report); err != nil {
		t.Fatal(err)
	}

	// Assert
	text := out.String()
	for _, want := range []string{
		"fpresta0607/northwind-api, read 2026-10-01 20:00Z\n",
		"Collaborative: 1 person besides fpresta0607 worked here in the last 30 days.\n",
		"Not read: open branches past the first 10 pages.\n",
		"- ana-teammate, active 6h ago: 1 PR opened, 1 issue opened, 1 branch pushed\n",
		"- #413 fix(refunds): honest currency totals (ana-teammate, opened 6h ago, 10 files, branch fix/refunds) https://github.com/o/r/pull/413\n",
		"    a/8.py\n    and 2 more (--json lists every file)\n",
		"- #409 [draft] fix(cart): checkout defects (ana-teammate, opened 3d ago, 1 file, branch fix/cart)",
		"- #415 Archived invoice batch always reports a mismatch after sync (ana-teammate, opened 6h ago; assigned none; labels enhancement) https://github.com/o/r/issues/415\n",
		"- fix/refunds (ana-teammate, last commit 6h ago, PR #413)\n",
		"Overlaps with api/routes_orders.py, invoice_service.py, services/retry, tasks/billing_sync.py, tests/invoices\n",
		"- PR #413 by ana-teammate changes api/routes_orders.py\n",
		"- issue #415 by ana-teammate shares the words archived, batch, invoice, mismatch, sync: Archived invoice batch",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("report lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "private detail") {
		t.Fatalf("report printed an issue body:\n%s", text)
	}
}

func TestRenderTextSaysPlainlyWhenNobodyElseWorksThere(t *testing.T) {
	// Arrange
	area := Area{Paths: []string{"internal/spawn"}}
	report := Build(overlordActivity(), testNow, &area)
	var out strings.Builder

	// Act
	if err := RenderText(&out, report); err != nil {
		t.Fatal(err)
	}

	// Assert
	text := out.String()
	for _, want := range []string{
		"Not collaborative: nobody besides fpresta0607 and bots worked here in the last 30 days.\n",
		"Open pull requests (0)\n",
		"Overlaps with internal/spawn\n- none\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("report lacks %q:\n%s", want, text)
		}
	}
}

func TestRenderJSONCarriesAvatarsAndNeverIssueBodies(t *testing.T) {
	// Arrange
	report := Build(teammateActivity(), testNow, nil)
	var out strings.Builder

	// Act
	if err := RenderJSON(&out, report); err != nil {
		t.Fatal(err)
	}

	// Assert
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out.String()), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["collaborative"] != true || decoded["overlord"] != "fpresta0607" {
		t.Fatalf("json header = %v", decoded)
	}
	contributor := decoded["contributors"].([]any)[0].(map[string]any)
	if contributor["login"] != "ana-teammate" || contributor["avatar_url"] != "https://avatars.githubusercontent.com/u/9?v=4" {
		t.Fatalf("contributor = %v", contributor)
	}
	if _, ok := decoded["overlaps"]; ok {
		t.Fatal("overlaps present without an area")
	}
	if strings.Contains(out.String(), "private detail") || strings.Contains(out.String(), `"body"`) {
		t.Fatalf("json carries an issue body:\n%s", out.String())
	}
	if files := decoded["pull_requests"].([]any)[0].(map[string]any)["files"].([]any); len(files) != 10 {
		t.Fatalf("json files = %v, want every file", files)
	}
}
