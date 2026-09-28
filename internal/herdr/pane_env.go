package herdr

import (
	"slices"
	"strings"
)

// paneVariables are the variables a Herdr pane gives every process it starts.
var paneVariables = []string{
	"HERDR_ENV",
	"HERDR_PANE_ID",
	"HERDR_TAB_ID",
	"HERDR_WORKSPACE_ID",
	"HERDR_STARTUP_CWD",
	"HERDR_SOCKET_PATH",
	"HERDR_BIN_PATH",
}

// IsPaneVariable reports whether name is one of the variables a Herdr pane
// gives every process it starts: HERDR_ENV, HERDR_PANE_ID, HERDR_TAB_ID,
// HERDR_WORKSPACE_ID, HERDR_STARTUP_CWD, HERDR_SOCKET_PATH and HERDR_BIN_PATH.
// A program started from a pane passes them on, and herdr refuses to start
// inside what they name as another Herdr ("inception detected"). Every other
// HERDR_ variable is not the pane's and is kept: HERDR_SESSION names the
// session a fleet runs in, and configuration such as HERDR_CONFIG_PATH is the
// user's. Names compare without case, as Windows compares them.
func IsPaneVariable(name string) bool {
	return slices.Contains(paneVariables, strings.ToUpper(name))
}

// WithoutPane is env without the pane variables (IsPaneVariable).
func WithoutPane(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		if name, _, _ := strings.Cut(entry, "="); !IsPaneVariable(name) {
			kept = append(kept, entry)
		}
	}
	return kept
}
