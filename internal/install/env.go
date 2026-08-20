package install

import (
	"os"
	"strings"
)

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
