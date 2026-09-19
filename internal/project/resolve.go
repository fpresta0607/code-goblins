package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A bare name that cannot be placed fails for one of four reasons, and a
// caller that treats a project argument as a credential scope rather than a
// checkout has to tell them apart: an unset root, an unknown name and a folder
// that is no checkout all leave the name as written, while an ambiguous one is
// a question only the operator can answer. A root that is recorded but cannot
// be read is none of these: it comes back unwrapped, because which of the four
// the name is cannot be known, and every caller refuses.
var (
	ErrRootUnset   = errors.New("projects root is not set")
	ErrUnknown     = errors.New("no such checkout")
	ErrNotCheckout = errors.New("not a git checkout")
	ErrAmbiguous   = errors.New("ambiguous project name")
)

// Resolve turns a --project argument into a checkout directory.
//
// A path wins and comes back exactly as written: anything with a separator, a
// volume, or a dot segment is the operator naming a directory, and what that
// directory must be stays the caller's rule, as it was before names existed.
//
// A bare name is looked up among the directories of the machine's projects
// root, which root reads and which is never consulted for a path. The first
// tier with a match decides: the name as written, then the name ignoring case,
// then the name as the leading words of a folder (`precisiondocs` for
// `PrecisionDocs-AI`). The last tier stops at a word boundary, so a fragment
// that merely resembles a folder names nothing. One match is the checkout,
// provided it holds a .git; more than one is refused by name.
//
// The folder is the answer, never a mapping: the credential scope is the
// resolved folder's name on every machine, so a name and its path agree.
func Resolve(arg string, root func() (string, error)) (string, error) {
	if !isBareName(arg) {
		return arg, nil
	}
	dir := ""
	if root != nil {
		var err error
		if dir, err = root(); err != nil {
			return "", fmt.Errorf("project %q: read the projects root: %w", arg, err)
		}
	}
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("project %q is a bare name and the %w: run `cfo install --projects-root <dir>` with the folder that holds your checkouts, or pass a path", arg, ErrRootUnset)
	}
	dir = filepath.Clean(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("project %q: read the projects root %s: %v", arg, dir, err)
	}
	var folders []string
	for _, entry := range entries {
		// Stat rather than the entry's own type, so a junction to a checkout
		// kept on another drive counts as the directory it is.
		if info, err := os.Stat(filepath.Join(dir, entry.Name())); err == nil && info.IsDir() {
			folders = append(folders, entry.Name())
		}
	}

	for _, matches := range []func(folder string) bool{
		func(folder string) bool { return folder == arg },
		func(folder string) bool { return strings.EqualFold(folder, arg) },
		func(folder string) bool { return startsWithWords(folder, arg) },
	} {
		var matched []string
		for _, folder := range folders {
			if matches(folder) {
				matched = append(matched, folder)
			}
		}
		switch len(matched) {
		case 0:
			continue
		case 1:
			checkout := filepath.Join(dir, matched[0])
			if !hasGit(checkout) {
				return "", fmt.Errorf("project %q resolves to %s, which is %w (no .git); clone it there or pass a path", arg, checkout, ErrNotCheckout)
			}
			return checkout, nil
		default:
			return "", fmt.Errorf("%w: %q matches %s under the projects root %s; name one in full or pass a path", ErrAmbiguous, arg, strings.Join(matched, ", "), dir)
		}
	}

	var candidates []string
	for _, folder := range folders {
		if hasGit(filepath.Join(dir, folder)) {
			candidates = append(candidates, folder)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("%w: %q under the projects root %s, which holds no git checkouts", ErrUnknown, arg, dir)
	}
	return "", fmt.Errorf("%w: %q under the projects root %s; candidates: %s", ErrUnknown, arg, dir, strings.Join(candidates, ", "))
}

// isBareName reports whether a project argument is a name to look up rather
// than a path to use.
func isBareName(arg string) bool {
	return arg != "" && arg != "." && arg != ".." &&
		!strings.ContainsAny(arg, `/\`) && filepath.VolumeName(arg) == ""
}

// startsWithWords reports whether name is the leading words of folder: a
// case-insensitive prefix that ends where a separator begins.
func startsWithWords(folder, name string) bool {
	if len(folder) <= len(name) || !strings.EqualFold(folder[:len(name)], name) {
		return false
	}
	return strings.ContainsRune("-_. ", rune(folder[len(name)]))
}

func hasGit(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
