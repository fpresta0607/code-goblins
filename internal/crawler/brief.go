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
// `connectors: none`. A brief with no connectors line carries them. A line
// that says anything else is refused rather than guessed at.
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
		if value = strings.TrimSpace(value); !strings.EqualFold(value, LeftOut) {
			return false, fmt.Errorf("the brief's %s line says %q: write %s: %s to leave the default connectors out, or no %s line to carry them", briefKey, value, briefKey, LeftOut, briefKey)
		}
		isLeftOut = true
	}
	return isLeftOut, nil
}
