//go:build !windows

package installbase

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

// UserVariable reads one user-scope variable from the file
// UserEnvFileVariable names, and finds none without it.
func UserVariable(name string) (string, bool, error) {
	if path := os.Getenv(UserEnvFileVariable); path != "" {
		return fileEnvStore{path: path}.Get(name)
	}
	return "", false, nil
}

type unsupportedEnvStore struct{}

var errUnsupported = errors.New("install: user-scope environment variables are Windows-only")

func (unsupportedEnvStore) Get(string) (string, bool, error) { return "", false, errUnsupported }
func (unsupportedEnvStore) Set(string, string) error         { return errUnsupported }
func (unsupportedEnvStore) Unset(string) error               { return errUnsupported }
func (unsupportedEnvStore) Names() ([]string, error)         { return nil, errUnsupported }
func (unsupportedEnvStore) Broadcast() error                 { return errUnsupported }
