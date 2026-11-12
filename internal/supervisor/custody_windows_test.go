package supervisor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

// The CEO's own session is the harness native terminal ceo runs. A harness in
// any other native terminal of the home, a goblin's included, never registers
// as the CEO and never takes the session lock.
func TestRegisterRefusesANativeTerminalThatIsNotTheCEOs(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	hostTerminal(t, store.Home.State, "desk").standIn(t)
	t.Setenv(host.IDVariable, "desk")

	// Act
	_, err := Register(store.Home.State, "claude", "session-1")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "native terminal ceo") {
		t.Fatalf("Register from native terminal desk: %v, want a refusal naming native terminal ceo", err)
	}
	if registrationExists(t, store) {
		t.Error("a refused registration wrote primary.json")
	}
	if _, err := lock.Read(store.Home.State); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused registration took the session lock: %v", err)
	}
}

// The session lock held by a live process that is not the CEO's own session
// does not keep the CEO from registering: the CEO's session takes the lock
// over and the takeover is audited.
func TestRegisterTakesTheHomeOverFromASessionThatIsNotTheCEOs(t *testing.T) {
	// Arrange
	store, _, ceo := registerFixture(t)
	other := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _, _ = other.Process.Wait() })
	if _, err := lock.AcquireOwner(store.Home.State, other.Process.Pid, "desktop-session"); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := Register(store.Home.State, "claude", "session-1")

	// Assert
	if err != nil {
		t.Fatalf("Register: %v, want the CEO registered over a holder that is not its own session", err)
	}
	if !lock.HeldBy(store.Home.State, os.Getpid()) {
		t.Error("the session lock does not name the CEO's harness")
	}
	if err := ceo.check(); err != nil {
		t.Errorf("the board cannot reach the registered CEO: %v", err)
	}
	audit, err := os.ReadFile(filepath.Join(store.Home.State, "custody.audit"))
	if err != nil || !strings.Contains(string(audit), strconv.Itoa(other.Process.Pid)) || !strings.Contains(string(audit), "desktop-session") {
		t.Errorf("state\\custody.audit = %q (%v), want one record naming pid %d and its session", audit, err, other.Process.Pid)
	}
}
