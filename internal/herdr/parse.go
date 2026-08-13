package herdr

import (
	"fmt"
	"strings"
)

// ParseTarget parses a session:pane target by splitting only its first colon.
func ParseTarget(raw string) (Target, error) {
	session, pane, found := strings.Cut(raw, ":")
	if !found || session == "" || pane == "" {
		return Target{}, fmt.Errorf("herdr: target %q must be <session>:<pane>", raw)
	}
	return Target{Session: session, Pane: pane}, nil
}
