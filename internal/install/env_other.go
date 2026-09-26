//go:build !windows

package install

import (
	"errors"
	"os"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// NewEnvStore has no non-Windows equivalent: code-goblins is a
// Windows-native fleet, and user-scope environment variables are a Windows
// registry concept with no portable counterpart. The file UserEnvFileVariable
// names stands in for it here as on Windows.
func NewEnvStore(execx.Runner) EnvStore {
	if path := os.Getenv(UserEnvFileVariable); path != "" {
		return fileEnvStore{path: path}
	}
	return unsupportedEnvStore{}
}

type unsupportedEnvStore struct{}

var errUnsupported = errors.New("install: user-scope environment variables are Windows-only")

func (unsupportedEnvStore) Get(string) (string, bool, error) { return "", false, errUnsupported }
func (unsupportedEnvStore) Set(string, string) error         { return errUnsupported }
func (unsupportedEnvStore) Unset(string) error               { return errUnsupported }
func (unsupportedEnvStore) Broadcast() error                 { return errUnsupported }
