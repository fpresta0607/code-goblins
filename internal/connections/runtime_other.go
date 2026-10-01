//go:build !windows

package connections

import (
	"context"
	"errors"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func nativeRuntime(ctx context.Context, stateDir string, meta state.TaskMeta) ([]string, []string, error) {
	return nil, nil, errors.New("Native runtime checks require Windows.")
}
