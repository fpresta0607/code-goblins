package quota

import (
	"fmt"
	"slices"
	"time"
)

// StartFloor applies only to new starts, leaving running sessions to the
// fleet's pause and switch policy. Only windows that bound this model count.
func (r Report) StartFloor(harness, model string, floor float64, now time.Time) (bool, string) {
	provider, ok := r.Providers[harness]
	if floor <= 0 || !ok || provider.Stale || !provider.Known || !r.GeneratedAt.IsZero() && now.Sub(r.GeneratedAt) > MaxAge {
		return false, ""
	}
	scope, ok := provider.Scopes["model:"+model]
	if !ok || model == "" {
		scope = provider.Scopes["all_models"]
	}
	for _, window := range provider.Windows {
		if !slices.Contains(scope.BoundedBy, window.ID) || window.PercentUsed < 100-floor || window.PercentUsed > 100 {
			continue
		}
		if window.Kind != "session" && window.Kind != "weekly" && window.Kind != "model" {
			continue
		}
		if !window.ResetsAt.IsZero() && !now.Before(window.ResetsAt) {
			continue
		}
		note := fmt.Sprintf("%s %s at the %g percent start floor", harness, window.ID, floor)
		if !window.ResetsAt.IsZero() {
			note += ", resets " + window.ResetsAt.UTC().Format(time.RFC3339)
		}
		return true, note
	}
	return false, ""
}
