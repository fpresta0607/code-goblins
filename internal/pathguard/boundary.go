// Package pathguard is the filesystem rule a goblin's tool calls are held
// to: they never read or write the credential store or
// ~/.ssh, and they write only inside their worktree, their scratch folder and
// the caches they were given. It is a boundary check on the paths a tool call
// names, made before the call runs, so a refused call writes nothing. It is
// not a sandbox: a program the call starts reads and writes as the user does,
// which is what a sandbox task's container is for (internal/sandbox).
package pathguard

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Boundary is what one session's tool calls may reach.
type Boundary struct {
	// Writable are the folders a tool call may write in: its worktree, its
	// scratch folder and the caches it was given.
	Writable []string
	// Secrets are the folders a tool call may neither read nor write, each
	// with the name a refusal gives it.
	Secrets []Secret
	// Cwd is the folder a relative path is read from.
	Cwd string
	// UserHome is the folder ~ names.
	UserHome string
	// Getenv reads the variables a path may name, as $HOME or %APPDATA%.
	Getenv func(string) string
}

// Secret is a folder no tool call reaches, and what a refusal calls it.
type Secret struct {
	Path string
	Name string
}

// Refusal is why a call that reads path, or writes it when isWrite, is
// refused, naming the path, or empty when the call may go on.
func (b Boundary) Refusal(path string, isWrite bool) string {
	resolved, ok := b.resolve(path)
	if !ok {
		return ""
	}
	for _, secret := range b.Secrets {
		if within(resolved, secret.Path) {
			return "it reaches " + resolved + ", which is in " + secret.Name + ", which no goblin or gate step reads or writes"
		}
	}
	if !isWrite {
		return ""
	}
	for _, root := range b.Writable {
		if root != "" && within(resolved, root) {
			return ""
		}
	}
	return "it writes " + resolved + ", which is outside its worktree, its scratch folder and the caches it was given"
}

// resolve is the absolute, link-free form of path as the session would
// reach it, or false for a word that names no path.
func (b Boundary) resolve(path string) (string, bool) {
	path = b.expand(strings.Trim(strings.TrimSpace(path), `"'`))
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		switch volume := filepath.VolumeName(path); {
		case strings.HasPrefix(path, `\`):
			path = filepath.VolumeName(b.Cwd) + path
		case volume != "":
			path = volume + `\` + path[len(volume):]
		case b.Cwd != "":
			path = filepath.Join(b.Cwd, path)
		}
		if !filepath.IsAbs(path) {
			return "", false
		}
	}
	return realPath(filepath.Clean(path)), true
}

// expand reads what a shell would before it reaches the path: ~, the
// variables that name the user's folders, and Git Bash's /c/ drives.
func (b Boundary) expand(path string) string {
	getenv := b.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	switch {
	case path == "~":
		path = b.UserHome
	case strings.HasPrefix(path, "~/"), strings.HasPrefix(path, `~\`):
		path = b.UserHome + path[1:]
	}
	for _, name := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP"} {
		value := getenv(name)
		if name == "HOME" && value == "" {
			value = b.UserHome
		}
		for _, form := range []string{"${" + name + "}", "$env:" + name, "%" + name + "%", "$" + name} {
			if len(path) >= len(form) && strings.EqualFold(path[:len(form)], form) && (len(path) == len(form) || path[len(form)] == '/' || path[len(form)] == '\\') {
				path = value + path[len(form):]
			}
		}
	}
	switch {
	case len(path) >= 2 && path[0] == '/' && isLetter(path[1]) && (len(path) == 2 || path[2] == '/'):
		path = strings.ToUpper(path[1:2]) + ":/" + strings.TrimPrefix(path[2:], "/")
	case path == "/tmp" || strings.HasPrefix(path, "/tmp/"):
		// Git Bash mounts the folder its first shell's TMP names as /tmp.
		path = getenv("TMP") + path[len("/tmp"):]
	}
	return filepath.FromSlash(path)
}

// realPath follows the links on path's longest existing part, so a link in
// the worktree to a folder outside it reads as that folder.
func realPath(path string) string {
	rest := ""
	for current := path; ; {
		if real, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(real, rest)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return path
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		rest = filepath.Join(filepath.Base(current), rest)
		current = parent
	}
}

// within reports whether path is root or inside it, comparing without case
// as Windows does.
func within(path, root string) bool {
	root = realPath(filepath.Clean(root))
	if strings.EqualFold(path, root) {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return len(path) > len(prefix) && strings.EqualFold(path[:len(prefix)], prefix)
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
