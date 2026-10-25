package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// updateWords are what a harness's own screen says once an update of it is
// installed and waits for a restart. Claude Code 2.1.292 draws "✓ Update
// installed · Restart to update" in its footer, and the second half is a copy
// its servers choose ("/restart to apply", "Restart to apply"), so only the
// first half is read. Codex and pi say only that an update is available
// ("✨ Update available!", "New version … is available"), which a restart does
// not install, and Codex says "Update ran successfully! Please restart Codex."
// only as it exits, so neither has words a running session shows.
var updateWords = map[Kind]string{Claude: "Update installed"}

// UpdateNotice returns the row where screen shows kind saying an update of
// itself was installed and waits for a restart, and false when it shows none.
// Only the footer under the composer's closing rule is the harness's own: the
// same words above it are the conversation, such as the CFO quoting the line,
// and a screen without the rule has no footer to read.
func UpdateNotice(kind Kind, screen []string) (string, bool) {
	words, ok := updateWords[kind]
	if !ok {
		return "", false
	}
	footer := -1
	for i, row := range screen {
		if isRule(row) {
			footer = i + 1
		}
	}
	if footer < 0 {
		return "", false
	}
	for _, row := range screen[footer:] {
		if strings.Contains(row, words) {
			return strings.TrimSpace(row), true
		}
	}
	return "", false
}

// npmPackages are the packages npm installs for the harnesses this fleet
// starts from npm, by the harness whose program on PATH is npm's script shim.
var npmPackages = map[Kind]string{
	Codex: "@openai/codex",
	Pi:    "@earendil-works/pi-coding-agent",
}

// ProgramInstalled returns the program kind starts from, as a launch finds it
// on PATH, and when that install was last written. Claude Code's installer
// writes claude.exe again on every update, leaving the one still running as
// claude.exe.old.*, so its program is the install. npm writes the script
// shims codex and pi run through only when it links the bin, and not when the
// harness's own updater rewrites the package under it (codex update, pi
// update), so for those the package npm installed is read instead, which
// every install of a new version writes. A program npm did not install beside
// itself keeps its own time.
func ProgramInstalled(kind Kind) (string, time.Time, error) {
	program, err := exec.LookPath(string(kind))
	if err != nil {
		return "", time.Time{}, err
	}
	info, err := os.Stat(program)
	if err != nil {
		return "", time.Time{}, err
	}
	if pkg, fromNpm := npmPackages[kind]; fromNpm {
		if at, isInstalled := npmPackageWritten(program, pkg); isInstalled {
			return program, at, nil
		}
	}
	return program, info.ModTime(), nil
}

// npmPackageWritten is when the package npm installed beside the shim was last
// written, read from its package.json: npm links the bin in the folder the shim
// sits in and unpacks the package under that folder's node_modules, and both
// npm's install of a new version and the harness's own updater write the
// manifest again.
func npmPackageWritten(shim, pkg string) (time.Time, bool) {
	manifest := filepath.Join(filepath.Dir(shim), "node_modules", filepath.FromSlash(pkg), "package.json")
	info, err := os.Stat(manifest)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}
