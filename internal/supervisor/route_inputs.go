package supervisor

import (
	"errors"
	"io/fs"

	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/routing"
)

// RouteInputs is what a spawn routes with: the lane table and, when the
// project has a manifest, that manifest for its budgets and capsule.
type RouteInputs struct {
	Table        routing.Table
	Manifest     project.Manifest
	ManifestPath string // "" when the project has no manifest
}

// LoadRouteInputs reads the fleet lane table from data/routing.json and the
// project's manifest when it has one. A manifest that defines lanes
// overrides the fleet table for that project. A project without a manifest
// routes from the fleet table alone; a manifest that exists but does not
// load is an error, never silently ignored.
func LoadRouteInputs(dataDir, checkout string) (RouteInputs, error) {
	policy, err := routing.Load(dataDir)
	if err != nil {
		return RouteInputs{}, err
	}
	table, err := policy.LaneTable()
	if err != nil {
		return RouteInputs{}, err
	}
	in := RouteInputs{Table: table}
	if checkout == "" {
		return in, nil
	}
	m, path, err := project.LoadForCheckout(dataDir, checkout)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return in, nil
	case err != nil:
		return RouteInputs{}, err
	}
	in.Manifest, in.ManifestPath = m, path
	if len(m.Routing.Lanes) > 0 {
		in.Table = routing.Table{Lanes: map[string]routing.ExecutionLane{}, DefaultLane: m.Routing.DefaultLane, EscalateTo: m.Routing.EscalateTo, Source: "project override " + path}
		for name, l := range m.Routing.Lanes {
			in.Table.Lanes[name] = routing.ExecutionLane{Name: name, Harness: l.Harness, Model: l.Model, Effort: l.Effort}
		}
	}
	return in, nil
}
