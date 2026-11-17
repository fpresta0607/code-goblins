//go:build linux || darwin

package verify

import (
	"context"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// strayWorkers finds none: a worker takes its tree's mark and its process
// group as it starts (execx.StartNamedTree), so no controller can end
// between its start and its joining the tree, as on Windows, where a worker
// is put in its job only once it has started.
func (Custody) strayWorkers(context.Context) ([]proc.Entry, error) {
	return nil, nil
}

// instanceOf is which run of its program pid is (proc.Instance).
func instanceOf(pid int) (string, error) {
	return proc.Instance(pid)
}
