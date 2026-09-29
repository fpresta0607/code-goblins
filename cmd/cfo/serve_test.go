package main

import (
	"os"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestFirstRunUsesTheCFOHomeAndSharesTheRememberedAgent(t *testing.T) {
	h := home.Home{Root: t.TempDir(), State: t.TempDir()}
	run := firstRunOn(h)
	if run.Home != h.Root {
		t.Fatalf("home=%s", run.Home)
	}
	if err := run.Save("pi"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfoHarnessPath(h.State))
	if err != nil || strings.TrimSpace(string(data)) != "pi" {
		t.Fatalf("choice=%q err=%v", data, err)
	}
	if name, err := run.DefaultAgent(); err != nil || name != "pi" {
		t.Fatalf("agent=%s err=%v", name, err)
	}
}
