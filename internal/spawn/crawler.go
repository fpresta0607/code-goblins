package spawn

import (
	"fmt"

	"github.com/fpresta0607/code-goblins/internal/crawler"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// crawlerLeftOut reports whether a new task's terminal goes without the
// crawler connector: its brief leaves it out with connectors: none, or it is
// a helper whose parent goes without it, since a helper never carries more
// than its parent.
func (s Service) crawlerLeftOut(req Request, brief []byte) (bool, error) {
	isLeftOut, err := crawler.IsLeftOutBy(string(brief))
	if err != nil {
		return false, fmt.Errorf("spawn: %w", err)
	}
	if isLeftOut || req.Parent == "" {
		return isLeftOut, nil
	}
	parent, err := state.ReadTaskMeta(s.StateDir, req.Parent)
	if err != nil {
		return false, fmt.Errorf("spawn: read the record of the helper's parent %s: %w", req.Parent, err)
	}
	return parent.IsCrawlerLeftOut, nil
}
