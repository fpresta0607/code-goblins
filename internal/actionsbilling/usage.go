package actionsbilling

import (
	"cmp"
	"context"
	"encoding/json"
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
}

// Reader reads the Actions spend of the account gh is signed in as from
// GitHub's API.
type Reader struct{ GitHub *githubapi.Client }

func (r Reader) Read(ctx context.Context, now time.Time, month string) Reading {
	now = now.UTC()
	reading := Reading{Status: "unavailable", Month: month, CheckedAt: now, Detail: "Could not read GitHub Actions billing usage."}
	reportMonth, err := time.Parse("2006-01", month)
	if err != nil {
		reading.Detail = "Invalid GitHub Actions report month."
		return reading
	}
	answer, err := r.GitHub.Do(ctx, http.MethodGet, "user", nil)
	if err != nil {
		reading.Detail = "Could not read the signed-in GitHub account. Check gh authentication."
		return reading
	}
	var account struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(answer, &account) != nil || account.Login == "" {
		reading.Detail = "GitHub did not return a signed-in account."
		return reading
	}
	endpoint := fmt.Sprintf("users/%s/settings/billing/usage?year=%d&month=%d", url.PathEscape(account.Login), reportMonth.Year(), reportMonth.Month())
	answer, err = r.GitHub.Do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		// GitHub's own words can name private repositories.
		reading.Detail = "Could not read GitHub Actions billing usage. Check gh authentication and the token's user scope."
		return reading
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
