package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

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
	data, err := os.ReadFile(s.path)
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

// pathSeparator is the user PATH's entry separator.
const pathSeparator = ";"

// pathEntries splits a raw PATH, dropping the empty segments a trailing or
// doubled separator leaves behind.
func pathEntries(raw string) []string {
	entries := make([]string, 0, 8)
	for _, entry := range strings.Split(raw, pathSeparator) {
		if strings.TrimSpace(entry) != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// samePathEntry reports whether a raw PATH entry names dir. The entry is
// expanded first, because an entry written as `%USERPROFILE%\bin` and a
// resolved absolute path are the same directory and must not be added twice.
func samePathEntry(entry, dir string) bool {
	return sameDirectory(entry, dir) || sameDirectory(expandWindowsVars(entry), dir)
}

// sameDirectory compares two directory paths the way Windows does: case
// insensitively, with either separator, and with a trailing separator or
// surrounding quotes meaning nothing.
func sameDirectory(left, right string) bool {
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		value = strings.ReplaceAll(value, "/", `\`)
		value = strings.TrimRight(value, `\`)
		return strings.ToLower(value)
	}
	return normalize(left) == normalize(right)
}

// expandWindowsVars expands `%NAME%` references from the process
// environment. An unset name is left as written rather than blanked, so an
// entry we cannot resolve can never accidentally compare equal to a
// different directory.
func expandWindowsVars(value string) string {
	if !strings.Contains(value, "%") {
		return value
	}
	var out strings.Builder
	rest := value
	for {
		open := strings.Index(rest, "%")
		if open < 0 {
			out.WriteString(rest)
			return out.String()
		}
		closing := strings.Index(rest[open+1:], "%")
		if closing < 0 {
			out.WriteString(rest)
			return out.String()
		}
		name := rest[open+1 : open+1+closing]
		out.WriteString(rest[:open])
		if replacement, ok := os.LookupEnv(name); ok && name != "" {
			out.WriteString(replacement)
		} else {
			out.WriteString("%" + name + "%")
		}
		rest = rest[open+closing+2:]
	}
}
