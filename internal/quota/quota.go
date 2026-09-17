// Package quota interprets what quota-axi knows about this machine's
// provider subscriptions, so a spawn can tell whether the lane it wants has
// headroom before dispatching a goblin into a wall.
package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

// MaxAge is how old a snapshot may be before it is no evidence at all.
const MaxAge = time.Hour

// Report is the interpreted snapshot: every provider quota-axi measured.
type Report struct {
	GeneratedAt time.Time
	Providers   map[string]Provider
}

// Provider is one subscription's headroom by scope.
type Provider struct {
	Name string
	// Stale is quota-axi's own verdict that its numbers are old.
	Stale bool
	// Known is whether quota-axi understands this provider's quota semantics
	// at all; cursor and copilot report windows it cannot interpret.
	Known bool
	// Scopes are the effective availabilities: "all_models" plus any
	// model-scoped window such as "model:fable".
	Scopes map[string]Scope
	// Resets maps a window id to when it resets, for naming a wall's end.
	Resets map[string]time.Time
}

// Scope is the effective headroom for one scope of a provider.
type Scope struct {
	Name string
	// Known is whether the percentage is measured; an unknown percentage
	// never reads as zero.
	Known            bool
	PercentRemaining int
	// Runway is quota-axi's verdict: through_reset, projected_exhaustion,
	// exhausted_now, or unknown.
	Runway               string
	UsableRunwaySeconds  int
	ProjectedExhaustedAt time.Time
	// ResetsAt is when the limiting window resets; zero when unknown.
	ResetsAt time.Time
}

// Headroom is what the report says about one harness and model.
type Headroom struct {
	Provider string
	Scope    string
	// Known is false when there is no evidence for this harness or model:
	// the provider is absent, stale, or not understood. No evidence never
	// blocks a spawn.
	Known bool
	// Exhausted is the one finding that makes a lane unusable: quota-axi
	// says exhausted_now, or a measured zero percent remaining.
	Exhausted            bool
	PercentRemaining     int
	Runway               string
	ResetsAt             time.Time
	ProjectedExhaustedAt time.Time
}

// Headroom reads the evidence for a harness and model. The harness name is
// the provider name for every harness quota-axi measures (claude, codex,
// kimi); a model-scoped window is preferred when quota-axi reports one for
// the model, otherwise the all-models scope bounds it.
func (r Report) Headroom(harness, model string) Headroom {
	h := Headroom{Provider: harness}
	p, ok := r.Providers[harness]
	if !ok || p.Stale || !p.Known {
		return h
	}
	scope, ok := p.Scopes["model:"+model]
	if !ok || model == "" {
		scope, ok = p.Scopes["all_models"]
	}
	if !ok {
		return h
	}
	h.Scope = scope.Name
	h.Known = true
	h.PercentRemaining = scope.PercentRemaining
	h.Runway = scope.Runway
	h.ResetsAt = scope.ResetsAt
	h.ProjectedExhaustedAt = scope.ProjectedExhaustedAt
	h.Exhausted = scope.Runway == "exhausted_now" || (scope.Known && scope.PercentRemaining <= 0)
	return h
}

// String is the one-line finding a spawn prints beside the lane it applies to.
func (h Headroom) String() string {
	if !h.Known {
		return h.Provider + ": no quota evidence"
	}
	if h.Exhausted {
		reset := "reset time unknown"
		if !h.ResetsAt.IsZero() {
			reset = "resets " + h.ResetsAt.UTC().Format(time.RFC3339)
		}
		return fmt.Sprintf("%s %s exhausted_now, %s", h.Provider, h.Scope, reset)
	}
	line := fmt.Sprintf("%s %s %d%% remaining, runway %s", h.Provider, h.Scope, h.PercentRemaining, h.Runway)
	if h.Runway == "projected_exhaustion" && !h.ProjectedExhaustedAt.IsZero() {
		line += ", projected exhausted " + h.ProjectedExhaustedAt.UTC().Format(time.RFC3339)
	}
	return line
}

// Reader runs quota-axi and interprets its answer.
type Reader struct {
	Commands execx.Runner
	// Now is the clock the snapshot's age is judged against; nil means the
	// wall clock.
	Now func() time.Time
}

// Read returns the report, or the one-line reason the check was skipped. A
// missing, failing, unparseable, or stale quota-axi is no evidence and never
// a block: the caller routes as configured and says the check was skipped.
func (r Reader) Read(ctx context.Context) (Report, string) {
	payload, err := axi.Quota{Commands: r.Commands}.JSON(ctx)
	if err != nil {
		return Report{}, err.Error()
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	report, err := Parse(payload, now())
	if err != nil {
		return Report{}, err.Error()
	}
	return report, ""
}

// payload is the part of quota-axi's JSON (schemaVersion 3) a spawn reads.
// Numbers arrive as json.RawMessage because quota-axi prints "unknown" where
// it has no measurement, and an unknown must not fail the whole snapshot.
type payload struct {
	GeneratedAt string `json:"generatedAt"`
	Providers   []struct {
		Provider string `json:"provider"`
		Windows  []struct {
			ID       string `json:"id"`
			ResetsAt string `json:"resetsAt"`
		} `json:"windows"`
		State struct {
			Stale bool `json:"stale"`
		} `json:"state"`
		QuotaSemantics struct {
			Status                string `json:"status"`
			EffectiveAvailability []struct {
				Scope                     string          `json:"scope"`
				Status                    string          `json:"status"`
				EffectivePercentRemaining json.RawMessage `json:"effectivePercentRemaining"`
				Runway                    struct {
					Status               string          `json:"status"`
					UsableRunwaySeconds  json.RawMessage `json:"usableRunwaySeconds"`
					LimitingWindowID     string          `json:"limitingWindowId"`
					ProjectedExhaustedAt string          `json:"projectedExhaustedAt"`
				} `json:"runway"`
			} `json:"effectiveAvailability"`
		} `json:"quotaSemantics"`
	} `json:"providers"`
}

// Parse interprets quota-axi --json. It fails on unparseable input, on a
// snapshot with no generation time, and on one older than MaxAge, so all
// three read as "no evidence" to the caller.
func Parse(data []byte, now time.Time) (Report, error) {
	var in payload
	if err := json.Unmarshal(data, &in); err != nil {
		return Report{}, fmt.Errorf("quota-axi: unparseable snapshot: %w", err)
	}
	generated, err := time.Parse(time.RFC3339Nano, in.GeneratedAt)
	if err != nil {
		return Report{}, errors.New("quota-axi: snapshot has no generation time")
	}
	if now.Sub(generated) > MaxAge {
		return Report{}, fmt.Errorf("quota-axi: snapshot is stale (generated %s)", generated.UTC().Format(time.RFC3339))
	}
	report := Report{GeneratedAt: generated, Providers: map[string]Provider{}}
	for _, p := range in.Providers {
		provider := Provider{Name: p.Provider, Stale: p.State.Stale, Known: p.QuotaSemantics.Status == "known", Scopes: map[string]Scope{}, Resets: map[string]time.Time{}}
		for _, w := range p.Windows {
			if at, err := time.Parse(time.RFC3339Nano, w.ResetsAt); err == nil {
				provider.Resets[w.ID] = at
			}
		}
		for _, e := range p.QuotaSemantics.EffectiveAvailability {
			scope := Scope{Name: e.Scope, Runway: e.Runway.Status, ResetsAt: provider.Resets[e.Runway.LimitingWindowID]}
			if percent, ok := number(e.EffectivePercentRemaining); ok && e.Status == "known" {
				scope.Known, scope.PercentRemaining = true, int(percent)
			}
			if seconds, ok := number(e.Runway.UsableRunwaySeconds); ok {
				scope.UsableRunwaySeconds = int(seconds)
			}
			if at, err := time.Parse(time.RFC3339Nano, e.Runway.ProjectedExhaustedAt); err == nil {
				scope.ProjectedExhaustedAt = at
			}
			provider.Scopes[e.Scope] = scope
		}
		report.Providers[p.Provider] = provider
	}
	return report, nil
}

// number reads a JSON number, and reports false for anything else (absent,
// null, or quota-axi's "unknown").
func number(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
