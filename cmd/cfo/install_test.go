package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Install wires in the checkout it runs from, and from anywhere else the
// per-user home it sets up, whatever CFO_HOME says.
func TestInstallRootIsTheCheckoutOrElseThePerUserHome(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("CFO_HOME", t.TempDir())
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "cmd", "cfo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "AGENTS.md"), []byte("contract"), 0o644); err != nil {
		t.Fatal(err)
	}
	// SamePath compares existing folders only; installRoot itself never
	// creates this one.
	perUser := filepath.Join(local, "CodeGoblins")
	if err := os.Mkdir(perUser, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, dir, want string
		checkout        bool
	}{
		{name: "a checkout", dir: checkout, want: checkout, checkout: true},
		{name: "a folder inside a checkout", dir: filepath.Join(checkout, "cmd"), want: perUser},
		{name: "any other folder", dir: t.TempDir(), want: perUser},
	} {
		t.Chdir(test.dir)
		root, isCheckout, err := installRoot()
		if err != nil || !fsx.SamePath(root, test.want) || isCheckout != test.checkout {
			t.Errorf("%s: installRoot = %q, %v, %v; want %q, %v", test.name, root, isCheckout, err, test.want, test.checkout)
		}
	}

	t.Setenv("LOCALAPPDATA", "")
	if root, _, err := installRoot(); err == nil {
		t.Errorf("installRoot without LOCALAPPDATA outside a checkout = %q, want a refusal", root)
	}
}
