package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/githubtest"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/train"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// healthForge is the fake forge, recording each read of its own repository
// GitHub's API is asked, answering the comparison of the open pull requests
// with comparisons, and the read named failureOn with failure, responseDelay
// after it was asked.
type healthForge struct {
	*fakeForge
	comparisons   string
	requests      []healthRead
	failureOn     string
	failure       githubFailure
	responseDelay time.Duration
}

// healthRead is a read of GitHub's API: its name ("pull requests", "user",
// "compare", "push runs", "jobs" or its path) and the body it sent.
type healthRead struct {
	name, body string
}

// githubFailure is GitHub answering with status, header and body, or, when
// isTimingOut, never answering.
type githubFailure struct {
	status      int
	header      http.Header
	body        string
	isTimingOut bool
}

func (f *healthForge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		githubtest.Refuse(w, http.StatusBadRequest, err.Error())
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	path := strings.TrimPrefix(r.URL.Path, "/")
	var asked struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	_ = json.Unmarshal(body, &asked)
	read := path
	switch {
	case strings.Contains(asked.Query, "pullRequests("):
		read = "pull requests"
	case strings.Contains(asked.Query, "compare("):
		read = "compare"
	case strings.HasSuffix(path, "/actions/runs"):
		read = "push runs"
	case strings.HasSuffix(path, "/jobs"):
		read = "jobs"
	}
	f.mu.Lock()
	_, isElsewhere := f.checkouts[requestedRepository(path, asked.Query, asked.Variables)]
	if !isElsewhere {
		f.requests = append(f.requests, healthRead{read, string(body)})
	}
	f.mu.Unlock()
	switch {
	case isElsewhere:
		f.fakeForge.ServeHTTP(w, r)
	case read == f.failureOn:
		time.Sleep(f.responseDelay)
		f.failure.answer(w)
	case read == "compare":
		_, _ = w.Write([]byte(f.comparisons))
	default:
		f.fakeForge.ServeHTTP(w, r)
	}
}

func (failure githubFailure) answer(w http.ResponseWriter) {
	if failure.isTimingOut {
		githubtest.TimeOut(w)
		return
	}
	for name, values := range failure.header {
		w.Header()[http.CanonicalHeaderKey(name)] = values
	}
	w.WriteHeader(failure.status)
	_, _ = w.Write([]byte(failure.body))
}

// forbidden is GitHub refusing a read with HTTP 403 and no rate limit
// header.
var forbidden = githubFailure{status: http.StatusForbidden, body: `{"message":"Resource not accessible by integration"}`}

func healthPull(head, mergeable string, isFork bool, checks string) string {
	return fmt.Sprintf(`[{"number":209,"url":"https://github.com/o/r/pull/209","headRefName":"feat/wakes","headRefOid":%q,"baseRefName":"main","mergeable":%q,"isCrossRepository":%t,"author":{"login":"teammate"},"statusCheckRollup":%s}]`, head, mergeable, isFork, checks)
}

func healthComparison(head string, behind int) string {
	return fmt.Sprintf(`{"data":{"repository":{"ref":{"pr209":{"behindBy":%d,"headTarget":{"oid":%q},"baseTarget":{"oid":"base-head"}}}}}}`, behind, head)
}

// prWakes returns the queued pr wakes whose detail starts with name.
func prWakes(t *testing.T, h home.Home, name string) []wake.Record {
	t.Helper()
	var matching []wake.Record
	for _, record := range fleetWakeRecords(t, h, "pr") {
		if strings.HasPrefix(record.Detail, name+": ") {
			matching = append(matching, record)
		}
	}
	return matching
}

func healthService(t *testing.T, hasGoblin bool) (*Service, home.Home, *healthForge, time.Time) {
	t.Helper()
	service, h := fleetService(t)
	project := t.TempDir()
	forge := &healthForge{fakeForge: forgeFor(project, "cg-health"), comparisons: healthComparison("head-one", 0)}
	forge.branch, forge.runs = "feat/wakes", "[]"
	service.Options.CI = forge
	service.Options.GitHub = githubtest.Client(forge)
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
		wantOwner  string
		wantDetail string
	}{
		{"conflicting goblin", "CONFLICTING", 3, "[]", true, false, "cg-health", "conflicts"},
		{"behind without a goblin", "MERGEABLE", 2, "[]", false, false, "", "behind"},
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
			if len(wakes) != 1 || wakes[0].Key != "health:o/r" || !strings.Contains(wakes[0].Detail, test.wantDetail) {
				t.Fatalf("health wakes = %+v, want one for o/r naming %s", wakes, test.wantDetail)
			}
			if test.wantOwner != "" {
				if !strings.Contains(wakes[0].Detail, test.wantOwner+"'s PR #209") || !strings.Contains(wakes[0].Detail, "tell "+test.wantOwner) {
					t.Fatalf("wake %q does not name its goblin %s", wakes[0].Detail, test.wantOwner)
				}
				for _, instruction := range []string{"merge commit", "regenerate", "one CI run", "never force-push"} {
					if !strings.Contains(wakes[0].Detail, instruction) {
						t.Errorf("wake %q lacks safe update %q", wakes[0].Detail, instruction)
					}
				}
			} else if strings.Contains(wakes[0].Detail, "tell cg-health") || !strings.Contains(wakes[0].Detail, "never pushes") {
				t.Fatalf("teammate's PR was owned or writable: %+v", wakes[0])
			}
		})
	}
}

func TestPRHealthDeduplicatesConditionsAndNewHeadsAcrossRestarts(t *testing.T) {
	service, h, forge, now := healthService(t, true)
	// No live goblin holds the pull request, so it is told whenever it
	// falls behind.
	forge.branch = "feat/elsewhere"
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
	// No live goblin holds the pull request, so it is told whenever it
	// falls behind.
	forge.branch = "feat/elsewhere"
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
		wantHeadWakes     int
	}{
		{"null", `{"data":{"repository":{"ref":{"pr209":null}}}}`, 1},
		{"wrong head", healthComparison("another-head", 2), 1},
		{"negative count", healthComparison("head-one", -1), 1},
		{"missing count", `{"data":{"repository":{"ref":{"pr209":{"headTarget":{"oid":"head-one"},"baseTarget":{"oid":"base"}}}}}}`, 1},
		{"GraphQL error beside a readable ref", `{"data":{"repository":{"ref":{"pr209":null}}},"errors":[{"message":"could not resolve head-one"}]}`, 1},
		{"GraphQL error", `{"data":{"repository":{"ref":null}},"errors":[{"message":"comparison unavailable"}]}`, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, h, forge, now := healthService(t, true)
			forge.pulls, forge.comparisons = healthPull("head-one", "UNKNOWN", false, "[]"), test.comparisons

			err := service.checkFleet(context.Background(), now)

			if err == nil || !strings.Contains(err.Error(), "compar") {
				t.Fatalf("unread comparison returned %v", err)
			}
			if wakes := prWakes(t, h, "pr_health"); len(wakes) != 0 {
				t.Fatalf("unread comparison inferred health: %+v", wakes)
			}
			if wakes := prWakes(t, h, "pr_unread"); len(wakes) != test.wantHeadWakes || len(wakes) > 0 && !strings.Contains(wakes[0].Detail, "PR #209 at head-one") {
				t.Fatalf("unread comparison wakes = %+v, want %d pr_unread naming the head", wakes, test.wantHeadWakes)
			}
			persisted, readErr := readFleetWakes(h.State)
			if readErr != nil || persisted.PRUnread[forge.repo].Failure == "" {
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
	// No live goblin holds the pull request, so it is told whenever it
	// falls behind.
	forge.branch = "feat/elsewhere"
	forge.pulls = strings.TrimSuffix(healthPull("head-one", "MERGEABLE", false, "[]"), "]") + `,{"number":210,"url":"https://github.com/o/r/pull/210","headRefName":"other","headRefOid":"head-two","mergeable":"MERGEABLE","author":{"login":"teammate"}}]`
	forge.comparisons = `{"data":{"repository":{"ref":{"pr209":{"behindBy":2,"headTarget":{"oid":"head-one"},"baseTarget":{"oid":"base"}},"pr210":{"behindBy":3,"headTarget":{"oid":"head-two"},"baseTarget":{"oid":"base"}}}}}}`

	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}

	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "#209") || !strings.Contains(wakes[0].Detail, "#210") {
		t.Fatalf("two unhealthy heads produced %+v, want one wake naming both", wakes)
	}
	var comparisonCalls int
	for _, request := range forge.requests {
		if request.name == "compare" {
			comparisonCalls++
			for _, head := range []string{"head-one", "head-two", "refs/heads/main"} {
				if !strings.Contains(request.body, head) {
					t.Errorf("comparison request lacks %s: %s", head, request.body)
				}
			}
		}
	}
	if comparisonCalls != 1 || len(forge.requests) != 4 {
		t.Fatalf("GitHub requests = %+v, want list, the account the token is of, one comparison batch and main runs", forge.requests)
	}
}

func TestPRHealthBackOffCoversTheRepositoryAndSurvivesRestart(t *testing.T) {
	for _, test := range []struct {
		name, read string
		failure    githubFailure
		wait       time.Duration
	}{
		{"403", "pull requests", forbidden, time.Hour},
		{"429", "pull requests", githubFailure{status: http.StatusTooManyRequests, body: `{"message":"too many requests"}`}, time.Hour},
		{"Retry-After", "compare", githubFailure{status: http.StatusTooManyRequests, header: http.Header{"Retry-After": {"600"}}, body: "{}"}, 10 * time.Minute},
		{"Retry-After date", "compare", githubFailure{status: http.StatusTooManyRequests, header: http.Header{"Retry-After": {"Fri, 02 Oct 2026 12:10:00 GMT"}}, body: "{}"}, 10 * time.Minute},
		{"reset", "compare", githubFailure{status: http.StatusForbidden, header: http.Header{"X-RateLimit-Remaining": {"0"}, "X-RateLimit-Reset": {"1790943000"}}, body: "{}"}, 10 * time.Minute},
		{"main runs", "push runs", githubFailure{status: http.StatusForbidden, body: `{"message":"API rate limit exceeded for user ID 1."}`}, time.Hour},
		{"successful exhausted response", "compare", githubFailure{status: http.StatusOK, header: http.Header{"X-RateLimit-Remaining": {"0"}, "X-RateLimit-Reset": {"1790943000"}}, body: healthComparison("head-one", 0)}, 10 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, h, forge, now := healthService(t, true)
			forge.pulls = healthPull("head-one", "MERGEABLE", false, "[]")
			forge.failureOn, forge.failure = test.read, test.failure
			if err := service.checkFleet(context.Background(), now); err == nil {
				t.Fatal("refused GitHub read returned no error")
			}
			calls := len(forge.requests)
			if test.read == "pull requests" && calls != 1 || test.read == "compare" && calls != 2 {
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
	forge.failureOn, forge.failure = "pull requests", forbidden
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
	forge.failureOn = "compare"
	forge.failure = githubFailure{status: http.StatusTooManyRequests, header: http.Header{"Retry-After": {"600"}}, body: "{}"}
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
	// No live goblin holds the pull request, so it is told whenever it
	// falls behind.
	forge.branch = "feat/elsewhere"
	forge.pulls = strings.TrimSuffix(healthPull("head-one", "MERGEABLE", false, "[]"), "]") + `,{"number":210,"url":"https://github.com/o/r/pull/210","headRefName":"other","headRefOid":"head-two","mergeable":"UNKNOWN"}]`
	forge.comparisons = `{"data":{"repository":{"ref":{"pr209":{"behindBy":2,"headTarget":{"oid":"head-one"},"baseTarget":{"oid":"base"}},"pr210":null}}},"errors":[{"message":"could not resolve head-two"}]}`

	err := service.checkFleet(context.Background(), now)

	if err == nil || !strings.Contains(err.Error(), "head-two") {
		t.Fatalf("partial comparison failure returned %v", err)
	}
	if wakes := prWakes(t, h, "pr_health"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "#209") {
		t.Fatalf("readable unhealthy head was dropped with the unread one: %+v", wakes)
	}
	if wakes := prWakes(t, h, "pr_unread"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "PR #210 at head-two") || strings.Contains(wakes[0].Detail, "PR #209") {
		t.Fatalf("unread head wakes = %+v, want one naming only #210", wakes)
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
	if wakes := prWakes(t, h, "pr_health"); len(wakes) != 0 {
		t.Fatalf("listing cap itself inferred an unhealthy head: %+v", wakes)
	}
	if wakes := prWakes(t, h, "pr_unread"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "limit of 100") {
		t.Fatalf("listing cap wakes = %+v, want one pr_unread naming the limit", wakes)
	}
}

func TestPRHealthBackOffOnRunDetailsStaysVisible(t *testing.T) {
	service, _, forge, now := healthService(t, true)
	forge.pulls, forge.runs = "[]", redGoRun
	forge.failureOn, forge.failure = "jobs", forbidden

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

func TestPRUnreadWakesOncePerHeadWhateverThePollErrors(t *testing.T) {
	service, h, forge, now := healthService(t, true)
	// No live goblin holds the pull request, so it is told whenever it
	// falls behind.
	forge.branch = "feat/elsewhere"
	for poll, reading := range []struct {
		after      time.Duration
		head       string
		isRead     bool
		wantUnread int
		wantHealth int
	}{
		{0, "head-one", false, 1, 0},
		{2 * time.Minute, "head-one", false, 1, 0},
		{4 * time.Minute, "head-one", false, 1, 0},
		{8 * time.Minute, "head-one", false, 1, 0},
		{10 * time.Minute, "head-two", false, 2, 0},
		{12 * time.Minute, "head-two", false, 2, 0},
		{14 * time.Minute, "head-two", true, 2, 1},
		{20 * time.Minute, "head-two", false, 2, 1},
	} {
		forge.pulls = healthPull(reading.head, "MERGEABLE", false, "[]")
		forge.comparisons = fmt.Sprintf(`{"data":{"repository":{"ref":{"pr209":null}}},"errors":[{"message":"timeout %d"}]}`, poll)
		if reading.isRead {
			forge.comparisons = healthComparison(reading.head, 2)
		}
		service = &Service{Store: service.Store, Options: service.Options}

		err := service.checkFleet(context.Background(), now.Add(reading.after))

		if reading.isRead != (err == nil) || !reading.isRead && !strings.Contains(err.Error(), reading.head) {
			t.Fatalf("at %s the board showed %v", reading.after, err)
		}
		if wakes := prWakes(t, h, "pr_unread"); len(wakes) != reading.wantUnread {
			t.Fatalf("at %s: unread wakes = %+v, want %d", reading.after, wakes, reading.wantUnread)
		}
		if wakes := prWakes(t, h, "pr_health"); len(wakes) != reading.wantHealth {
			t.Fatalf("at %s: health wakes = %+v, want %d", reading.after, wakes, reading.wantHealth)
		}
		persisted, readErr := readFleetWakes(h.State)
		if readErr != nil || reading.isRead != (persisted.PRUnread[forge.repo].Failure == "") {
			t.Fatalf("at %s the persisted unread evidence was %+v, %v", reading.after, persisted.PRUnread, readErr)
		}
	}
	unread := prWakes(t, h, "pr_unread")
	if unread[1].Key != "repo:"+filepath.Base(forge.repo) || !strings.Contains(unread[1].Detail, "PR #209 at head-two") || !strings.Contains(unread[1].Detail, "next: check each by hand") || strings.Contains(unread[1].Detail, "gh auth login") {
		t.Fatalf("unread wake = %+v, want one keyed by the repository naming the head and its own next step", unread[1])
	}
	if wakes := fleetWakeRecords(t, h, "ci"); len(wakes) != 0 {
		t.Fatalf("unread PR health raised CI wakes: %+v", wakes)
	}
}

func TestPRUnreadWakesForTheListingLimitOnceUntilItClears(t *testing.T) {
	service, h, forge, now := healthService(t, true)
	// No live goblin holds the pull request, so it is told whenever it
	// falls behind.
	forge.branch = "feat/elsewhere"
	pull := strings.TrimSuffix(strings.TrimPrefix(healthPull("head-one", "MERGEABLE", false, "[]"), "["), "]")
	capped := "[" + strings.TrimSuffix(strings.Repeat(pull+",", 100), ",") + "]"
	underLimit := "[" + strings.TrimSuffix(strings.Repeat(pull+",", 99), ",") + "]"
	forge.comparisons = healthComparison("head-one", 2)
	forge.runs, forge.jobs = redGoRun, `{"jobs":[{"name":"test","conclusion":"failure"}]}`
	for _, reading := range []struct {
		after    time.Duration
		listing  string
		isCapped bool
		wantWoke int
	}{
		{0, capped, true, 1},
		{2 * time.Minute, capped, true, 1},
		{4 * time.Minute, capped, true, 1},
		{10 * time.Minute, capped, true, 1},
		{12 * time.Minute, underLimit, false, 1},
		{14 * time.Minute, capped, true, 2},
		{20 * time.Minute, capped, true, 2},
	} {
		forge.pulls = reading.listing
		service = &Service{Store: service.Store, Options: service.Options}

		err := service.checkFleet(context.Background(), now.Add(reading.after))

		if reading.isCapped != (err != nil && strings.Contains(err.Error(), "first 100")) || !reading.isCapped && err != nil {
			t.Fatalf("at %s the board showed %v", reading.after, err)
		}
		if wakes := prWakes(t, h, "pr_unread"); len(wakes) != reading.wantWoke {
			t.Fatalf("at %s: listing limit wakes = %+v, want %d", reading.after, wakes, reading.wantWoke)
		}
	}
	if wakes := prWakes(t, h, "pr_health"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "2 commits behind") {
		t.Fatalf("readable health beside the listing limit = %+v, want one behind wake", wakes)
	}
	ci := fleetWakeRecords(t, h, "ci")
	if len(ci) != 1 || !strings.Contains(ci[0].Detail, "push CI is red") {
		t.Fatalf("CI wakes beside the listing limit = %+v, want main's red run and no ci_unreadable", ci)
	}
}

func TestCIUnreadableWakesForAPersistentRefusalOnItsSecondRetry(t *testing.T) {
	for _, test := range []struct {
		name, read string
		failure    githubFailure
	}{
		{"403", "pull requests", forbidden},
		{"exhausted allowance", "pull requests", githubFailure{status: http.StatusForbidden, header: http.Header{"X-RateLimit-Remaining": {"0"}}, body: `{"message":"API rate limit exceeded for user ID 1."}`}},
		{"Retry-After", "compare", githubFailure{status: http.StatusTooManyRequests, header: http.Header{"Retry-After": {"600"}}, body: "{}"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, h, forge, now := healthService(t, true)
			forge.pulls = healthPull("head-one", "MERGEABLE", false, "[]")
			forge.failureOn, forge.failure = test.read, test.failure
			at := now
			var lastBackOff time.Time
			for retry, wantWoke := range []int{0, 0, 1} {
				service = &Service{Store: service.Store, Options: service.Options}
				calls := len(forge.requests)

				err := service.checkFleet(context.Background(), at)

				if err == nil || len(forge.requests) == calls {
					t.Fatalf("retry %d read nothing or showed nothing: %v, %+v", retry, err, forge.requests)
				}
				if woke := fleetWakeRecords(t, h, "ci"); len(woke) != wantWoke || wantWoke > 0 && !strings.HasPrefix(woke[0].Detail, "ci_unreadable: ") {
					t.Fatalf("retry %d: CI wakes = %+v, want %d ci_unreadable", retry, woke, wantWoke)
				}
				if woke := prWakes(t, h, "pr_unread"); len(woke) != 0 {
					t.Fatalf("retry %d: a refused comparison woke for its heads: %+v", retry, woke)
				}
				persisted, readErr := readFleetWakes(h.State)
				if readErr != nil || !persisted.BackOff[forge.repo].After(lastBackOff) {
					t.Fatalf("retry %d: backoff %s did not advance past %s: %v", retry, persisted.BackOff[forge.repo], lastBackOff, readErr)
				}
				lastBackOff = persisted.BackOff[forge.repo]
				calls = len(forge.requests)
				service = &Service{Store: service.Store, Options: service.Options}

				held := service.checkFleet(context.Background(), lastBackOff.Add(-time.Minute))

				if len(forge.requests) != calls {
					t.Fatalf("retry %d: GitHub was read before the backoff ended: %+v", retry, forge.requests[calls:])
				}
				if held == nil || !strings.Contains(held.Error(), "wait out a refusal") || test.read == "compare" && !strings.Contains(held.Error(), "PR health: compare") {
					t.Fatalf("retry %d: the board lost the unread evidence during backoff: %v", retry, held)
				}
				at = lastBackOff.Add(time.Minute)
			}
		})
	}
}

func TestPRUnreadKeepsBatchFailuresOnTheBoardWithoutHeadWakes(t *testing.T) {
	for _, test := range []struct {
		name    string
		failure githubFailure
	}{
		{"refusal", githubFailure{status: http.StatusTooManyRequests, header: http.Header{"Retry-After": {"600"}}, body: "{}"}},
		{"timeout", githubFailure{isTimingOut: true}},
		{"server error", githubFailure{status: http.StatusBadGateway, body: "<html>bad gateway</html>"}},
		{"unparseable body", githubFailure{status: http.StatusOK, body: "not json"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, h, forge, now := healthService(t, true)
			forge.pulls = strings.TrimSuffix(healthPull("head-one", "CONFLICTING", false, "[]"), "]") + `,{"number":210,"url":"https://github.com/o/r/pull/210","headRefName":"other","headRefOid":"head-two","mergeable":"MERGEABLE","author":{"login":"teammate"}}]`
			forge.runs, forge.jobs = redGoRun, `{"jobs":[{"name":"test","conclusion":"failure"}]}`
			forge.failureOn, forge.failure = "compare", test.failure
			for _, after := range []time.Duration{0, 2 * time.Minute} {
				service = &Service{Store: service.Store, Options: service.Options}

				err := service.checkFleet(context.Background(), now.Add(after))

				if err == nil || !strings.Contains(err.Error(), "PR health: ") {
					t.Fatalf("at %s the board lost the failed comparison: %v", after, err)
				}
				if wakes := prWakes(t, h, "pr_unread"); len(wakes) != 0 {
					t.Fatalf("at %s a failed comparison woke for its heads: %+v", after, wakes)
				}
				persisted, readErr := readFleetWakes(h.State)
				if readErr != nil || persisted.PRUnread[forge.repo].Failure == "" || persisted.Health["https://github.com/o/r/pull/209"].HasUnreadWake || persisted.Health["https://github.com/o/r/pull/210"].HasUnreadWake {
					t.Fatalf("at %s a failed comparison left %+v, %+v, %v", after, persisted.PRUnread, persisted.Health, readErr)
				}
			}
			if wakes := prWakes(t, h, "pr_health"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "conflicts") {
				t.Fatalf("readable conflict beside a failed comparison = %+v, want one conflict wake", wakes)
			}
			forge.failureOn = ""
			forge.comparisons = `{"data":{"repository":{"ref":{"pr209":{"behindBy":1,"headTarget":{"oid":"head-one"},"baseTarget":{"oid":"base"}},"pr210":null}}}}`
			service = &Service{Store: service.Store, Options: service.Options}

			_ = service.checkFleet(context.Background(), now.Add(11*time.Minute))

			if wakes := prWakes(t, h, "pr_unread"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "PR #210 at head-two") || strings.Contains(wakes[0].Detail, "PR #209") {
				t.Fatalf("a later individual failure of an unchanged head = %+v, want one wake naming only #210", wakes)
			}
			if ci := fleetWakeRecords(t, h, "ci"); !slices.ContainsFunc(ci, func(record wake.Record) bool { return strings.Contains(record.Detail, "push CI is red") }) {
				t.Fatalf("CI wakes beside a failed comparison = %+v, want main's red run", ci)
			}
		})
	}
}

func TestPRUnreadWakesOnceForAConflictingHeadWhoseComparisonFails(t *testing.T) {
	service, h, forge, now := healthService(t, true)
	for _, reading := range []struct {
		after     time.Duration
		head      string
		wantWakes int
	}{
		{0, "head-one", 1},
		{2 * time.Minute, "head-one", 1},
		{4 * time.Minute, "head-one", 1},
		{8 * time.Minute, "head-one", 1},
		{10 * time.Minute, "head-two", 2},
		{12 * time.Minute, "head-two", 2},
	} {
		forge.pulls = healthPull(reading.head, "CONFLICTING", false, "[]")
		forge.comparisons = healthComparison("another-head", 3)
		service = &Service{Store: service.Store, Options: service.Options}

		_ = service.checkFleet(context.Background(), now.Add(reading.after))

		conflicts, unread := prWakes(t, h, "pr_health"), prWakes(t, h, "pr_unread")
		if len(conflicts) != reading.wantWakes || len(unread) != reading.wantWakes {
			t.Fatalf("at %s: conflict wakes %+v and unread wakes %+v, want %d of each", reading.after, conflicts, unread, reading.wantWakes)
		}
		if !strings.Contains(unread[len(unread)-1].Detail, "PR #209 at "+reading.head) || strings.Contains(conflicts[len(conflicts)-1].Detail, "behind") {
			t.Fatalf("at %s: unread %q or conflict %q misreports the head", reading.after, unread[len(unread)-1].Detail, conflicts[len(conflicts)-1].Detail)
		}
	}
}

const passedCheck = `[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]`

// finishedPull is the goblin cg-health's open pull request #209 into main as
// gh lists it, at head-one.
func finishedPull(mergeable, author, checks, labels string) string {
	return fmt.Sprintf(`[{"number":209,"url":"https://github.com/o/r/pull/209","headRefName":"feat/wakes","headRefOid":"head-one","baseRefName":"main","mergeable":%q,"author":{"login":%q},"labels":%s,"statusCheckRollup":%s}]`, mergeable, author, labels, checks)
}

// On 2026-10-10 pr_health woke the CFO five times with a goblin's pull
// request "N commits behind main" (wakes 1000415, 1000420, 1000431, 1000435
// and 1000446), and seven times more once a train's riders were left alone
// (wakes 1000612 to 1000667), each for a green, mergeable pull request its
// goblin had not yet reported done. A merge train tests main and its riders
// together, so a goblin's own pull request that is only behind its base is
// behind to no one's concern, whether its goblin reported it, holds its
// branch now or held it before, and whether it is held, until it is next to
// land with no train to test it. A head whose checks failed or that has none
// asks for nothing either, a conflict always is told, and so is a head no
// live goblin holds: a teammate's, and the fleet's own from another branch.
func TestPRHealthSaysNothingOfAGoblinsPullRequestThatIsOnlyBehindItsBase(t *testing.T) {
	const failed = `[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE"}]`
	const mine, theirs = "tell cg-health to merge", "the fleet reports a teammate's pull request and never pushes to it"
	held := fmt.Sprintf(`[{"name":%q}]`, train.HoldLabel)
	for _, test := range []struct {
		name                              string
		mergeable, author, checks, labels string
		// checkedOut is the branch the goblin's worktree has now, and reflog
		// what it had before.
		checkedOut, reflog string
		isReportedDone     bool
		want               []string
	}{
		{name: "green, reported done and alone, which a train of its own takes", mergeable: "MERGEABLE", author: "o", checks: passedCheck, labels: "[]", checkedOut: "feat/wakes", isReportedDone: true},
		{name: "green and not yet reported done", mergeable: "MERGEABLE", author: "o", checks: passedCheck, labels: "[]", checkedOut: "feat/wakes"},
		{name: "green, on a branch its worktree held before", mergeable: "MERGEABLE", author: "o", checks: passedCheck, labels: "[]", checkedOut: "feat/next", reflog: "checkout: moving from feat/wakes to feat/next\n"},
		{name: "green and held", mergeable: "MERGEABLE", author: "o", checks: passedCheck, labels: held, checkedOut: "feat/wakes", isReportedDone: true},
		{name: "its checks still running, which it waits for", mergeable: "MERGEABLE", author: "o", checks: `[{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS"}]`, labels: "[]", checkedOut: "feat/wakes", isReportedDone: true},
		{name: "in conflict with its base", mergeable: "CONFLICTING", author: "o", checks: passedCheck, labels: "[]", checkedOut: "feat/wakes", isReportedDone: true, want: []string{"cg-health's PR #209", "conflicts with its base", mine}},
		{name: "its checks failed", mergeable: "MERGEABLE", author: "o", checks: failed, labels: "[]", checkedOut: "feat/wakes", isReportedDone: true},
		{name: "it has no checks", mergeable: "MERGEABLE", author: "o", checks: "[]", labels: "[]", checkedOut: "feat/wakes"},
		{name: "a teammate's, green", mergeable: "MERGEABLE", author: "teammate", checks: passedCheck, labels: "[]", checkedOut: "feat/next", want: []string{"teammate's PR #209", "2 commits behind", theirs}},
		{name: "another account's that the goblin works on, green, which no train takes", mergeable: "MERGEABLE", author: "teammate", checks: passedCheck, labels: "[]", checkedOut: "feat/wakes", isReportedDone: true, want: []string{"cg-health's PR #209", "2 commits behind", "next to land, and no merge train takes it", mine}},
		{name: "the fleet's own account's, green, from a branch no live goblin holds", mergeable: "MERGEABLE", author: "o", checks: passedCheck, labels: "[]", checkedOut: "feat/next", want: []string{"o's PR #209", "2 commits behind", "the fleet reports a pull request its own account opened from a branch no live goblin holds"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, h, forge, now := healthService(t, true)
			forge.branch, forge.reflog = test.checkedOut, test.reflog
			forge.pulls = finishedPull(test.mergeable, test.author, test.checks, test.labels)
			forge.comparisons = healthComparison("head-one", 2)
			if test.isReportedDone {
				if err := state.AppendStatus(h.State, "cg-health", "done: PR https://github.com/o/r/pull/209"); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			if err := service.checkFleet(context.Background(), now); err != nil {
				t.Fatal(err)
			}

			// Assert
			wakes := prWakes(t, h, "pr_health")
			if len(test.want) == 0 {
				if len(wakes) != 0 {
					t.Fatalf("health wakes = %+v, want none for a goblin's own pull request that is only behind its base and not next to land", wakes)
				}
				return
			}
			if len(wakes) != 1 {
				t.Fatalf("health wakes = %+v, want one", wakes)
			}
			for _, want := range test.want {
				if !strings.Contains(wakes[0].Detail, want) {
					t.Errorf("wake %q lacks %q", wakes[0].Detail, want)
				}
			}
			if !slices.Contains(test.want, theirs) && strings.Contains(wakes[0].Detail, "teammate") || !slices.Contains(test.want, mine) && strings.Contains(wakes[0].Detail, "tell cg-health") {
				t.Errorf("wake %q calls the pull request a teammate's or the goblin's, want neither but as %q says", wakes[0].Detail, test.want)
			}
			if strings.Contains(wakes[0].Detail, "cfo pr train") {
				t.Errorf("wake %q names cfo pr train, which takes no pull request it tells of", wakes[0].Detail)
			}
		})
	}
}

// On 2026-10-10 pr_health told the CFO that a goblin's pull request was 9
// commits behind main minutes after its push, while its checks still ran
// (wake 1000554). A head whose checks are still running is not yet one a
// merge train has passed over: it is left alone until they conclude, and is
// then judged as any behind head is: told only once it is next to land. A
// conflict wakes at once all the same, and checks that never conclude ask
// nothing of its goblin, since they never passed.
func TestPRHealthLeavesABehindHeadAloneWhileItsChecksRun(t *testing.T) {
	const running = `[{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS"}]`
	const failed = `[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE"}]`
	held := fmt.Sprintf(`[{"name":%q}]`, train.HoldLabel)
	for _, test := range []struct {
		name                 string
		mergeable            string
		wantWhileRunning     string
		checksThen, labels   string
		after                time.Duration
		wantOnceTheyConclude string
	}{
		{"then they pass, alone, for a train of its own", "MERGEABLE", "", passedCheck, "[]", ciPollEvery, ""},
		{"then they fail", "MERGEABLE", "", failed, "[]", ciPollEvery, ""},
		{"then they pass on a held pull request", "MERGEABLE", "", passedCheck, held, ciPollEvery, ""},
		{"and still run at the next poll", "MERGEABLE", "", running, "[]", ciPollEvery, ""},
		{"and still run when a train would have given up on them", "MERGEABLE", "", running, "[]", 3 * time.Hour, ""},
		{"on a head in conflict with its base", "CONFLICTING", "conflicts with its base", running, "[]", ciPollEvery, "conflicts with its base"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, h, forge, now := healthService(t, true)
			forge.pulls = finishedPull(test.mergeable, "o", running, "[]")
			forge.comparisons = healthComparison("head-one", 2)
			if err := state.AppendStatus(h.State, "cg-health", "done: PR https://github.com/o/r/pull/209"); err != nil {
				t.Fatal(err)
			}
			told := func() string {
				wakes := prWakes(t, h, "pr_health")
				if len(wakes) > 1 {
					t.Fatalf("health wakes = %+v, want one at most", wakes)
				}
				if len(wakes) == 0 {
					return ""
				}
				return wakes[0].Detail
			}

			// Act
			if err := service.checkFleet(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			whileRunning := told()
			forge.pulls = finishedPull(test.mergeable, "o", test.checksThen, test.labels)
			if err := service.checkFleet(context.Background(), now.Add(test.after)); err != nil {
				t.Fatal(err)
			}

			// Assert
			if isTold := whileRunning != ""; isTold != (test.wantWhileRunning != "") || !strings.Contains(whileRunning, test.wantWhileRunning) {
				t.Errorf("while its checks ran the CFO was told %q, want %q", whileRunning, test.wantWhileRunning)
			}
			if after := told(); (after != "") != (test.wantOnceTheyConclude != "") || !strings.Contains(after, test.wantOnceTheyConclude) {
				t.Errorf("%s later the CFO was told %q, want %q", test.after, after, test.wantOnceTheyConclude)
			}
		})
	}
}

// On 2026-10-10 seven pr_health wakes in three hours called a live goblin's
// own pull request a teammate's (wakes 1000612 to 1000667). Each was opened
// from the goblin's worktree, and by the time of its wake the worktree had the
// goblin's next branch checked out: Quincy made his second branch at
// 13:39:52Z, and from then on #628 was nobody's until he reported it done at
// 13:52:08Z. A pull request the account the fleet works as opened from a
// branch a live goblin's worktree has or had checked out is that goblin's,
// and git keeps that, so a supervisor that restarts reads it again. One
// another account opened is a teammate's, and one the fleet's account opened
// from a branch no live goblin holds is called neither.
func TestPRHealthNamesAGoblinsPullRequestFromEveryBranchItsWorktreeHeld(t *testing.T) {
	// Arrange
	service, h, forge, now := healthService(t, true)
	forge.branch = "feat/second"
	forge.reflog = "checkout: moving from feat/first to feat/second\ncommit: change the first thing\ncheckout: moving from 4e8bd9e5364fc7fdb8f4a4b4a9e8d3d1c6a2b7f0 to feat/first\n"
	for restart, head := range []string{"head-one", "head-two"} {
		forge.pulls = "[" + strings.Join([]string{
			listedPull("o", 209, "feat/first", head, "CONFLICTING", "o"),
			listedPull("o", 210, "feat/second", head, "CONFLICTING", "o"),
			listedPull("o", 211, "feat/theirs", head, "CONFLICTING", "teammate"),
			listedPull("o", 212, "feat/by-hand", head, "CONFLICTING", "o"),
		}, ",") + "]"
		forge.comparisons = behindBy(map[int]comparedHead{209: {head, 0}, 210: {head, 0}, 211: {head, 0}, 212: {head, 0}})
		// The supervisor restarts, and keeps nothing but what it wrote down.
		service = &Service{Store: service.Store, Options: service.Options}

		// Act
		if err := service.checkFleet(context.Background(), now.Add(time.Duration(restart)*2*ciWakeGap)); err != nil {
			t.Fatal(err)
		}

		// Assert
		wakes := prWakes(t, h, "pr_health")
		if len(wakes) != restart+1 {
			t.Fatalf("health wakes = %+v, want one for each poll that found new heads in conflict", wakes)
		}
		detail := wakes[restart].Detail
		for _, want := range []string{
			"cg-health's PR #209 (feat/first)", "cg-health's PR #210 (feat/second)", "tell cg-health to merge",
			"teammate's PR #211 (feat/theirs)", "the fleet reports a teammate's pull request and never pushes to it",
			"o's PR #212 (feat/by-hand)", "the fleet reports a pull request its own account opened from a branch no live goblin holds",
		} {
			if !strings.Contains(detail, want) {
				t.Errorf("wake %q lacks %q", detail, want)
			}
		}
		if named := strings.Count(detail, "teammate's"); named != 2 {
			t.Errorf("wake %q says teammate's %d times, want it of #211 alone, and once more for what the fleet does with it", detail, named)
		}
	}
}

// A head left alone while its checks failed is told once they pass on a
// second run and it is next to land, as another account's pull request its
// goblin reported done is, since no train takes it: failed checks never ask
// for main.
func TestPRHealthTellsOfABehindHeadOnceItIsNextToLand(t *testing.T) {
	// Arrange
	service, h, forge, now := healthService(t, true)
	if err := state.AppendStatus(h.State, "cg-health", "done: PR https://github.com/o/r/pull/209"); err != nil {
		t.Fatal(err)
	}
	forge.pulls = finishedPull("MERGEABLE", "teammate", `[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE"}]`, "[]")
	forge.comparisons = healthComparison("head-one", 2)
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	whileFailed := prWakes(t, h, "pr_health")
	forge.pulls = finishedPull("MERGEABLE", "teammate", passedCheck, "[]")

	// Act
	if err := service.checkFleet(context.Background(), now.Add(ciPollEvery)); err != nil {
		t.Fatal(err)
	}

	// Assert
	wakes := prWakes(t, h, "pr_health")
	if len(whileFailed) != 0 || len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "2 commits behind") || !strings.Contains(wakes[0].Detail, "next to land") {
		t.Fatalf("health wakes = %+v while its checks had failed and %+v once they passed, want none and then one saying it is behind and next to land", whileFailed, wakes)
	}
}
