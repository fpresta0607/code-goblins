package actionsbilling

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/githubtest"
)

// billingGitHub answers the account and its billing usage as GitHub's API
// does, and records each request's path and query. A failure refuses the
// usage read with its status and message.
type billingGitHub struct {
	requests []string
	usage    string
	status   int
	failure  string
	account  string
	scopes   string
	accepted string
}

func (g *billingGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.requests = append(g.requests, strings.TrimPrefix(r.URL.RequestURI(), "/"))
	switch {
	case len(g.requests) == 1 && g.account != "":
		_, _ = w.Write([]byte(g.account))
	case len(g.requests) == 1:
		_, _ = w.Write([]byte(`{"login":"example-user"}`))
	case g.status != 0:
		w.Header().Set("X-OAuth-Scopes", g.scopes)
		w.Header().Set("X-Accepted-OAuth-Scopes", g.accepted)
		githubtest.Refuse(w, g.status, g.failure)
	default:
		_, _ = w.Write([]byte(g.usage))
	}
}

func reader(github *billingGitHub) Reader {
	return Reader{GitHub: githubtest.Client(github)}
}

// usageItem is one line of GitHub's billing usage report.
func usageItem(date, product, repository, unit string, quantity, gross, net float64) string {
	return fmt.Sprintf(`{"date":%q,"product":%q,"sku":"Actions Linux","repositoryName":%q,"unitType":%q,"quantity":%v,"pricePerUnit":0.006,"grossAmount":%v,"discountAmount":%v,"netAmount":%v}`,
		date, product, repository, unit, quantity, gross, gross-net, net)
}

func usageReport(items ...string) string {
	return `{"usageItems":[` + strings.Join(items, ",") + `]}`
}

// servedJSON is the reading's use as the board receives it.
func servedJSON(t *testing.T, reading Reading) string {
	t.Helper()
	all, err := json.Marshal(reading)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(all, &fields); err != nil {
		t.Fatal(err)
	}
	served, err := json.Marshal(map[string]json.RawMessage{"total": fields["total"], "repositories": fields["repositories"]})
	if err != nil {
		t.Fatal(err)
	}
	return string(served)
}

func TestReadKeepsTheUseOfAMonthGitHubFullyCovers(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	github := &billingGitHub{usage: usageReport(
		usageItem("2026-10-01T00:00:00Z", "actions", "fern", "Minutes", 400, 2.4, 0),
		usageItem("2026-10-02T00:00:00Z", "actions", "fern", "Minutes", 200, 1.2, 0),
		usageItem("2026-10-02T00:00:00Z", "actions", "moss", "Minutes", 100, 0.6, 0),
		usageItem("2026-10-02T00:00:00Z", "actions", "moss", "GigabyteHours", 12.5, 0.004, 0),
	)}
	reading := reader(github).Read(context.Background(), now, "2026-10")
	if reading.Status != "available" {
		t.Fatalf("reading = %+v", reading)
	}
	want := `{"repositories":[{"name":"fern","minutes":600,"used_usd":3.6,"billed_usd":0},{"name":"moss","minutes":100,"used_usd":0.6,"billed_usd":0}],"total":{"minutes":700,"used_usd":4.2,"billed_usd":0}}`
	if got := servedJSON(t, reading); got != want {
		t.Fatalf("served reading\n got %s\nwant %s", got, want)
	}
}

func TestReadSumsAPartlyBilledMonthByRepository(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	github := &billingGitHub{usage: usageReport(
		usageItem("2026-10-03", "actions", "fern", "Minutes", 1000, 8, 0),
		usageItem("2026-10-04", "Actions", "moss", "Minutes", 3000, 24, 10.5),
		usageItem("2026-10-04", "actions", "moss", "minutes", 100, 1.6, 1.6),
		usageItem("2026-10-05", "actions", "moss", "GigabyteHours", 40, 0.0123, 0.0123),
		usageItem("2026-10-05", "packages", "sedge", "GigabyteHours", 10, 20, 20),
		usageItem("2026-09-30", "actions", "reed", "Minutes", 500, 40, 40),
	)}
	reading := reader(github).Read(context.Background(), now, now.Format("2006-01"))
	if reading.Status != "available" || reading.Month != "2026-10" || !reading.ReadAt.Equal(now) || reading.Detail != "" {
		t.Fatalf("reading = %+v", reading)
	}
	if want := (Usage{Minutes: 4100, UsedUSD: 33.61, BilledUSD: 12.11}); reading.Total == nil || *reading.Total != want {
		t.Fatalf("total = %+v, want %+v", reading.Total, want)
	}
	want := []Repository{{Name: "moss", Usage: Usage{Minutes: 3100, UsedUSD: 25.61, BilledUSD: 12.11}}, {Name: "fern", Usage: Usage{Minutes: 1000, UsedUSD: 8}}}
	if !reflect.DeepEqual(reading.Repositories, want) {
		t.Fatalf("repositories = %+v", reading.Repositories)
	}
	if got := github.requests[1]; got != "users/example-user/settings/billing/usage?year=2026&month=10" {
		t.Fatalf("billing request = %s", got)
	}
}

func TestReadKeepsCancelledAndCreditedRepositories(t *testing.T) {
	github := &billingGitHub{usage: usageReport(
		usageItem("2026-10-01", "actions", "offset", "Minutes", 50, 1.25, 1.25),
		usageItem("2026-10-02", "actions", "offset", "Minutes", 0, 0, -1.25),
		usageItem("2026-10-02", "actions", "credit", "Minutes", 0, 0, -0.5),
	)}
	reading := reader(github).Read(context.Background(), time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), "2026-10")
	want := []Repository{{Name: "offset", Usage: Usage{Minutes: 50, UsedUSD: 1.25}}, {Name: "credit", Usage: Usage{BilledUSD: -0.5}}}
	if reading.Status != "available" || reading.Total == nil || *reading.Total != (Usage{Minutes: 50, UsedUSD: 1.25, BilledUSD: -0.5}) || !reflect.DeepEqual(reading.Repositories, want) {
		t.Fatalf("reading = %+v", reading)
	}
}

func TestReadReportsUnavailableWithoutGuessing(t *testing.T) {
	for _, test := range []struct {
		name, usage, failure string
		status               int
	}{
		{name: "missing scope", status: http.StatusForbidden, failure: "scope required"},
		{name: "GitHub fails", status: http.StatusBadGateway, failure: "fixture private-repo detail"},
		{name: "invalid JSON", usage: `{"usageItems":`},
		{name: "missing usage", usage: `{}`},
		{name: "null usage", usage: `{"usageItems":null}`},
		{name: "invalid net", usage: `{"usageItems":[{"product":"actions","date":"2026-10-01","repositoryName":"fern","unitType":"Minutes","quantity":1,"grossAmount":2,"netAmount":"2"}]}`},
		{name: "missing net", usage: `{"usageItems":[{"product":"actions","date":"2026-10-01","repositoryName":"fern","unitType":"Minutes","quantity":1,"grossAmount":2}]}`},
		{name: "missing gross", usage: `{"usageItems":[{"product":"actions","date":"2026-10-01","repositoryName":"fern","unitType":"Minutes","quantity":1,"netAmount":0}]}`},
		{name: "missing quantity", usage: `{"usageItems":[{"product":"actions","date":"2026-10-01","repositoryName":"fern","unitType":"Minutes","grossAmount":2,"netAmount":0}]}`},
		{name: "missing repository", usage: usageReport(usageItem("2026-10-01", "actions", " ", "Minutes", 1, 2, 0))},
		{name: "invalid date", usage: usageReport(usageItem("wrong", "actions", "fern", "Minutes", 1, 2, 2))},
	} {
		t.Run(test.name, func(t *testing.T) {
			github := &billingGitHub{usage: test.usage, status: test.status, failure: test.failure}
			if test.name == "missing scope" {
				github.scopes, github.accepted = "repo, gist, read:org", "user"
			}
			reading := reader(github).Read(context.Background(), time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), "2026-10")
			if reading.Status != "unavailable" || reading.Total != nil || reading.Repositories != nil || reading.Detail == "" {
				t.Fatalf("reading = %+v", reading)
			}
			if test.name == "missing scope" && !strings.Contains(reading.Detail, "user") {
				t.Fatalf("scope guidance missing: %s", reading.Detail)
			}
			if strings.Contains(reading.Detail, "fixture private-repo detail") {
				t.Fatal("GitHub's own words escaped")
			}
		})
	}
}

func TestReadRequiresASignedInAccount(t *testing.T) {
	for _, account := range []string{`{}`, `invalid`} {
		github := &billingGitHub{account: account}
		reading := reader(github).Read(context.Background(), time.Now(), "2026-10")
		if reading.Total != nil || reading.Status != "unavailable" || len(github.requests) != 1 {
			t.Fatalf("reading = %+v, requests = %d", reading, len(github.requests))
		}
	}
}

func TestReadUsesTheSelectedCalendarMonth(t *testing.T) {
	now := time.Date(2026, 12, 31, 21, 0, 0, 0, time.FixedZone("fixture-west", -6*60*60))
	github := &billingGitHub{usage: `{"usageItems":[]}`}
	reading := reader(github).Read(context.Background(), now, now.Format("2006-01"))
	if reading.Month != "2026-12" || github.requests[1] != "users/example-user/settings/billing/usage?year=2026&month=12" {
		t.Fatalf("reading = %+v, request = %+v", reading, github.requests[1])
	}
}

func TestReadSelectsReportWithoutChangingTheReadingTime(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	github := &billingGitHub{usage: usageReport(
		usageItem("2026-10-31", "actions", "fern", "Minutes", 100, 8.5, 8.5),
		usageItem("2026-11-01", "actions", "fern", "Minutes", 500, 40, 40),
	)}
	reading := reader(github).Read(context.Background(), now, "2026-10")
	if reading.Month != "2026-10" || reading.Total == nil || *reading.Total != (Usage{Minutes: 100, UsedUSD: 8.5, BilledUSD: 8.5}) || !reading.ReadAt.Equal(now) || !reading.CheckedAt.Equal(now) {
		t.Fatalf("reading = %+v", reading)
	}
	if got := github.requests[1]; got != "users/example-user/settings/billing/usage?year=2026&month=10" {
		t.Fatalf("billing request = %s", got)
	}
}

func TestReadRejectsInvalidMonthsBeforeCallingGitHub(t *testing.T) {
	for _, month := range []string{"", "2026-00", "2026-13", "2026-1", "2026-10-01", "../user"} {
		t.Run(month, func(t *testing.T) {
			github := &billingGitHub{}
			reading := reader(github).Read(context.Background(), time.Now(), month)
			if reading.Status != "unavailable" || reading.Total != nil || len(github.requests) != 0 {
				t.Fatalf("reading = %+v, requests = %d", reading, len(github.requests))
			}
		})
	}
}

func TestReadAMonthWithoutActionsUseIsMeasuredZero(t *testing.T) {
	github := &billingGitHub{usage: usageReport(usageItem("2026-10-02", "packages", "sedge", "GigabyteHours", 10, 2, 2))}
	reading := reader(github).Read(context.Background(), time.Now(), "2026-10")
	if want := `{"repositories":[],"total":{"minutes":0,"used_usd":0,"billed_usd":0}}`; reading.Status != "available" || servedJSON(t, reading) != want {
		t.Fatalf("reading = %+v, served %s", reading, servedJSON(t, reading))
	}
}

func TestReadAcceptsBillingTimestampsAndDocumentedDates(t *testing.T) {
	for _, date := range []string{"2026-10-02", "2026-10-02T00:00:00Z", "2026-10-02T00:00:00.000Z", "2026-11-01T00:30:00+02:00"} {
		t.Run(date, func(t *testing.T) {
			github := &billingGitHub{usage: usageReport(usageItem(date, "Actions", "fern", "Minutes", 30, 2.25, 2.25))}
			reading := reader(github).Read(context.Background(), time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), "2026-10")
			if reading.Status != "available" || reading.Total == nil || reading.Total.BilledUSD != 2.25 || reading.Total.Minutes != 30 {
				t.Fatalf("reading = %+v", reading)
			}
		})
	}
}

func TestReadClassifiesBillingFailuresWithoutExposingGitHubMessages(t *testing.T) {
	for _, test := range []struct {
		name, scopes, accepted, remaining, retryAfter, kind, action string
		status                                                      int
	}{
		{name: "authenticated missing permission", status: 404, scopes: "gist, read:org, repo, workflow", accepted: "user", kind: "permission", action: "update"},
		{name: "eligible account unknown", status: 404, scopes: "user", accepted: "user", kind: "unavailable", action: "retry"},
		{name: "404 alone proves no permission", status: 404, kind: "unavailable", action: "retry"},
		{name: "invalid login", status: 401, kind: "authentication", action: "update"},
		{name: "primary rate limit", status: 403, remaining: "0", scopes: "repo", accepted: "user", kind: "rate_limit", action: "retry"},
		{name: "secondary rate limit", status: 403, retryAfter: "60", kind: "rate_limit", action: "retry"},
		{name: "too many requests", status: 429, kind: "rate_limit", action: "retry"},
		{name: "server failure", status: 503, kind: "transient", action: "retry"},
		{name: "platform refusal", status: 403, kind: "unavailable", action: "retry"},
		{name: "malformed report", status: 200, kind: "malformed", action: "retry"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := githubtest.Client(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/user" {
					_, _ = w.Write([]byte(`{"login":"example-user"}`))
					return
				}
				if test.scopes != "" {
					w.Header().Set("X-OAuth-Scopes", test.scopes)
				}
				w.Header().Set("X-Accepted-OAuth-Scopes", test.accepted)
				w.Header().Set("X-RateLimit-Remaining", test.remaining)
				w.Header().Set("Retry-After", test.retryAfter)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"message":"private-repo secret-detail"}`))
			}))
			client.Getenv = func(name string) string {
				if name == "GH_TOKEN" {
					return "fixture"
				}
				return ""
			}

			reading := (Reader{GitHub: client}).Read(context.Background(), time.Now(), "2026-11")

			if reading.Problem == nil || reading.Problem.Kind != test.kind || reading.Problem.Action != test.action || reading.Problem.Account != "example-user" || reading.Problem.Credential != "GH_TOKEN" || reading.Total != nil {
				t.Fatalf("reading = %+v, problem = %+v", reading, reading.Problem)
			}
			if strings.Contains(reading.Detail, "private-repo") || strings.Contains(reading.Detail, "secret-detail") {
				t.Fatalf("unsafe detail = %q", reading.Detail)
			}
		})
	}
}

func TestReadNetworkFailureOnlyOffersRetry(t *testing.T) {
	client := githubtest.Client(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { githubtest.TimeOut(w) }))
	reading := (Reader{GitHub: client}).Read(context.Background(), time.Now(), "2026-11")
	if reading.Problem == nil || reading.Problem.Kind != "transient" || reading.Problem.Action != "retry" || reading.Total != nil {
		t.Fatalf("reading = %+v", reading)
	}
}
