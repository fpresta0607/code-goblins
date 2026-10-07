package harness

import (
	"os"
	"os/exec"
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

// ProgramInstalled returns the program kind starts from, as a launch finds it
// on PATH, and when it was last written. Claude Code's installer writes
// claude.exe again on every update, leaving the one still running as
// claude.exe.old.*, and npm writes the script shims codex and pi run through
// again on every install of them, so a harness started before that time runs
// an older install than the one there.
func ProgramInstalled(kind Kind) (string, time.Time, error) {
	program, err := exec.LookPath(string(kind))
	if err != nil {
		return "", time.Time{}, err
	}
	info, err := os.Stat(program)
	if err != nil {
		return "", time.Time{}, err
	}
	return program, info.ModTime(), nil
}
