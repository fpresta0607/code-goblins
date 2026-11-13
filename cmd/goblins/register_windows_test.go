package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

func TestSessionStartRegistersTheSessionThatHoldsTheHome(t *testing.T) {
	h := registerHome(t)
	if _, err := lock.Acquire(h.State); err != nil {
		t.Fatal(err)
	}
	hostAttachTestTerminal(t, h.State, "ceo")
	standInAsTerminalProgram(t, h.State, "ceo")
	var stdout bytes.Buffer
	registerPrimary(h, os.Getpid(), "claude", "s1", &stdout)
	want := "CEO REGISTRATION: claude pid " + strconv.Itoa(os.Getpid()) + " in native terminal ceo\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	data, err := os.ReadFile(filepath.Join(h.State, "primary.json"))
	if err != nil || !strings.Contains(string(data), `"agent":"claude"`) || !strings.Contains(string(data), `"session":"s1"`) {
		t.Fatalf("primary.json = %s, %v", data, err)
	}
}
