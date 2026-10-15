package cirunner

import (
	"context"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// recorder keeps every command HyperV runs, and how long each had, and
// answers each with result.
type recorder struct {
	ran    []execx.Request
	limits []time.Duration
	result execx.Result
}

func (r *recorder) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	r.ran = append(r.ran, request)
	limit := time.Duration(-1)
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		limit = time.Until(deadline)
	}
	r.limits = append(r.limits, limit)
	return r.result, nil
}

// script is the script a request ran.
func script(request execx.Request) string {
	return request.Args[len(request.Args)-1]
}
