package main

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/conpty"
)

// authStoreConsole runs this test binary as cfo auth in a pseudo console of
// its own, with no fleet behind it; TestMain sends it to runAuth.
const authStoreConsole = "auth-store-console-test"

// shownWithin waits up to wait for the console to show text.
func (c *console) shownWithin(text string, wait time.Duration) bool {
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if strings.Contains(c.shown(), text) {
			return true
		}
	}
	return false
}

// A value typed into cfo auth store at a console never shows on it: the
// command says the typing is hidden, and the store holds what was typed.
func TestAuthStoreHidesAValueTypedAtTheConsole(t *testing.T) {
	// Arrange
	vault := filepath.Join(t.TempDir(), "vault")
	var env []string
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); !strings.EqualFold(name, auth.StoreDirEnv) {
			env = append(env, entry)
		}
	}
	env = append(env, auth.StoreDirEnv+"="+vault)
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	canary := "canary" + hex.EncodeToString(random[:])
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	started, err := conpty.Start(conpty.Spec{Args: []string{program, authStoreConsole, "store", "--project", "throwaway", "CONSOLE_TOKEN"}, Env: env, Cols: 120, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	c := &console{Console: started}
	t.Cleanup(func() { _ = started.Close() })
	go func() {
		chunk := make([]byte, 32<<10)
		for {
			n, err := started.Read(chunk)
			c.mu.Lock()
			c.screen.Write(chunk[:n])
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	prompted := c.shownWithin("typing is hidden", 15*time.Second)

	// Act
	if _, err := started.Write([]byte(canary + "\r")); err != nil {
		t.Fatal(err)
	}
	stored := c.shownWithin("stored throwaway/CONSOLE_TOKEN", 15*time.Second)

	// Assert
	if strings.Contains(c.shown(), canary) {
		t.Fatal("the console showed the value as it was typed")
	}
	if !prompted || !stored {
		shown := c.shown()
		t.Fatalf("prompted %v, stored %v; the console shows %q", prompted, stored, shown[max(0, len(shown)-400):])
	}
	if data, err := os.ReadFile(filepath.Join(vault, "throwaway", "CONSOLE_TOKEN")); err != nil || string(data) != canary {
		t.Fatalf("the store does not hold what was typed: %v", err)
	}
}
