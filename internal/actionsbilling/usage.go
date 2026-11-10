package actionsbilling

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/githubapi"
)

// Usage is GitHub Actions use: the minutes run, their value at list price
// and what GitHub bills for them once public repositories and the plan's
// included minutes are free. What GitHub covered is used less billed.
type Usage struct {
	Minutes   float64 `json:"minutes"`
	UsedUSD   float64 `json:"used_usd"`
	BilledUSD float64 `json:"billed_usd"`
}

type Repository struct {
	Name string `json:"name"`
	Usage
}

type Reading struct {
	Status       string       `json:"status"`
	Month        string       `json:"month"`
	Total        *Usage       `json:"total"`
	Repositories []Repository `json:"repositories"`
	ReadAt       time.Time    `json:"read_at"`
	CheckedAt    time.Time    `json:"checked_at"`
	Detail       string       `json:"detail"`
	Problem      *Problem     `json:"problem,omitempty"`
	Recovery     *Recovery    `json:"recovery,omitempty"`
}

type Recovery struct {
	RunID string `json:"run_id"`
	State string `json:"state"`
}

type Problem struct {
	Kind       string `json:"kind"`
	Action     string `json:"action"`
	Account    string `json:"account"`
	Credential string `json:"credential"`
}

// Reader reads the Actions spend of the account gh is signed in as from
// GitHub's API.
type Reader struct{ GitHub *githubapi.Client }

func (r Reader) Read(ctx context.Context, now time.Time, month string) Reading {
	now = now.UTC()
	reading := Reading{Status: "unavailable", Month: month, CheckedAt: now, Detail: "Could not read GitHub Actions billing usage.", Problem: &Problem{Kind: "malformed", Action: "retry", Credential: r.GitHub.CredentialSource()}}
	reportMonth, err := time.Parse("2006-01", month)
	if err != nil {
		reading.Detail = "Invalid GitHub Actions report month."
		return reading
	}
	answer, err := r.GitHub.Do(ctx, http.MethodGet, "user", nil)
	if err != nil {
		return failedReading(reading, err)
	}
	var account struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(answer, &account) != nil || account.Login == "" {
		reading.Detail = "GitHub did not return a signed-in account."
		return reading
	}
	reading.Problem.Account = account.Login
	endpoint := fmt.Sprintf("users/%s/settings/billing/usage?year=%d&month=%d", url.PathEscape(account.Login), reportMonth.Year(), reportMonth.Month())
	answer, err = r.GitHub.Do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return failedReading(reading, err)
	}
	var response struct {
		Items *[]struct {
			Date        string   `json:"date"`
			Product     string   `json:"product"`
			Repository  string   `json:"repositoryName"`
			UnitType    string   `json:"unitType"`
			Quantity    *float64 `json:"quantity"`
			GrossAmount *float64 `json:"grossAmount"`
			NetAmount   *float64 `json:"netAmount"`
		} `json:"usageItems"`
	}
	if json.Unmarshal(answer, &response) != nil || response.Items == nil {
		reading.Detail = "GitHub returned an invalid billing usage report."
		return reading
	}
	var total Usage
	byRepository := map[string]Usage{}
	for _, item := range *response.Items {
		if !strings.EqualFold(item.Product, "actions") {
			continue
		}
		date, err := time.Parse("2006-01-02", item.Date)
		if err != nil {
			date, err = time.Parse(time.RFC3339, item.Date)
		}
		if err != nil || item.Quantity == nil || item.GrossAmount == nil || item.NetAmount == nil {
			reading.Detail = "GitHub returned an invalid Actions usage item."
			return reading
		}
		if date.UTC().Format("2006-01") != reading.Month {
			continue
		}
		if strings.TrimSpace(item.Repository) == "" {
			reading.Detail = "GitHub returned Actions use without a repository name."
			return reading
		}
		// Storage comes in gigabyte-hours: it costs money but runs no minutes.
		use := Usage{UsedUSD: *item.GrossAmount, BilledUSD: *item.NetAmount}
		if strings.EqualFold(item.UnitType, "minutes") {
			use.Minutes = *item.Quantity
		}
		repository := byRepository[item.Repository]
		repository.add(use)
		byRepository[item.Repository] = repository
		total.add(use)
	}
	if math.IsInf(total.Minutes, 0) || math.IsInf(total.UsedUSD, 0) || math.IsInf(total.BilledUSD, 0) {
		reading.Detail = "GitHub returned an invalid Actions total."
		return reading
	}
	reading.Repositories = []Repository{}
	for name, use := range byRepository {
		reading.Repositories = append(reading.Repositories, Repository{Name: name, Usage: use.inCents()})
	}
	slices.SortFunc(reading.Repositories, func(a, b Repository) int {
		if a.UsedUSD != b.UsedUSD {
			return cmp.Compare(b.UsedUSD, a.UsedUSD)
		}
		return strings.Compare(a.Name, b.Name)
	})
	total = total.inCents()
	reading.Status, reading.Detail, reading.ReadAt, reading.Total = "available", "", now, &total
	reading.Problem = nil
	return reading
}

func failedReading(reading Reading, err error) Reading {
	problem := reading.Problem
	problem.Kind, problem.Action = "transient", "retry"
	reading.Detail = "GitHub could not be reached. Retry the billing reading."
	var refused *githubapi.Error
	var credential *githubapi.CredentialError
	isCredentialUnavailable := errors.As(err, &credential) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled)
	if !errors.As(err, &refused) && !isCredentialUnavailable {
		return reading
	}
	switch {
	case isCredentialUnavailable || refused.Status == http.StatusUnauthorized:
		// gh auth refresh refuses a host gh is not signed in to, so a
		// missing or refused login gets sign-in guidance, never a consent run.
		problem.Kind, problem.Action = "authentication", "renew"
		reading.Detail = "The GitHub CLI login on this PC is missing or was refused. Sign in again with gh auth login in a terminal on this PC, then Retry."
		if problem.Credential != "gh" {
			reading.Detail = "The selected GitHub credential was refused."
		}
	case refused.Status == http.StatusTooManyRequests || refused.Status == http.StatusForbidden && (refused.Header.Get("X-RateLimit-Remaining") == "0" || refused.Header.Get("Retry-After") != ""):
		problem.Kind = "rate_limit"
		reading.Detail = "GitHub is limiting requests. Wait before retrying the billing reading."
	case refused.Status >= 500:
		reading.Detail = "GitHub billing is temporarily unavailable. Retry the reading."
	case (refused.Status == http.StatusForbidden || refused.Status == http.StatusNotFound) && refused.Header.Get("X-Accepted-OAuth-Scopes") == "user" && refused.Header.Get("X-OAuth-Scopes") != "" && !slices.Contains(strings.FieldsFunc(refused.Header.Get("X-OAuth-Scopes"), func(character rune) bool { return character == ',' || character == ' ' }), "user") && problem.Account != "":
		problem.Kind, problem.Action = "permission", "allow"
		reading.Detail = "The selected GitHub credential lacks the user scope required for billing. Account eligibility may also affect access."
	default:
		problem.Kind = "unavailable"
		reading.Detail = "GitHub billing is unavailable for this account or credential. Access and account eligibility could not be confirmed."
	}
	if problem.Credential != "gh" && (problem.Action == "allow" || problem.Action == "renew") {
		problem.Action = "update"
		reading.Detail += " Update " + problem.Credential + " in the environment that starts Code Goblins; it takes precedence over the CLI login."
	}
	return reading
}

func (u *Usage) add(use Usage) {
	u.Minutes += use.Minutes
	u.UsedUSD += use.UsedUSD
	u.BilledUSD += use.BilledUSD
}

// inCents rounds the dollars GitHub reports in fractions of a cent, so used,
// covered and billed shown in cents add up.
func (u Usage) inCents() Usage {
	u.UsedUSD = math.Round(u.UsedUSD*100) / 100
	u.BilledUSD = math.Round(u.BilledUSD*100) / 100
	return u
}
