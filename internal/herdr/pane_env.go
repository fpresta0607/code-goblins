package herdr

import "strings"

// IsPaneVariable reports whether name is one of the variables a Herdr pane
// gives every process it starts: HERDR_ENV, HERDR_PANE_ID, HERDR_TAB_ID,
// HERDR_WORKSPACE_ID, HERDR_STARTUP_CWD, HERDR_SOCKET_PATH, HERDR_BIN_PATH and
// any Herdr adds later. A program started from a pane passes them on, and
// herdr refuses to start inside what they name as another Herdr ("inception
// detected"). HERDR_SESSION names the session a fleet runs in, not a pane, so
// it is not one. Names compare without case, as Windows compares them.
func IsPaneVariable(name string) bool {
	upper := strings.ToUpper(name)
	return strings.HasPrefix(upper, "HERDR_") && upper != "HERDR_SESSION"
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
