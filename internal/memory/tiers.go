package memory

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var tierMarker = regexp.MustCompile(`<!--(a|p):([0-9]{4}-[0-9]{2}-[0-9]{2})(/[0-9]+)?-->|<!--(P|g)-->|<!--t:([^<>\s]+)-->`)

type Status struct {
	Tier           string `json:"tier"`
	Reinforced     string `json:"reinforced,omitempty"`
	IsStale        bool   `json:"is_stale"`
	IsGrace        bool   `json:"is_grace"`
	IsLegacy       bool   `json:"is_legacy"`
	PendingCapture string `json:"pending_capture,omitempty"`
}

// Inspect follows Stow's default date horizons. Reading never advances decay.
func Inspect(content []byte, authority string, now time.Time) (Status, error) {
	status := Status{Tier: "aging"}
	if authority == "ruling" || authority == "history" {
		status.Tier = "pinned"
	}
	hasTier := false
	text := string(content)
	for _, match := range tierMarker.FindAllStringSubmatch(text, -1) {
		if match[5] != "" {
			if status.PendingCapture != "" {
				return Status{}, fmt.Errorf("conflicting pending-capture markers")
			}
			status.PendingCapture = match[5]
			continue
		}
		if hasTier {
			return Status{}, fmt.Errorf("conflicting tier markers; catalog each entry separately")
		}
		hasTier = true
		switch match[4] {
		case "P":
			status.Tier = "pinned"
		case "g":
			status.IsGrace = true
		default:
			date, err := time.Parse(time.DateOnly, match[2])
			if err != nil || date.After(now) {
				return Status{}, fmt.Errorf("invalid reinforcement date %q", match[2])
			}
			status.Reinforced = match[2]
			horizon := 30 * 24 * time.Hour
			if match[1] == "p" {
				status.Tier = "perishable"
				horizon = 7 * 24 * time.Hour
			}
			status.IsStale = !now.Before(date.Add(horizon))
			if match[3] != "" {
				if _, err := strconv.Atoi(strings.TrimPrefix(match[3], "/")); err != nil {
					return Status{}, fmt.Errorf("invalid pass counter")
				}
			}
		}
	}
	if !hasTier && status.Tier == "aging" {
		status.IsLegacy = true
	}
	for _, prefix := range []string{"<!--a:", "<!--p:", "<!--P", "<!--g", "<!--t:"} {
		cleaned := tierMarker.ReplaceAllString(text, "")
		if strings.Contains(cleaned, prefix) {
			return Status{}, fmt.Errorf("malformed memory marker")
		}
	}
	return status, nil
}
