package supervisor

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// listedPull is one open pull request as gh pr list gives it, in owner's
// repository r.
func listedPull(owner string, number int, branch, head, mergeable, author string) string {
	return fmt.Sprintf(`{"number":%d,"url":"https://github.com/%s/r/pull/%d","headRefName":%q,"headRefOid":%q,"baseRefName":"main","mergeable":%q,"author":{"login":%q},"statusCheckRollup":[]}`, number, owner, number, branch, head, mergeable, author)
}

// comparedHead is a listed head and how many commits it is behind main.
type comparedHead struct {
	head   string
	behind int
}

// behindBy is GitHub's comparison of each listed head with main, by number.
func behindBy(heads map[int]comparedHead) string {
	var refs []string
	for _, number := range slices.Sorted(maps.Keys(heads)) {
		refs = append(refs, fmt.Sprintf(`"pr%d":{"behindBy":%d,"headTarget":{"oid":%q},"baseTarget":{"oid":"base-head"}}`, number, heads[number].behind, heads[number].head))
	}
	return `{"data":{"repository":{"ref":{` + strings.Join(refs, ",") + `}}}}`
}

// On 2026-10-07 C:\dev\no-mistakes, a checkout of the Overlord's fork whose
// origin is kunchenguid's upstream, raised 39 pr_health wakes for other
// people's pull requests there. A repository the fleet does not own is none
// of its business: GitHub's owner of the listed pull requests decides, never
// the checkout's origin, which here names the fleet's own account.
func TestPRHealthIgnoresPullRequestsInARepositoryTheFleetDoesNotOwn(t *testing.T) {
	// Arrange
	service, h, forge, now := healthService(t, true)
	forge.pulls = "[" + listedPull("upstream", 1359, "fix/one", "head-a", "CONFLICTING", "someone") + "," + listedPull("upstream", 1342, "fix/two", "head-b", "MERGEABLE", "another") + "]"
	forge.comparisons = behindBy(map[int]comparedHead{1359: {"head-a", 4}, 1342: {"head-b", 9}})

	// Act
	err := service.checkFleet(context.Background(), now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
		t.Fatalf("pull requests in a repository the fleet does not own woke %+v", wakes)
	}
	for _, request := range forge.requests {
		if slices.Contains(request.Args, "graphql") {
			t.Fatalf("pull requests the fleet does not watch were compared: %v", request.Args)
		}
	}
}

func TestPRHealthStillReportsAGoblinsPullRequestInAnotherOwnersRepository(t *testing.T) {
	// Arrange
	service, h, forge, now := healthService(t, true)
	forge.pulls = "[" + listedPull("upstream", 1400, "feat/wakes", "head-one", "CONFLICTING", "fpresta0607") + "," + listedPull("upstream", 1342, "fix/two", "head-b", "CONFLICTING", "another") + "]"
	forge.comparisons = behindBy(map[int]comparedHead{1400: {"head-one", 2}})

	// Act
	err := service.checkFleet(context.Background(), now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	wakes := prWakes(t, h, "pr_health")
	if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "#1400") || !strings.Contains(wakes[0].Detail, "cg-health") || strings.Contains(wakes[0].Detail, "#1342") {
		t.Fatalf("health wakes = %+v, want one for the goblin's #1400 alone", wakes)
	}
}

func TestPRHealthReportsATeammatesPullRequestOnlyInARepositoryTheFleetOwns(t *testing.T) {
	for _, test := range []struct {
		name, owner, settings string
		isReported            bool
	}{
		{name: "the account gh works as", owner: "o", isReported: true},
		{name: "an organization the fleet names", owner: "siq-org", settings: `{"github_owners":["SIQ-Org"]}`, isReported: true},
		{name: "an organization the fleet does not name", owner: "siq-org", settings: `{"github_owners":["other-org"]}`},
		{name: "another account", owner: "upstream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, h, forge, now := healthService(t, false)
			if test.settings != "" {
				writeFile(t, filepath.Join(h.Root, "config", "fleet.json"), test.settings)
			}
			forge.pulls = "[" + listedPull(test.owner, 12, "feat/theirs", "head-t", "MERGEABLE", "teammate") + "]"
			forge.comparisons = behindBy(map[int]comparedHead{12: {"head-t", 3}})

			// Act
			err := service.checkFleet(context.Background(), now)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			wakes := prWakes(t, h, "pr_health")
			if test.isReported != (len(wakes) == 1) || len(wakes) > 1 {
				t.Fatalf("health wakes = %+v, want reported=%t", wakes, test.isReported)
			}
			if test.isReported && (!strings.Contains(wakes[0].Detail, "teammate's PR #12") || !strings.Contains(wakes[0].Detail, "never pushes")) {
				t.Fatalf("teammate's pull request was not reported as theirs: %q", wakes[0].Detail)
			}
		})
	}
}

func TestPRHealthGroupsAPollsUnhealthyPullRequestsIntoOneWakePerRepository(t *testing.T) {
	// Arrange
	service, h, forge, now := healthService(t, true)
	forge.pulls = "[" + strings.Join([]string{
		listedPull("o", 209, "feat/wakes", "head-one", "MERGEABLE", "fpresta0607"),
		listedPull("o", 210, "feat/b", "head-b", "CONFLICTING", "teammate"),
		listedPull("o", 211, "feat/c", "head-c", "MERGEABLE", "teammate"),
		listedPull("o", 212, "feat/d", "head-d", "MERGEABLE", "teammate"),
	}, ",") + "]"
	forge.comparisons = behindBy(map[int]comparedHead{209: {"head-one", 2}, 210: {"head-b", 5}, 211: {"head-c", 1}, 212: {"head-d", 0}})

	// Act
	err := service.checkFleet(context.Background(), now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	wakes := prWakes(t, h, "pr_health")
	if len(wakes) != 1 {
		t.Fatalf("health wakes = %+v, want one for the repository", wakes)
	}
	for _, named := range []string{"o/r", "#209", "#210", "#211", "cg-health", "merge commit", "never pushes"} {
		if !strings.Contains(wakes[0].Detail, named) {
			t.Errorf("grouped wake %q lacks %q", wakes[0].Detail, named)
		}
	}
	if strings.Contains(wakes[0].Detail, "#212") {
		t.Errorf("a healthy head was reported: %q", wakes[0].Detail)
	}
}

func TestPRHealthDoesNotReraiseAPullRequestWhoseStateHasNotChanged(t *testing.T) {
	// Arrange
	service, h, forge, now := healthService(t, false)
	teammate := listedPull("o", 210, "feat/b", "head-b", "MERGEABLE", "teammate")
	another := listedPull("o", 211, "feat/c", "head-c", "CONFLICTING", "teammate")
	forge.comparisons = behindBy(map[int]comparedHead{210: {"head-b", 2}, 211: {"head-c", 3}})

	for _, reading := range []struct {
		after       time.Duration
		pulls       string
		wantNamed   []string
		wantUnnamed []string
	}{
		{0, "[" + teammate + "]", []string{"#210"}, nil},
		{10 * time.Minute, "[" + teammate + "]", nil, nil},
		{20 * time.Minute, "[" + teammate + "," + another + "]", []string{"#211"}, []string{"#210"}},
		{30 * time.Minute, "[" + teammate + "," + another + "]", nil, nil},
	} {
		before := len(prWakes(t, h, "pr_health"))
		forge.pulls = reading.pulls
		service = &Service{Store: service.Store, Options: service.Options}

		// Act
		if err := service.checkFleet(context.Background(), now.Add(reading.after)); err != nil {
			t.Fatal(err)
		}

		// Assert
		raised := prWakes(t, h, "pr_health")[before:]
		if len(reading.wantNamed) == 0 {
			if len(raised) != 0 {
				t.Fatalf("at %s an unchanged pull request woke again: %+v", reading.after, raised)
			}
			continue
		}
		if len(raised) != 1 {
			t.Fatalf("at %s: health wakes = %+v, want one", reading.after, raised)
		}
		for _, named := range reading.wantNamed {
			if !strings.Contains(raised[0].Detail, named) {
				t.Errorf("at %s: %q lacks %s", reading.after, raised[0].Detail, named)
			}
		}
		for _, unnamed := range reading.wantUnnamed {
			if strings.Contains(raised[0].Detail, unnamed) {
				t.Errorf("at %s: %q names the unchanged %s again", reading.after, raised[0].Detail, unnamed)
			}
		}
	}
}

// GitHub works out whether a pull request conflicts only once it is asked: a
// head it has not looked at lists as UNKNOWN, and its conflict shows on a
// later listing. Reporting it behind first and then conflicting woke twice
// for one head that never changed (kunchenguid/no-mistakes#1099 on
// 2026-10-07), so a behind head waits a while for GitHub's answer.
func TestPRHealthWaitsForGitHubsMergeabilityBeforeReportingABehindHead(t *testing.T) {
	// Arrange
	service, h, forge, now := healthService(t, true)
	forge.comparisons = healthComparison("head-one", 3)

	for _, reading := range []struct {
		after     time.Duration
		mergeable string
		wantWakes int
	}{
		{0, "UNKNOWN", 0},
		{2 * time.Minute, "CONFLICTING", 1},
		{8 * time.Minute, "CONFLICTING", 1},
	} {
		forge.pulls = healthPull("head-one", reading.mergeable, false, "[]")

		// Act
		if err := service.checkFleet(context.Background(), now.Add(reading.after)); err != nil {
			t.Fatal(err)
		}

		// Assert
		wakes := prWakes(t, h, "pr_health")
		if len(wakes) != reading.wantWakes {
			t.Fatalf("at %s: health wakes = %+v, want %d", reading.after, wakes, reading.wantWakes)
		}
		if len(wakes) > 0 && (!strings.Contains(wakes[0].Detail, "conflicts") || strings.Contains(wakes[0].Detail, "behind")) {
			t.Fatalf("at %s: %q, want the conflict alone", reading.after, wakes[0].Detail)
		}
	}
}

func TestPRHealthReportsABehindHeadWhoseMergeabilityStaysUnknown(t *testing.T) {
	// Arrange
	service, h, forge, now := healthService(t, true)
	forge.pulls, forge.comparisons = healthPull("head-one", "UNKNOWN", false, "[]"), healthComparison("head-one", 3)

	for _, after := range []time.Duration{0, 4 * time.Minute, 8 * time.Minute, mergeabilityGrace} {
		// Act
		if err := service.checkFleet(context.Background(), now.Add(after)); err != nil {
			t.Fatal(err)
		}

		// Assert
		wakes := prWakes(t, h, "pr_health")
		if after < mergeabilityGrace && len(wakes) != 0 || after >= mergeabilityGrace && (len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "3 commits behind")) {
			t.Fatalf("at %s: health wakes = %+v", after, wakes)
		}
	}
}
