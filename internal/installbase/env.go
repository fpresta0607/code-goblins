// Package installbase is what an install reads before it changes anything:
// the home it puts Code Goblins in, the user-scope environment that names
// that home, and the choice of Start at login the home keeps. The setup reads
// these alone, and this package imports nothing of the repository beyond
// internal/home, internal/fsx and internal/execx, since each package the
// setup links is a way its file changes between releases, and a changed file
// starts SmartScreen's reputation over.
package installbase

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// HomeVariable is the variable that tells cfo where the fleet lives, and the
// one the installed hooks resolve the binary through.
const HomeVariable = "CFO_HOME"

// UserEnvFileVariable names a JSON file that stands in for the user-scope
// environment, HKCU\Environment on Windows. With it set, cfo reads and writes
// that file in place of the user scope, so a test or a proof in a scratch
// profile never writes the machine's own user environment.
const UserEnvFileVariable = "CFO_USER_ENV_FILE"

// fileEnvStore keeps user-scope variables in a JSON object, comparing names
// without case as Windows does. A missing file is an empty scope.
type fileEnvStore struct {
	path string
}

func (s fileEnvStore) read() (map[string]string, error) {
	data, err := fsx.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("install: read %s: %w", s.path, err)
	}
	return values, nil
}

func (s fileEnvStore) write(values map[string]string) error {
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o600)
}

func (s fileEnvStore) Get(name string) (string, bool, error) {
	values, err := s.read()
	if err != nil {
		return "", false, err
	}
	for key, value := range values {
		if strings.EqualFold(key, name) {
			return value, true, nil
		}
	}
	return "", false, nil
}

func (s fileEnvStore) Set(name, value string) error {
	values, err := s.read()
	if err != nil {
		return err
	}
	for key := range values {
		if strings.EqualFold(key, name) {
			delete(values, key)
		}
	}
	values[name] = value
	return s.write(values)
}

func (s fileEnvStore) Unset(name string) error {
	values, err := s.read()
	if err != nil {
		return err
	}
	for key := range values {
		if strings.EqualFold(key, name) {
			delete(values, key)
		}
	}
	return s.write(values)
}

// Broadcast has nothing to publish: no process reads this file on its own.
func (fileEnvStore) Broadcast() error {
	return nil
}

// EnvStore reads and writes user-scope (persistent) environment variables.
//
// Get returns the raw, unexpanded value: a user PATH routinely holds
// `%USERPROFILE%\...` entries, and reading the expanded process value and
// writing it back would bake today's expansion into the registry forever.
// Broadcast publishes the changes so newly started processes see them
// without a sign-out.
type EnvStore interface {
	Get(name string) (value string, set bool, err error)
	Set(name, value string) error
	Unset(name string) error
	Broadcast() error
}

// SameDirectory compares two directory paths the way Windows does: case
// insensitively, with either separator, and with a trailing separator or
// surrounding quotes meaning nothing.
func SameDirectory(left, right string) bool {
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		value = strings.ReplaceAll(value, "/", `\`)
		value = strings.TrimRight(value, `\`)
		return strings.ToLower(value)
	}
	return normalize(left) == normalize(right)
}
