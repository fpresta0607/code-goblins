//go:build !windows

package supervisor

import (
	"errors"

	"github.com/fpresta0607/code-goblins/internal/host"
)

func ceoJobActivity(host.Record) (string, error) {
	return "", errors.New("the CEO's terminal job cannot be inspected on this system")
}
