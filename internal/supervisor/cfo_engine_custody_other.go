//go:build !windows

package supervisor

import (
	"errors"

	"github.com/fpresta0607/code-goblins/internal/host"
)

func cfoJobActivity(host.Record) (string, error) {
	return "", errors.New("the CFO's terminal job cannot be inspected on this system")
}
