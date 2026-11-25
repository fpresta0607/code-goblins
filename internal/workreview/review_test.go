package workreview

import (
	"reflect"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/tickets"
)

const testRepository = "owner/name"

var testNow = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func issue(number int) tickets.IssueRef {
	return tickets.IssueRef{Repository: testRepository, Number: number}
}

func findingsOfKind(findings []Finding, kind string) []Finding {
	var matching []Finding
	for _, finding := range findings {
		if finding.Kind == kind {
			matching = append(matching, finding)
		}
	}
	return matching
}

func TestReviewDoneAlready(t *testing.T) {
	tests := []struct {
		name   string
		item   Item
		states States
		want   []Finding
	}{
		{
			name:   "row whose issues are all closed is done",
			item:   Item{ID: "row", Source: SourceQueued, Issues: []tickets.IssueRef{issue(11), issue(12)}},
			states: States{issue(11): "closed", issue(12): "closed"},
			want: []Finding{{
				Kind:     KindDone,
				Items:    []string{"row"},
				Evidence: []string{"owner/name#11 closed", "owner/name#12 closed"},
				Proposal: "mark the row done, with this evidence",
			}},
		},
		{
			name:   "a merged state settles an issue",
			item:   Item{ID: "row", Source: SourceParked, Issues: []tickets.IssueRef{issue(11)}},
			states: States{issue(11): "merged"},
			want: []Finding{{
				Kind:     KindDone,
				Items:    []string{"row"},
				Evidence: []string{"owner/name#11 merged"},
				Proposal: "mark the row done, with this evidence",
			}},
		},
		{
			name:   "row whose pull request merged is done",
			item:   Item{ID: "row", Source: SourceQueued, Pulls: []tickets.IssueRef{issue(40)}},
			states: States{issue(40): "merged"},
			want: []Finding{{
				Kind:     KindDone,
				Items:    []string{"row"},
				Evidence: []string{"pull request owner/name#40 merged"},
				Proposal: "mark the row done, with this evidence",
			}},
		},
		{
			name:   "paused task is retired with an issue and a pull request as evidence",
			item:   Item{ID: "task", Source: SourcePaused, Issues: []tickets.IssueRef{issue(11)}, Pulls: []tickets.IssueRef{issue(40)}},
			states: States{issue(11): "closed", issue(40): "merged"},
			want: []Finding{{
				Kind:     KindDone,
				Items:    []string{"task"},
				Evidence: []string{"owner/name#11 closed", "pull request owner/name#40 merged"},
				Proposal: "retire the paused task, with this evidence",
			}},
		},
		{
			name:   "one open issue among closed is not done",
			item:   Item{ID: "row", Source: SourceQueued, Issues: []tickets.IssueRef{issue(11), issue(12)}},
			states: States{issue(11): "closed", issue(12): "open"},
		},
		{
			name:   "a pull request closed without merging is not done",
			item:   Item{ID: "row", Source: SourceQueued, Pulls: []tickets.IssueRef{issue(40)}},
			states: States{issue(40): "closed"},
		},
		{
			name:   "an open pull request next to a closed issue is not done",
			item:   Item{ID: "row", Source: SourceQueued, Issues: []tickets.IssueRef{issue(11)}, Pulls: []tickets.IssueRef{issue(40)}},
			states: States{issue(11): "closed", issue(40): "open"},
		},
		{
			name:   "an item that names nothing is not done",
			item:   Item{ID: "row", Source: SourceQueued},
			states: States{},
		},
		{
			name:   "a missing ref is never settled",
			item:   Item{ID: "row", Source: SourceQueued, Issues: []tickets.IssueRef{issue(11)}},
			states: States{issue(11): "missing"},
		},
		{
			name:   "an unread ref is never settled",
			item:   Item{ID: "row", Source: SourceQueued, Issues: []tickets.IssueRef{issue(11), issue(12)}},
			states: States{issue(11): "closed"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := findingsOfKind(Review([]Item{test.item}, test.states, testNow, 7*24*time.Hour), KindDone)

			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("done findings = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestReviewOvertaken(t *testing.T) {
	passed := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	proposal := "close the step, or say what it still waits on"
	tests := []struct {
		name   string
		item   Item
		states States
		want   []Finding
	}{
		{
			name:   "owner step with one closed issue among open ones is overtaken",
			item:   Item{ID: "step", Source: SourceQueued, IsOwnerStep: true, Issues: []tickets.IssueRef{issue(11), issue(12)}},
			states: States{issue(11): "closed", issue(12): "open"},
			want: []Finding{{
				Kind:     KindOvertaken,
				Items:    []string{"step"},
				Evidence: []string{"owner/name#11 closed"},
				Proposal: proposal,
			}},
		},
		{
			name:   "owner step whose named pull request merged is overtaken",
			item:   Item{ID: "step", Source: SourcePaused, IsOwnerStep: true, Issues: []tickets.IssueRef{issue(11)}, Pulls: []tickets.IssueRef{issue(40)}},
			states: States{issue(11): "open", issue(40): "merged"},
			want: []Finding{{
				Kind:     KindOvertaken,
				Items:    []string{"step"},
				Evidence: []string{"pull request owner/name#40 merged"},
				Proposal: proposal,
			}},
		},
		{
			name:   "owner step whose date passed is overtaken",
			item:   Item{ID: "step", Source: SourceParked, IsOwnerStep: true, Dates: []time.Time{passed}},
			states: States{},
			want: []Finding{{
				Kind:     KindOvertaken,
				Items:    []string{"step"},
				Evidence: []string{"until 2026-11-01 passed"},
				Proposal: proposal,
			}},
		},
		{
			name:   "a date at the moment of the review has passed",
			item:   Item{ID: "step", Source: SourceQueued, IsOwnerStep: true, Dates: []time.Time{testNow}},
			states: States{},
			want: []Finding{{
				Kind:     KindOvertaken,
				Items:    []string{"step"},
				Evidence: []string{"until 2026-11-24 passed"},
				Proposal: proposal,
			}},
		},
		{
			name:   "evidence names the issue, the pull request and the date",
			item:   Item{ID: "step", Source: SourceQueued, IsOwnerStep: true, Issues: []tickets.IssueRef{issue(11), issue(12)}, Pulls: []tickets.IssueRef{issue(40)}, Dates: []time.Time{passed, future}},
			states: States{issue(11): "closed", issue(12): "open", issue(40): "merged"},
			want: []Finding{{
				Kind:     KindOvertaken,
				Items:    []string{"step"},
				Evidence: []string{"owner/name#11 closed", "pull request owner/name#40 merged", "until 2026-11-01 passed"},
				Proposal: proposal,
			}},
		},
		{
			name:   "a step that is not the owner's is not overtaken",
			item:   Item{ID: "step", Source: SourceQueued, Issues: []tickets.IssueRef{issue(11), issue(12)}, Dates: []time.Time{passed}},
			states: States{issue(11): "closed", issue(12): "open"},
		},
		{
			name:   "a future date does not overtake",
			item:   Item{ID: "step", Source: SourceQueued, IsOwnerStep: true, Dates: []time.Time{future}},
			states: States{},
		},
		{
			name:   "a pull request closed without merging does not overtake",
			item:   Item{ID: "step", Source: SourceQueued, IsOwnerStep: true, Pulls: []tickets.IssueRef{issue(40)}},
			states: States{issue(40): "closed"},
		},
		{
			name:   "an owner step that is done already is not also overtaken",
			item:   Item{ID: "step", Source: SourceQueued, IsOwnerStep: true, Issues: []tickets.IssueRef{issue(11)}},
			states: States{issue(11): "closed"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := findingsOfKind(Review([]Item{test.item}, test.states, testNow, 7*24*time.Hour), KindOvertaken)

			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("overtaken findings = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestReviewFold(t *testing.T) {
	tests := []struct {
		name   string
		items  []Item
		states States
		want   []Finding
	}{
		{
			name: "two queued rows naming the same open issue fold the newer into the older",
			items: []Item{
				{ID: "newer", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 2},
				{ID: "older", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 1},
			},
			states: States{issue(7): "open"},
			want: []Finding{{
				Kind:     KindFold,
				Items:    []string{"older", "newer"},
				Evidence: []string{"older and newer both name owner/name#7"},
				Proposal: "fold newer into older",
			}},
		},
		{
			name: "two items sharing a path fold",
			items: []Item{
				{ID: "a", Source: SourceQueued, Repository: testRepository, Paths: []string{"internal/x/y.go"}, Order: 1},
				{ID: "b", Source: SourceQueued, Repository: testRepository, Paths: []string{"cmd/z.go", "internal/x/y.go"}, Order: 2},
			},
			want: []Finding{{
				Kind:     KindFold,
				Items:    []string{"a", "b"},
				Evidence: []string{"a and b both touch internal/x/y.go"},
				Proposal: "fold b into a",
			}},
		},
		{
			name: "a paused member is the target even when a queued row is older",
			items: []Item{
				{ID: "queued", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 1},
				{ID: "paused-late", Source: SourcePaused, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 9},
				{ID: "paused-early", Source: SourcePaused, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 5},
			},
			states: States{issue(7): "open"},
			want: []Finding{{
				Kind:  KindFold,
				Items: []string{"paused-early", "queued", "paused-late"},
				Evidence: []string{
					"queued and paused-early both name owner/name#7",
					"queued and paused-late both name owner/name#7",
					"paused-early and paused-late both name owner/name#7",
				},
				Proposal: "fold queued, paused-late into the paused goblin paused-early",
			}},
		},
		{
			name: "a chain of three is one finding for the whole group",
			items: []Item{
				{ID: "c", Source: SourceQueued, Repository: testRepository, Paths: []string{"pkg/b.go"}, Order: 3},
				{ID: "a", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 1},
				{ID: "b", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Paths: []string{"pkg/b.go"}, Order: 2},
			},
			states: States{issue(7): "open"},
			want: []Finding{{
				Kind:  KindFold,
				Items: []string{"a", "b", "c"},
				Evidence: []string{
					"a and b both name owner/name#7",
					"b and c both touch pkg/b.go",
				},
				Proposal: "fold b, c into a",
			}},
		},
		{
			name: "rows of one project without a repository fold when the project matches ignoring case",
			items: []Item{
				{ID: "a", Source: SourceQueued, Project: "Code-Goblins", Paths: []string{"x.go"}, Order: 1},
				{ID: "b", Source: SourcePaused, Project: "code-goblins", Paths: []string{"x.go"}, Order: 2},
			},
			want: []Finding{{
				Kind:     KindFold,
				Items:    []string{"b", "a"},
				Evidence: []string{"a and b both touch x.go"},
				Proposal: "fold a into the paused goblin b",
			}},
		},
		{
			name: "different projects do not fold",
			items: []Item{
				{ID: "a", Source: SourceQueued, Project: "one", Paths: []string{"x.go"}, Order: 1},
				{ID: "b", Source: SourceQueued, Project: "two", Paths: []string{"x.go"}, Order: 2},
			},
		},
		{
			name: "different repositories do not fold even in one project",
			items: []Item{
				{ID: "a", Source: SourceQueued, Project: "one", Repository: "owner/one", Paths: []string{"x.go"}, Order: 1},
				{ID: "b", Source: SourceQueued, Project: "one", Repository: "owner/two", Paths: []string{"x.go"}, Order: 2},
			},
		},
		{
			name: "items with neither a repository nor a project do not fold",
			items: []Item{
				{ID: "a", Source: SourceQueued, Paths: []string{"x.go"}, Order: 1},
				{ID: "b", Source: SourceQueued, Paths: []string{"x.go"}, Order: 2},
			},
		},
		{
			name: "a shared issue that is settled does not link",
			items: []Item{
				{ID: "a", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 1},
				{ID: "b", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 2},
			},
			states: States{issue(7): "closed"},
		},
		{
			name: "a shared issue whose state was not read does not link",
			items: []Item{
				{ID: "a", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 1},
				{ID: "b", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 2},
			},
		},
		{
			name: "a shared issue GitHub says is missing does not link",
			items: []Item{
				{ID: "a", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 1},
				{ID: "b", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 2},
			},
			states: States{issue(7): "missing"},
		},
		{
			name: "a parked row is not folded",
			items: []Item{
				{ID: "a", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 1},
				{ID: "b", Source: SourceParked, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Order: 2},
			},
			states: States{issue(7): "open"},
		},
		{
			name: "items with nothing in common do not fold",
			items: []Item{
				{ID: "a", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(7)}, Paths: []string{"a.go"}, Order: 1},
				{ID: "b", Source: SourceQueued, Repository: testRepository, Issues: []tickets.IssueRef{issue(8)}, Paths: []string{"b.go"}, Order: 2},
			},
			states: States{issue(7): "open", issue(8): "open"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := findingsOfKind(Review(test.items, test.states, testNow, 7*24*time.Hour), KindFold)

			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("fold findings = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestReviewStale(t *testing.T) {
	staleAfter := 7 * 24 * time.Hour
	proposal := "ask the owner once whether to resume, fold or retire it"
	tests := []struct {
		name   string
		item   Item
		states States
		want   []Finding
	}{
		{
			name: "paused task with old activity is stale",
			item: Item{ID: "task", Source: SourcePaused, LastActivity: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)},
			want: []Finding{{
				Kind:     KindStale,
				Items:    []string{"task"},
				Evidence: []string{"last activity 2026-11-10"},
				Proposal: proposal,
			}},
		},
		{
			name: "activity exactly the stale age ago is stale",
			item: Item{ID: "task", Source: SourcePaused, LastActivity: testNow.Add(-staleAfter)},
			want: []Finding{{
				Kind:     KindStale,
				Items:    []string{"task"},
				Evidence: []string{"last activity 2026-11-17"},
				Proposal: proposal,
			}},
		},
		{
			name: "held queued row with no activity on record is not stale",
			item: Item{ID: "row", Source: SourceQueued, IsHeld: true},
		},
		{
			name: "paused task with no activity on record is not stale",
			item: Item{ID: "task", Source: SourcePaused},
		},
		{
			name: "parked row with old activity is stale",
			item: Item{ID: "row", Source: SourceParked, IsHeld: true, LastActivity: testNow.Add(-30 * 24 * time.Hour)},
			want: []Finding{{
				Kind:     KindStale,
				Items:    []string{"row"},
				Evidence: []string{"last activity 2026-10-25"},
				Proposal: proposal,
			}},
		},
		{
			name: "a dated condition keeps it from being stale",
			item: Item{ID: "task", Source: SourcePaused, Dates: []time.Time{testNow.Add(24 * time.Hour)}},
		},
		{
			name: "recent activity is not stale",
			item: Item{ID: "task", Source: SourcePaused, LastActivity: testNow.Add(-staleAfter + time.Minute)},
		},
		{
			name: "a queued row that is not held is not stale",
			item: Item{ID: "row", Source: SourceQueued},
		},
		{
			name:   "an item that is done already is not also stale",
			item:   Item{ID: "task", Source: SourcePaused, Issues: []tickets.IssueRef{issue(11)}, LastActivity: testNow.Add(-30 * 24 * time.Hour)},
			states: States{issue(11): "closed"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := findingsOfKind(Review([]Item{test.item}, test.states, testNow, staleAfter), KindStale)

			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("stale findings = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestReviewListsFindingsByKindThenOrder(t *testing.T) {
	items := []Item{
		{ID: "stale-late", Source: SourcePaused, LastActivity: testNow.Add(-30 * 24 * time.Hour), Order: 6},
		{ID: "stale-early", Source: SourceParked, IsHeld: true, LastActivity: testNow.Add(-30 * 24 * time.Hour), Order: 2},
		{ID: "fold-a", Source: SourceQueued, Repository: testRepository, Paths: []string{"x.go"}, Order: 4},
		{ID: "fold-b", Source: SourceQueued, Repository: testRepository, Paths: []string{"x.go"}, Order: 5},
		{ID: "overtaken", Source: SourceQueued, IsOwnerStep: true, Dates: []time.Time{testNow.Add(-time.Hour)}, Order: 3},
		{ID: "done-late", Source: SourceQueued, Issues: []tickets.IssueRef{issue(11)}, Order: 8},
		{ID: "done-early", Source: SourceQueued, Issues: []tickets.IssueRef{issue(11)}, Order: 1},
	}
	states := States{issue(11): "closed"}

	findings := Review(items, states, testNow, 7*24*time.Hour)

	var got []string
	for _, finding := range findings {
		got = append(got, finding.Key())
	}
	want := []string{
		"done already:done-early",
		"done already:done-late",
		"overtaken:overtaken",
		"fold:fold-a,fold-b",
		"stale:stale-early",
		"stale:stale-late",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("finding keys = %v, want %v", got, want)
	}
}

func TestFindingKeyJoinsKindAndItemsInOrder(t *testing.T) {
	tests := []struct {
		name    string
		finding Finding
		want    string
	}{
		{"one item", Finding{Kind: KindDone, Items: []string{"row"}}, "done already:row"},
		{"fold keeps its target first", Finding{Kind: KindFold, Items: []string{"t", "b", "a"}}, "fold:t,b,a"},
		{"evidence and proposal are not part of it", Finding{Kind: KindStale, Items: []string{"x"}, Evidence: []string{"e"}, Proposal: "p"}, "stale:x"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.finding.Key(); got != test.want {
				t.Fatalf("Key() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestReviewWithNothingToReportReturnsNoFindings(t *testing.T) {
	findings := Review(nil, nil, testNow, 7*24*time.Hour)

	if len(findings) != 0 {
		t.Fatalf("findings = %#v, want none", findings)
	}
}
