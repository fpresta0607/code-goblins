package terminal

import (
	"testing"

	"github.com/fpresta0607/code-goblins/internal/herdr"
)

// A named session replaces the client's own and an empty one keeps it, and
// neither changes the client every later task is opened from.
func TestHerdrSessionsOpensTheClientInTheNamedSession(t *testing.T) {
	client := &herdr.Client{Session: "fleet"}
	open := HerdrSessions(client)

	for session, want := range map[string]string{"task-session": "task-session", "": "fleet"} {
		if got := open(session).EffectiveSession(); got != want {
			t.Errorf("open(%q) routes to %q, want %q", session, got, want)
		}
	}
	if client.Session != "fleet" {
		t.Errorf("the shared client moved to session %q", client.Session)
	}
}

// Every session opened from a client with a socket cache reads Herdr's
// structure through that one cache, which keeps each session's socket apart.
func TestHerdrSessionsShareTheClientsSocketCache(t *testing.T) {
	cache := herdr.NewSocketCache()
	open := HerdrSessions(&herdr.Client{Session: "fleet", Sockets: cache})

	first, second := open("task-session").(*herdr.Client), open("").(*herdr.Client)

	if first.Sockets != cache || second.Sockets != cache {
		t.Fatalf("sockets = %p and %p, want the client's cache %p", first.Sockets, second.Sockets, cache)
	}
}
