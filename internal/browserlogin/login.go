// Package browserlogin keeps the fleet's browser logins: one Chrome profile
// for each provider account, which the Overlord signs in to himself and a
// key fetch then drives. What is kept is the browser's session, never a
// password, a code or a link. The registry, state/browser-logins.json,
// records each login and the Chrome the broker runs on it now, and the
// profiles live under state/browser-logins, readable by his Windows user
// alone, never in a goblin's worktree.
package browserlogin

import (
	"regexp"
	"strings"
	"time"
)

// Login is one provider account's kept browser session.
type Login struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	// Account is the account's name as the Overlord reads it.
	Account string `json:"account"`
	// Projects are the credential scopes whose goblins may use the login.
	Projects []string   `json:"projects"`
	UsedAt   *time.Time `json:"used_at,omitempty"`
	// Browser is the Chrome the broker runs on the login's profile now, nil
	// while none runs.
	Browser *Browser `json:"browser,omitempty"`
}

// Browser is a Chrome the broker started on a login's profile: its process,
// proven by when it started, its DevTools address on this machine, and the
// task it is lent to.
type Browser struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	URL     string    `json:"url"`
	Holder  string    `json:"holder"`
	Since   time.Time `json:"since"`
	// Hosts are the host names it may reach, from its provider's recipe.
	Hosts []string `json:"hosts"`
}

var loginID = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

// ValidID reports whether id can name a login, and so a profile folder.
func ValidID(id string) bool {
	return loginID.MatchString(id)
}

// DefaultID is the ID of a project's own login of a provider: the provider
// and the project's scope, lowercase, with every other character a dash.
func DefaultID(provider, project string) string {
	var id strings.Builder
	for _, r := range strings.ToLower(provider + "-" + project) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			id.WriteRune(r)
		} else if !strings.HasSuffix(id.String(), "-") {
			id.WriteByte('-')
		}
	}
	trimmed := strings.Trim(id.String(), "-")
	if len(trimmed) > 64 {
		trimmed = strings.TrimRight(trimmed[:64], "-")
	}
	return trimmed
}
