package crawler

import (
	"errors"
	"fmt"
	"strings"
)

// briefSection and briefKey are where a brief says which default connectors
// its goblin carries: one `connectors:` line under its Authentication
// heading, beside its credentials and mcp lines.
const (
	briefSection = "Authentication"
	briefKey     = "connectors"
	// LeftOut is the word a brief writes on its connectors line, and a task
	// record keeps, for a goblin that carries no default connector.
	LeftOut = "none"
)

// IsLeftOutBy reports whether brief leaves the default connectors out with
// `connectors: none`. A brief with no connectors line, or one that names the
// crawler, carries it. A line that names anything else is refused rather
// than guessed at.
func IsLeftOutBy(brief string) (bool, error) {
	section, isStated, isLeftOut := "", false, false
	for _, line := range strings.Split(strings.ReplaceAll(brief, "\r\n", "\n"), "\n") {
		if heading, found := strings.CutPrefix(line, "## "); found {
			section = strings.TrimSpace(heading)
			continue
		}
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if section != briefSection || !found || !strings.EqualFold(strings.TrimSpace(key), briefKey) {
			continue
		}
		if isStated {
			return false, errors.New("the brief's " + briefSection + " section has more than one " + briefKey + " line")
		}
		isStated = true
		switch value = strings.TrimSpace(value); {
		case strings.EqualFold(value, LeftOut):
			isLeftOut = true
		case strings.EqualFold(value, Name):
		default:
			return false, fmt.Errorf("the brief's %s line names %q, which is no default connector: write %s: %s to carry it, or %s: %s to leave it out", briefKey, value, briefKey, Name, briefKey, LeftOut)
		}
	}
	return isLeftOut, nil
}
