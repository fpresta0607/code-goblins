package fleet

import (
	"context"
	"errors"
	"fmt"

	"github.com/fpresta0607/code-goblins/internal/herdr"
)

const (
	defaultTailLines = 40
	invalidTailLines = 200
)

// Peeker returns a bounded terminal tail for a resolved Herdr pane.
type Peeker struct {
	Resolve TargetResolver
	Herdr   *herdr.Client
}

// Tail returns a local tail only. Herdr's client always requests at least 200
// lines to work around its viewport bug before it trims the output.
func (p Peeker) Tail(ctx context.Context, raw string, lines int) (string, error) {
	if p.Resolve == nil {
		return "", errors.New("fleet: target resolver is required")
	}
	if p.Herdr == nil {
		return "", errors.New("fleet: Herdr client is required")
	}
	if lines == 0 {
		lines = defaultTailLines
	} else if lines < 0 {
		lines = invalidTailLines
	}
	target, _, err := p.Resolve.Resolve(ctx, raw)
	if err != nil {
		return "", err
	}
	output, err := p.Herdr.Capture(ctx, target, lines, false)
	if err != nil {
		return "", fmt.Errorf("fleet: peek %s: %w", target, err)
	}
	return output, nil
}
