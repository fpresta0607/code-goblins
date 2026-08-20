//go:build !windows

package install

import (
	"errors"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// NewEnvStore has no non-Windows equivalent: code-goblins is a
// Windows-native fleet, and user-scope environment variables are a Windows
// registry concept with no portable counterpart.
func NewEnvStore(execx.Runner) EnvStore {
	return unsupportedEnvStore{}
}

type unsupportedEnvStore struct{}

var errUnsupported = errors.New("install: user-scope environment variables are Windows-only")

func (unsupportedEnvStore) Get(string) (string, bool, error) { return "", false, errUnsupported }
func (unsupportedEnvStore) Set(string, string) error         { return errUnsupported }
func (unsupportedEnvStore) Unset(string) error               { return errUnsupported }
func (unsupportedEnvStore) Broadcast() error                 { return errUnsupported }
