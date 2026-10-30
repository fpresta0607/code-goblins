package actionsbilling

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type Repository struct {
	Name     string  `json:"name"`
	SpentUSD float64 `json:"spent_usd"`
}

type Reading struct {
	Status       string       `json:"status"`
	Month        string       `json:"month"`
	TotalUSD     *float64     `json:"total_usd"`
	Repositories []Repository `json:"repositories"`
	ReadAt       time.Time    `json:"read_at"`
	CheckedAt    time.Time    `json:"checked_at"`
	Detail       string       `json:"detail"`
}

type Reader struct{ Commands execx.Runner }

func (r Reader) Read(ctx context.Context, now time.Time) Reading {
	now = now.UTC()
	reading := Reading{Status: "unavailable", Month: now.Format("2006-01"), CheckedAt: now, Detail: "Could not read GitHub Actions billing usage."}
	result, err := r.Commands.Run(ctx, execx.Request{Name: "gh", Args: []string{"api", "user"}})
	if err != nil || result.ExitCode != 0 {
		reading.Detail = "Could not read the signed-in GitHub account. Check gh authentication."
		return reading
	}
	var account struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(result.Stdout, &account) != nil || account.Login == "" {
		reading.Detail = "GitHub did not return a signed-in account."
		return reading
	}
	endpoint := fmt.Sprintf("users/%s/settings/billing/usage?year=%d&month=%d", url.PathEscape(account.Login), now.Year(), now.Month())
	result, err = r.Commands.Run(ctx, execx.Request{Name: "gh", Args: []string{"api", endpoint}})
	if err != nil || result.ExitCode != 0 {
		// Raw CLI errors can contain private repository names and credentials.
		reading.Detail = "Could not read GitHub Actions billing usage. Check gh authentication and the token's user scope."
		return reading
	}
	var response struct {
		Items *[]struct {
			Date       string   `json:"date"`
			Product    string   `json:"product"`
			Repository string   `json:"repositoryName"`
			NetAmount  *float64 `json:"netAmount"`
		} `json:"usageItems"`
	}
	if json.Unmarshal(result.Stdout, &response) != nil || response.Items == nil {
		reading.Detail = "GitHub returned an invalid billing usage report."
		return reading
	}
	byRepository := map[string]float64{}
	for _, item := range *response.Items {
		if !strings.EqualFold(item.Product, "actions") {
			continue
		}
		date, err := time.Parse("2006-01-02", item.Date)
		if err != nil {
			date, err = time.Parse(time.RFC3339, item.Date)
		}
		if err != nil || item.NetAmount == nil {
			reading.Detail = "GitHub returned an invalid Actions usage item."
			return reading
		}
		if date.UTC().Format("2006-01") != reading.Month || *item.NetAmount == 0 {
			continue
		}
		if strings.TrimSpace(item.Repository) == "" {
			reading.Detail = "GitHub returned Actions spend without a repository name."
			return reading
		}
		byRepository[item.Repository] += *item.NetAmount
	}
	total := 0.0
	reading.Repositories = []Repository{}
	for name, amount := range byRepository {
		if amount == 0 {
			continue
		}
		reading.Repositories = append(reading.Repositories, Repository{Name: name, SpentUSD: amount})
	}
	slices.SortFunc(reading.Repositories, func(a, b Repository) int {
		if a.SpentUSD > b.SpentUSD {
			return -1
		}
		if a.SpentUSD < b.SpentUSD {
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	for _, repository := range reading.Repositories {
		total += repository.SpentUSD
	}
	if math.IsInf(total, 0) {
		reading.Repositories = nil
		reading.Detail = "GitHub returned an invalid Actions total."
		return reading
	}
	reading.Status, reading.Detail, reading.ReadAt, reading.TotalUSD = "available", "", now, &total
	return reading
}
