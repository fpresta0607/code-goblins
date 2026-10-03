package supervisor

import (
	"net/url"
	"slices"
	"strings"
	"time"
)

type CIDuration struct {
	Repository string    `json:"repository"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Seconds    int64     `json:"duration_seconds"`
	FinishedAt time.Time `json:"finished_at"`
}

func recordCIDuration(watched *fleetWakes, target, kind, name string, started, finished time.Time) {
	if started.IsZero() || finished.IsZero() || finished.Before(started) {
		return
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host != "github.com" {
		return
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return
	}
	measured := CIDuration{Repository: parts[0] + "/" + parts[1], Kind: kind, Name: name, URL: target, Seconds: int64(finished.Sub(started) / time.Second), FinishedAt: finished}
	if !slices.Contains(watched.Durations, measured) {
		watched.Durations = append(watched.Durations, measured)
		watched.Durations = watched.Durations[max(0, len(watched.Durations)-200):]
	}
}
