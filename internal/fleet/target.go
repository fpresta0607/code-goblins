// Package fleet resolves and operates local Code Goblin Herdr targets.
package fleet

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// TargetResolver resolves a user selector to one Herdr target and its local
// task metadata. Explicit targets do not have associated local metadata.
type TargetResolver interface {
	Resolve(ctx context.Context, raw string) (herdr.Target, state.TaskMeta, error)
}

// Resolver resolves local task selectors from the CFO state directory, or an
// explicit canonical Herdr target in session:pane form.
type Resolver struct {
	StateDir string
}

// Resolve accepts a task ID, gb-<task-id>, or an explicit Herdr target. A
// target only splits on its first colon because pane IDs can contain colons.
func (r Resolver) Resolve(_ context.Context, raw string) (herdr.Target, state.TaskMeta, error) {
	if target, err := herdr.ParseTarget(raw); err == nil {
		return target, state.TaskMeta{}, nil
	}
	if strings.Contains(raw, ":") {
		return herdr.Target{}, state.TaskMeta{}, fmt.Errorf("fleet: target %q must be <session>:<pane-id>", raw)
	}
	if r.StateDir == "" {
		return herdr.Target{}, state.TaskMeta{}, errors.New("fleet: state directory is required to resolve task selectors")
	}

	id := raw
	meta, err := state.ReadTaskMeta(r.StateDir, id)
	if errors.Is(err, os.ErrNotExist) && strings.HasPrefix(raw, "gb-") {
		id = strings.TrimPrefix(raw, "gb-")
		meta, err = state.ReadTaskMeta(r.StateDir, id)
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return herdr.Target{}, state.TaskMeta{}, fmt.Errorf("fleet: unknown selector %q; provide <session>:<pane-id> for an explicit Herdr pane", raw)
		}
		return herdr.Target{}, state.TaskMeta{}, fmt.Errorf("fleet: read selector %q: %w", raw, err)
	}
	if meta.Backend != "herdr" {
		return herdr.Target{}, state.TaskMeta{}, fmt.Errorf("fleet: selector %q does not identify a Herdr task", raw)
	}
	if meta.HerdrSession == "" || meta.HerdrPaneID == "" {
		return herdr.Target{}, state.TaskMeta{}, fmt.Errorf("fleet: selector %q has incomplete Herdr metadata", raw)
	}
	return herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}, meta, nil
}
