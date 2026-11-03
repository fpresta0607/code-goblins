package actionsbilling

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type billingRunner struct {
	requests []execx.Request
	usage    string
	failure  string
	account  string
}

func (r *billingRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	if r.failure == "start" {
		return execx.Result{}, errors.New("fixture start failure")
	}
	if len(r.requests) == 1 {
		if r.account != "" {
			return execx.Result{Stdout: []byte(r.account)}, nil
		}
		return execx.Result{Stdout: []byte(`{"login":"example-user"}`)}, nil
	}
	if r.failure != "" {
		return execx.Result{ExitCode: 1, Stderr: []byte(r.failure)}, nil
	}
	return execx.Result{Stdout: []byte(r.usage)}, nil
}

func TestReadSumsOnlyActionsNetByRepository(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	runner := &billingRunner{usage: `{"usageItems":[
		{"date":"2026-10-01","product":"Actions","repositoryName":"example-user/fern","grossAmount":100,"netAmount":2.25},
		{"date":"2026-10-02","product":"actions","repositoryName":"example-user/fern","netAmount":1.25},
		{"date":"2026-10-02","product":"actions","repositoryName":"example-user/moss","netAmount":5},
		{"date":"2026-10-02","product":"actions","repositoryName":"example-user/public-example","grossAmount":80,"netAmount":0},
		{"date":"2026-10-02","product":"packages","repositoryName":"example-user/other","netAmount":20},
		{"date":"2026-09-30","product":"actions","repositoryName":"example-user/old","netAmount":40}
	]}`}
	reading := (Reader{Commands: runner}).Read(context.Background(), now, now.Format("2006-01"))
	if reading.Status != "available" || reading.TotalUSD == nil || *reading.TotalUSD != 8.5 || reading.Month != "2026-10" || !reading.ReadAt.Equal(now) {
		t.Fatalf("reading = %+v", reading)
	}
	want := []Repository{{Name: "example-user/moss", SpentUSD: 5}, {Name: "example-user/fern", SpentUSD: 3.5}}
	if !reflect.DeepEqual(reading.Repositories, want) {
		t.Fatalf("repositories = %+v", reading.Repositories)
	}
	if got := strings.Join(runner.requests[1].Args, " "); got != "api users/example-user/settings/billing/usage?year=2026&month=10" {
		t.Fatalf("billing command = %s", got)
	}
}

func TestReadReportsUnavailableWithoutGuessing(t *testing.T) {
	for _, test := range []struct{ name, usage, failure string }{
		{name: "missing scope", failure: "HTTP 403: scope required"},
		{name: "start failure", failure: "start"},
		{name: "invalid JSON", usage: `{"usageItems":`},
		{name: "missing usage", usage: `{}`},
		{name: "null usage", usage: `{"usageItems":null}`},
		{name: "invalid net", usage: `{"usageItems":[{"product":"actions","date":"2026-10-01","netAmount":"2"}]}`},
		{name: "missing net", usage: `{"usageItems":[{"product":"actions","date":"2026-10-01","repositoryName":"example-user/fern"}]}`},
		{name: "missing repository", usage: `{"usageItems":[{"product":"actions","date":"2026-10-01","netAmount":2}]}`},
		{name: "invalid date", usage: `{"usageItems":[{"product":"actions","date":"wrong","repositoryName":"example-user/fern","netAmount":2}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &billingRunner{usage: test.usage, failure: test.failure}
			reading := (Reader{Commands: runner}).Read(context.Background(), time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), "2026-10")
			if reading.Status != "unavailable" || reading.TotalUSD != nil || reading.Detail == "" {
				t.Fatalf("reading = %+v", reading)
			}
			if test.name == "missing scope" && !strings.Contains(reading.Detail, "user") {
				t.Fatalf("scope guidance missing: %s", reading.Detail)
			}
			if strings.Contains(reading.Detail, "fixture start failure") {
				t.Fatal("raw process error escaped")
			}
		})
	}
}

func TestReadRequiresASignedInAccount(t *testing.T) {
	for _, account := range []string{`{}`, `invalid`} {
		runner := &billingRunner{account: account}
		reading := (Reader{Commands: runner}).Read(context.Background(), time.Now(), "2026-10")
		if reading.TotalUSD != nil || reading.Status != "unavailable" || len(runner.requests) != 1 {
			t.Fatalf("reading = %+v, requests = %d", reading, len(runner.requests))
		}
	}
}

func TestReadUsesTheSelectedCalendarMonth(t *testing.T) {
	now := time.Date(2026, 12, 31, 21, 0, 0, 0, time.FixedZone("fixture-west", -6*60*60))
	runner := &billingRunner{usage: `{"usageItems":[]}`}
	reading := (Reader{Commands: runner}).Read(context.Background(), now, now.Format("2006-01"))
	if reading.Month != "2026-12" || strings.Join(runner.requests[1].Args, " ") != "api users/example-user/settings/billing/usage?year=2026&month=12" {
		t.Fatalf("reading = %+v, request = %+v", reading, runner.requests[1])
	}
}

func TestReadSelectsReportWithoutChangingTheReadingTime(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	runner := &billingRunner{usage: `{"usageItems":[
		{"date":"2026-10-31","product":"actions","repositoryName":"example-user/fern","netAmount":8.5},
		{"date":"2026-11-01","product":"actions","repositoryName":"example-user/fern","netAmount":40}
	]}`}
	reading := (Reader{Commands: runner}).Read(context.Background(), now, "2026-10")
	if reading.Month != "2026-10" || reading.TotalUSD == nil || *reading.TotalUSD != 8.5 || !reading.ReadAt.Equal(now) || !reading.CheckedAt.Equal(now) {
		t.Fatalf("reading = %+v", reading)
	}
	if got := strings.Join(runner.requests[1].Args, " "); got != "api users/example-user/settings/billing/usage?year=2026&month=10" {
		t.Fatalf("billing command = %s", got)
	}
}

func TestReadRejectsInvalidMonthsBeforeCallingGitHub(t *testing.T) {
	for _, month := range []string{"", "2026-00", "2026-13", "2026-1", "2026-10-01", "../user"} {
		t.Run(month, func(t *testing.T) {
			runner := &billingRunner{}
			reading := (Reader{Commands: runner}).Read(context.Background(), time.Now(), month)
			if reading.Status != "unavailable" || reading.TotalUSD != nil || len(runner.requests) != 0 {
				t.Fatalf("reading = %+v, requests = %d", reading, len(runner.requests))
			}
		})
	}
}

func TestReadEmptyUsageIsMeasuredZero(t *testing.T) {
	runner := &billingRunner{usage: `{"usageItems":[]}`}
	reading := (Reader{Commands: runner}).Read(context.Background(), time.Now(), "2026-10")
	if reading.Status != "available" || reading.TotalUSD == nil || *reading.TotalUSD != 0 || len(reading.Repositories) != 0 {
		t.Fatalf("reading = %+v", reading)
	}
}

func TestReadFiltersZeroNetAfterSummingTheRepository(t *testing.T) {
	runner := &billingRunner{usage: `{"usageItems":[
		{"date":"2026-10-01","product":"actions","repositoryName":"example-user/offset","netAmount":1.25},
		{"date":"2026-10-02","product":"actions","repositoryName":"example-user/offset","netAmount":-1.25},
		{"date":"2026-10-02","product":"actions","repositoryName":"example-user/credit","netAmount":-0.5}
	]}`}
	reading := (Reader{Commands: runner}).Read(context.Background(), time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), "2026-10")
	if reading.Status != "available" || reading.TotalUSD == nil || *reading.TotalUSD != -0.5 || len(reading.Repositories) != 1 || reading.Repositories[0].Name != "example-user/credit" {
		t.Fatalf("reading = %+v", reading)
	}
}

func TestReadAcceptsBillingTimestampsAndDocumentedDates(t *testing.T) {
	for _, date := range []string{"2026-10-02", "2026-10-02T00:00:00Z", "2026-10-02T00:00:00.000Z", "2026-11-01T00:30:00+02:00"} {
		t.Run(date, func(t *testing.T) {
			runner := &billingRunner{usage: `{"usageItems":[{"date":"` + date + `","product":"Actions","repositoryName":"example-user/fern","netAmount":2.25}]}`}
			reading := (Reader{Commands: runner}).Read(context.Background(), time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), "2026-10")
			if reading.Status != "available" || reading.TotalUSD == nil || *reading.TotalUSD != 2.25 {
				t.Fatalf("reading = %+v", reading)
			}
		})
	}
}
