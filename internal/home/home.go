// Package home resolves the CFO home directory and decides whether a
// directory is a genuine primary fleet home. A home without a state/
// directory is a dev checkout: every hook must stay inert there.
package home

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Home names the three directories everything else keys on.
type Home struct {
	Root  string
	State string
	Data  string
}

// Resolve returns the home from CFO_HOME or the working directory.
// It never creates directories.
func Resolve() (Home, error) {
	root := os.Getenv("CFO_HOME")
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return Home{}, err
		}
		root = wd
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Home{}, err
	}
	h := Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if s := os.Getenv("CFO_STATE_OVERRIDE"); s != "" {
		h.State = s
	}
	return h, nil
}

// IsPrimary reports whether h is a genuine primary home: AGENTS.md present,
// state/ present, and a plain (non-worktree) git checkout. It never creates
// anything; any failure to confirm is false, never an error.
func IsPrimary(h Home) bool {
	if fi, err := os.Stat(filepath.Join(h.Root, "AGENTS.md")); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if fi, err := os.Stat(h.State); err != nil || !fi.IsDir() {
		return false
	}
	gitDir, err := gitPath(h.Root, "--git-dir")
	if err != nil {
		return false
	}
	commonDir, err := gitPath(h.Root, "--git-common-dir")
	if err != nil {
		return false
	}
	return strings.EqualFold(gitDir, commonDir)
}

func gitPath(root, flag string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", flag).Output()
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p), nil
}
