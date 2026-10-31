package memory

import (
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

var tierMarker = regexp.MustCompile(`<!--(a|p):([0-9]{4}-[0-9]{2}-[0-9]{2})-->|<!--(P|g)-->|<!--t:([^<>\s]+)-->`)

func pinnedReferences(dataDir string) (map[string]bool, error) {
	content, err := ReadData(dataDir, "memory/MEMORY.md")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	pinned := make(map[string]bool)
	link := regexp.MustCompile(`\[[^\]]*\]\(<?([^<>\s)]+)>?\)`)
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.Contains(line, "<!--P-->") {
			continue
		}
		for _, match := range link.FindAllStringSubmatch(line, -1) {
			pinned[path.Join("memory", match[1])] = true
		}
	}
	return pinned, nil
}

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
		if match[4] != "" {
			if status.PendingCapture != "" {
				return Status{}, fmt.Errorf("conflicting pending-capture markers")
			}
			status.PendingCapture = match[4]
			continue
		}
		if hasTier {
			return Status{}, fmt.Errorf("conflicting tier markers; catalog each entry separately")
		}
		hasTier = true
		switch match[3] {
		case "P":
			status.Tier = "pinned"
		case "g":
			status.Tier = "aging"
			status.IsGrace = true
		default:
			date, err := time.Parse(time.DateOnly, match[2])
			if err != nil || date.After(now) {
				return Status{}, fmt.Errorf("invalid reinforcement date %q", match[2])
			}
			status.Reinforced = match[2]
			status.Tier = "aging"
			horizon := 30 * 24 * time.Hour
			if match[1] == "p" {
				status.Tier = "perishable"
				horizon = 7 * 24 * time.Hour
			}
			status.IsStale = !now.Before(date.Add(horizon))
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
