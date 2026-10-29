package supervisor

import (
	"cmp"
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

// CIUsual is the median time a repository's finished CI runs or deploys of
// one kind took.
type CIUsual struct {
	Repository string  `json:"repository"`
	Kind       string  `json:"kind"`
	Seconds    float64 `json:"seconds"`
}

// usualCIDurations is the median of each repository's measured runs of each
// kind, by repository and then kind. A card paused on CI shows only that, and
// the 200 runs it is taken from were 40 KB of every board's first snapshot,
// sent whole again whenever one finished (#1611).
func usualCIDurations(durations []CIDuration) []CIUsual {
	measured := map[CIUsual][]int64{}
	for _, run := range durations {
		key := CIUsual{Repository: run.Repository, Kind: run.Kind}
		measured[key] = append(measured[key], run.Seconds)
	}
	usual := make([]CIUsual, 0, len(measured))
	for each, seconds := range measured {
		slices.Sort(seconds)
		middle := len(seconds) / 2
		each.Seconds = float64(seconds[middle])
		if len(seconds)%2 == 0 {
			each.Seconds = float64(seconds[middle-1]+seconds[middle]) / 2
		}
		usual = append(usual, each)
	}
	slices.SortFunc(usual, func(one, other CIUsual) int {
		return cmp.Or(strings.Compare(one.Repository, other.Repository), strings.Compare(one.Kind, other.Kind))
	})
	return usual
}
