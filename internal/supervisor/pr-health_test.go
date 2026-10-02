package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
)

type healthForge struct {
	*fakeForge
	comparisons   string
	requests      []execx.Request
	failureOn     string
	failure       execx.Result
	responseDelay time.Duration
}

func (f *healthForge) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	command := request.Name + " " + strings.Join(request.Args, " ")
	if request.Name == "gh" && fsx.SamePath(request.Dir, f.repo) {
		f.requests = append(f.requests, request)
		if f.failureOn != "" && strings.HasPrefix(command, f.failureOn) {
			time.Sleep(f.responseDelay)
			return f.failure, nil
		}
		if strings.HasPrefix(command, "gh api graphql") {
			return execx.Result{Stdout: []byte(f.comparisons)}, nil
		}
	}
	return f.fakeForge.Run(ctx, request)
}

func healthPull(head, mergeable string, isFork bool, checks string) string {
	return fmt.Sprintf(`[{"number":209,"url":"https://github.com/o/r/pull/209","headRefName":"feat/wakes","headRefOid":%q,"baseRefName":"main","mergeable":%q,"isCrossRepository":%t,"author":{"login":"teammate"},"statusCheckRollup":%s}]`, head, mergeable, isFork, checks)
}

func healthComparison(head string, behind int) string {
	return fmt.Sprintf(`{"data":{"repository":{"ref":{"pr209":{"behindBy":%d,"headTarget":{"oid":%q},"baseTarget":{"oid":"base-head"}}}}}}`, behind, head)
}

func healthService(t *testing.T, hasGoblin bool) (*Service, home.Home, *healthForge, time.Time) {
	t.Helper()
	service, h := fleetService(t)
	project := t.TempDir()
	forge := &healthForge{fakeForge: forgeFor(project, "cg-health"), comparisons: healthComparison("head-one", 0)}
	forge.branch, forge.runs = "feat/wakes", "[]"
	service.Options.CI = forge
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	if hasGoblin {
		liveGoblin(t, h, "cg-health", project)
	} else if err := writeFleetWakes(h.State, fleetWakes{Repos: map[string]time.Time{project: now}}); err != nil {
		t.Fatal(err)
	}
	return service, h, forge, now
}

func TestPRHealthReportsEveryUnhealthyHead(t *testing.T) {
	for _, test := range []struct {
		name       string
		mergeable  string
		behind     int
		checks     string
		hasGoblin  bool
		isFork     bool
		wantKey    string
		wantDetail string
	}{
		{"conflicting goblin", "CONFLICTING", 3, "[]", true, false, "cg-health", "conflicts"},
		{"behind without checks", "MERGEABLE", 2, "[]", true, false, "cg-health", "behind"},
		{"behind with pending checks", "MERGEABLE", 2, `[{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS"}]`, true, false, "cg-health", "behind"},
		{"behind with failed checks", "MERGEABLE", 2, `[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE"}]`, true, false, "cg-health", "behind"},
		{"teammate without a goblin", "CONFLICTING", 0, "[]", false, false, "", "teammate"},
		{"fork with matching branch name", "MERGEABLE", 2, "[]", true, true, "", "teammate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, h, forge, now := healthService(t, test.hasGoblin)
			forge.pulls = healthPull("head-one", test.mergeable, test.isFork, test.checks)
			forge.comparisons = healthComparison("head-one", test.behind)

			if err := service.checkFleet(context.Background(), now); err != nil {
				t.Fatal(err)
			}

			wakes := fleetWakeRecords(t, h, "pr")
			if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, test.wantDetail) {
				t.Fatalf("health wakes = %+v, want one naming %s", wakes, test.wantDetail)
			}
			if test.wantKey != "" {
				if wakes[0].Key != test.wantKey {
					t.Fatalf("owner = %q, want %q", wakes[0].Key, test.wantKey)
				}
				for _, instruction := range []string{"merge commit", "regenerate", "one CI run", "never force-push"} {
					if !strings.Contains(wakes[0].Detail, instruction) {
						t.Errorf("wake %q lacks safe update %q", wakes[0].Detail, instruction)
					}
				}
			} else if wakes[0].Key == "cg-health" || !strings.Contains(wakes[0].Detail, "never pushes") {
				t.Fatalf("teammate's PR was owned or writable: %+v", wakes[0])
			}
		})
	}
}

func TestPRHealthDeduplicatesConditionsAndNewHeadsAcrossRestarts(t *testing.T) {
	service, h, forge, now := healthService(t, true)
	for _, reading := range []struct {
		after     time.Duration
		head      string
		mergeable string
		behind    int
		wantWakes int
	}{
		{0, "head-one", "MERGEABLE", 2, 1},
		{2 * time.Minute, "head-one", "CONFLICTING", 3, 1},
		{6 * time.Minute, "head-one", "CONFLICTING", 3, 2},
		{12 * time.Minute, "head-one", "MERGEABLE", 4, 2},
		{14 * time.Minute, "head-two", "MERGEABLE", 2, 3},
		{16 * time.Minute, "head-three", "MERGEABLE", 2, 3},
		{20 * time.Minute, "head-three", "MERGEABLE", 2, 4},
	} {
		forge.pulls = healthPull(reading.head, reading.mergeable, false, "[]")
		forge.comparisons = healthComparison(reading.head, reading.behind)
		service = &Service{Store: service.Store, Options: service.Options}

		if err := service.checkFleet(context.Background(), now.Add(reading.after)); err != nil {
			t.Fatal(err)
		}

		if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != reading.wantWakes {
			t.Fatalf("at %s on %s/%s: wakes = %+v, want %d", reading.after, reading.head, reading.mergeable, wakes, reading.wantWakes)
		}
	}
}

func TestPRHealthRetainsAHeadOpenForWeeksAndForgetsClosedHeads(t *testing.T) {
	service, h, forge, now := healthService(t, true)
	forge.pulls, forge.comparisons = healthPull("head-one", "MERGEABLE", false, "[]"), healthComparison("head-one", 3)
	for day := range 16 {
		if err := service.checkFleet(context.Background(), now.Add(time.Duration(day)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 1 {
		t.Fatalf("head open for sixteen days woke %d times, want one", len(wakes))
	}
	forge.pulls = "[]"
	if err := service.checkFleet(context.Background(), now.Add(24*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	data, err := fsx.ReadFile(fleetWakesPath(h.State))
	if err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Health json.RawMessage `json:"health"`
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Health) != 0 && string(persisted.Health) != "{}" {
		t.Fatalf("closed heads are still retained: %s", persisted.Health)
	}
}

func TestPRHealthKeepsUnreadComparisonsVisible(t *testing.T) {
	for _, test := range []struct {
		name, comparisons string
	}{
		{"null", `{"data":{"repository":{"ref":{"pr209":null}}}}`},
		{"wrong head", healthComparison("another-head", 2)},
		{"negative count", healthComparison("head-one", -1)},
		{"missing count", `{"data":{"repository":{"ref":{"pr209":{"headTarget":{"oid":"head-one"},"baseTarget":{"oid":"base"}}}}}}`},
		{"GraphQL error", `{"data":{"repository":{"ref":null}},"errors":[{"message":"comparison unavailable"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, h, forge, now := healthService(t, true)
			forge.pulls, forge.comparisons = healthPull("head-one", "UNKNOWN", false, "[]"), test.comparisons

			err := service.checkFleet(context.Background(), now)

			if err == nil || !strings.Contains(err.Error(), "compar") {
				t.Fatalf("unread comparison returned %v", err)
			}
			if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
				t.Fatalf("unread comparison inferred health: %+v", wakes)
			}
			persisted, readErr := readFleetWakes(h.State)
			if readErr != nil || persisted.Unreadable[forge.repo].Failure == "" {
				t.Fatalf("unread evidence disappeared: %+v, %v", persisted, readErr)
			}
		})
	}
}

func TestPRHealthRejectsUnreadPullRequestLists(t *testing.T) {
	valid := healthPull("head-one", "CONFLICTING", false, "[]")
	for _, listing := range []string{
		"null",
		"{",
		strings.Replace(valid, `"number":209`, `"number":0`, 1),
		strings.Replace(valid, `"headRefOid":"head-one"`, `"headRefOid":""`, 1),
		strings.Replace(valid, `https://github.com/o/r/pull/209`, "invalid-url", 1),
	} {
		t.Run(listing, func(t *testing.T) {
			service, h, forge, now := healthService(t, true)
			forge.pulls = listing

			err := service.checkFleet(context.Background(), now)

			if err == nil {
				t.Fatal("unread list returned no error")
			}
			if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
				t.Fatalf("invalid PR raised a health wake: %+v", wakes)
			}
		})
	}
}

func TestPRHealthBatchesAllOpenHeadsInOneComparisonCall(t *testing.T) {
	service, h, forge, now := healthService(t, true)
	forge.pulls = strings.TrimSuffix(healthPull("head-one", "MERGEABLE", false, "[]"), "]") + `,{"number":210,"url":"https://github.com/o/r/pull/210","headRefName":"other","headRefOid":"head-two","mergeable":"MERGEABLE","author":{"login":"teammate"}}]`
	forge.comparisons = `{"data":{"repository":{"ref":{"pr209":{"behindBy":2,"headTarget":{"oid":"head-one"},"baseTarget":{"oid":"base"}},"pr210":{"behindBy":3,"headTarget":{"oid":"head-two"},"baseTarget":{"oid":"base"}}}}}}`

	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}

	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 2 {
		t.Fatalf("two unhealthy heads produced %+v", wakes)
	}
	var comparisonCalls int
	for _, request := range forge.requests {
		if slices.Contains(request.Args, "graphql") {
			comparisonCalls++
			query := strings.Join(request.Args, " ")
			for _, head := range []string{"head-one", "head-two", "refs/heads/main"} {
				if !strings.Contains(query, head) {
					t.Errorf("comparison request lacks %s: %s", head, query)
				}
			}
		}
	}
	if comparisonCalls != 1 || len(forge.requests) != 3 {
		t.Fatalf("GitHub requests = %+v, want list, one comparison batch and main runs", forge.requests)
	}
}

func TestPRHealthBackOffCoversTheRepositoryAndSurvivesRestart(t *testing.T) {
	for _, test := range []struct {
		name, command string
		failure       execx.Result
		wait          time.Duration
	}{
		{"403", "gh pr list", execx.Result{ExitCode: 1, Stderr: []byte("gh: forbidden (HTTP 403)")}, time.Hour},
		{"429", "gh pr list", execx.Result{ExitCode: 1, Stderr: []byte("gh: too many requests (HTTP 429)")}, time.Hour},
		{"Retry-After", "gh api graphql", execx.Result{ExitCode: 1, Stdout: []byte("HTTP/2.0 429 Too Many Requests\r\nRetry-After: 600\r\n\r\n{}"), Stderr: []byte("gh: HTTP 429")}, 10 * time.Minute},
		{"Retry-After date", "gh api graphql", execx.Result{ExitCode: 1, Stdout: []byte("HTTP/2.0 429 Too Many Requests\r\nRetry-After: Fri, 02 Oct 2026 12:10:00 GMT\r\n\r\n{}"), Stderr: []byte("gh: HTTP 429")}, 10 * time.Minute},
		{"reset", "gh api graphql", execx.Result{ExitCode: 1, Stdout: []byte("HTTP/2.0 403 Forbidden\r\nX-RateLimit-Remaining: 0\r\nX-RateLimit-Reset: 1790943000\r\n\r\n{}"), Stderr: []byte("gh: HTTP 403")}, 10 * time.Minute},
		{"main runs", "gh run list", execx.Result{ExitCode: 1, Stderr: []byte("gh: API rate limit exceeded")}, time.Hour},
		{"successful exhausted response", "gh api graphql", execx.Result{Stdout: []byte("HTTP/2.0 200 OK\r\nX-RateLimit-Remaining: 0\r\nX-RateLimit-Reset: 1790943000\r\n\r\n" + healthComparison("head-one", 0))}, 10 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, h, forge, now := healthService(t, true)
			forge.pulls = healthPull("head-one", "MERGEABLE", false, "[]")
			forge.failureOn, forge.failure = test.command, test.failure
			if err := service.checkFleet(context.Background(), now); err == nil {
				t.Fatal("refused GitHub read returned no error")
			}
			calls := len(forge.requests)
			if test.command == "gh pr list" && calls != 1 || test.command == "gh api graphql" && calls != 2 {
				t.Fatalf("continued GitHub reads after refusal: %+v", forge.requests)
			}
			service = &Service{Store: service.Store, Options: service.Options}
			forge.failureOn = ""

			_ = service.checkFleet(context.Background(), now.Add(test.wait-time.Minute))

			if len(forge.requests) != calls {
				t.Fatalf("persisted wait allowed %d extra calls before expiry", len(forge.requests)-calls)
			}
			if err := service.checkFleet(context.Background(), now.Add(test.wait+time.Minute)); err != nil {
				t.Fatal(err)
			}
			if len(forge.requests) != calls+3 {
				t.Fatalf("expired wait did not resume the repository: %+v", forge.requests)
			}
			persisted, err := readFleetWakes(h.State)
			if err != nil || persisted.Unreadable[forge.repo].Failure != "" {
				t.Fatalf("successful retry did not clear unreadable evidence: %+v, %v", persisted, err)
			}
		})
	}
}

func TestPRHealthDoesNotSuppressOtherRepositoriesDuringBackOff(t *testing.T) {
	service, h, forge, now := healthService(t, true)
	other := t.TempDir()
	liveGoblin(t, h, "cg-other", other)
	forge.pulls = healthPull("head-one", "MERGEABLE", false, "[]")
	forge.failureOn, forge.failure = "gh pr list", execx.Result{ExitCode: 1, Stderr: []byte("gh: forbidden (HTTP 403)")}
	_ = service.checkFleet(context.Background(), now)
	forge.runListDirs = nil

	_ = service.checkFleet(context.Background(), now.Add(ciPollEvery))

	if !slices.Contains(forge.runListDirs, filepath.Clean(other)) || slices.Contains(forge.runListDirs, filepath.Clean(forge.repo)) {
		t.Fatalf("backoff leaked between repositories: main reads in %v", forge.runListDirs)
	}
}

func TestPRHealthRetryAfterStartsWhenTheResponseArrives(t *testing.T) {
	service, h, forge, now := healthService(t, true)
	forge.pulls = healthPull("head-one", "MERGEABLE", false, "[]")
	forge.failureOn = "gh api graphql"
	forge.failure = execx.Result{ExitCode: 1, Stdout: []byte("HTTP/2.0 429 Too Many Requests\r\nRetry-After: 600\r\n\r\n{}")}
	forge.responseDelay = 100 * time.Millisecond

	_ = service.checkFleet(context.Background(), now)

	persisted, err := readFleetWakes(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if until := persisted.BackOff[forge.repo]; until.Before(now.Add(10*time.Minute + forge.responseDelay)) {
		t.Fatalf("Retry-After ends at %s, shortening the requested delay by the read's duration", until)
	}
}

func TestPRHealthReportsAReadableHeadWhenAnotherComparisonFails(t *testing.T) {
	service, h, forge, now := healthService(t, true)
	forge.pulls = strings.TrimSuffix(healthPull("head-one", "MERGEABLE", false, "[]"), "]") + `,{"number":210,"url":"https://github.com/o/r/pull/210","headRefName":"other","headRefOid":"head-two","mergeable":"UNKNOWN"}]`
	forge.comparisons = `{"data":{"repository":{"ref":{"pr209":{"behindBy":2,"headTarget":{"oid":"head-one"},"baseTarget":{"oid":"base"}},"pr210":null}}},"errors":[{"message":"could not resolve head-two"}]}`

	err := service.checkFleet(context.Background(), now)

	if err == nil || !strings.Contains(err.Error(), "head-two") {
		t.Fatalf("partial comparison failure returned %v", err)
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "#209") {
		t.Fatalf("readable unhealthy head was dropped with the unread one: %+v", wakes)
	}
}

func TestPRHealthReportsConflictsWhenComparisonIsUnreadable(t *testing.T) {
	service, h, forge, now := healthService(t, true)
	forge.pulls, forge.comparisons = healthPull("head-one", "CONFLICTING", false, "[]"), `{"data":{"repository":{"ref":null}}}`

	err := service.checkFleet(context.Background(), now)

	if err == nil {
		t.Fatal("unread comparison returned no error")
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "conflicts") {
		t.Fatalf("known conflict was dropped because behind could not be read: %+v", wakes)
	}
}

func TestPRHealthDoesNotWakeForHealthyOrUnknownMergeability(t *testing.T) {
	for _, mergeable := range []string{"MERGEABLE", "UNKNOWN"} {
		t.Run(mergeable, func(t *testing.T) {
			service, h, forge, now := healthService(t, true)
			forge.pulls = healthPull("head-one", mergeable, false, "[]")
			for poll := range 3 {
				if err := service.checkFleet(context.Background(), now.Add(time.Duration(poll)*ciPollEvery)); err != nil {
					t.Fatal(err)
				}
			}
			if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
				t.Fatalf("healthy or unknown PR woke: %+v", wakes)
			}
			if forge.listCalls != 3 {
				t.Fatalf("mergeability was not read again: %d listings", forge.listCalls)
			}
		})
	}
}

func TestPRHealthExposesAListingAtItsLimit(t *testing.T) {
	service, h, forge, now := healthService(t, true)
	pull := strings.TrimSuffix(strings.TrimPrefix(healthPull("head-one", "MERGEABLE", false, "[]"), "["), "]")
	forge.pulls = "[" + strings.TrimSuffix(strings.Repeat(pull+",", 100), ",") + "]"

	err := service.checkFleet(context.Background(), now)

	if err == nil || !strings.Contains(err.Error(), "first 100") || !strings.Contains(err.Error(), "not read") {
		t.Fatalf("listing cap was silent: %v", err)
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
		t.Fatalf("listing cap itself inferred an unhealthy head: %+v", wakes)
	}
}

func TestPRHealthBackOffOnRunDetailsStaysVisible(t *testing.T) {
	service, _, forge, now := healthService(t, true)
	forge.pulls, forge.runs = "[]", redGoRun
	forge.failureOn, forge.failure = "gh run view", execx.Result{ExitCode: 1, Stderr: []byte("gh: forbidden (HTTP 403)")}

	err := service.checkFleet(context.Background(), now)

	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("run-detail refusal disappeared: %v", err)
	}
	calls := len(forge.requests)
	_ = service.checkFleet(context.Background(), now.Add(ciPollEvery))
	if len(forge.requests) != calls {
		t.Fatalf("main's detail refusal did not stop later repository reads: %+v", forge.requests)
	}
}
