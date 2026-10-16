package spawn

import (
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/routing"
)

func (s Service) subagentRoutes(kind harness.Kind, model string) (map[string]routing.ExecutionLane, error) {
	policy, err := routing.Load(filepath.Join(s.HomeRoot, "data"))
	if err != nil {
		return nil, err
	}
	return policy.SubagentRoutes(string(kind), model)
}
