package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestWorkResolverReadsTheRepositoryOfACheckoutThatAppearsLater(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h := home.Home{Root: root, Data: filepath.Join(root, "data"), State: filepath.Join(root, "state")}
	checkout := filepath.Join(root, "widgets")
	resolve := workResolver(t.Context(), h, func(string) (string, error) { return checkout, nil }, func(context.Context, string) (string, error) { return "Acme/Widgets", nil })

	// Act
	before, _, beforeErr := resolve("widgets")
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	after, _, afterErr := resolve("widgets")

	// Assert
	if before != "" || beforeErr != nil {
		t.Fatalf("before the checkout: repository %q, err %v, want none and no error", before, beforeErr)
	}
	if after != "acme/widgets" || afterErr != nil {
		t.Fatalf("after the checkout appeared: repository %q, err %v, want acme/widgets", after, afterErr)
	}
}

func TestWorkResolverSaysACheckoutItCannotStat(t *testing.T) {
	// Arrange: a path with a NUL byte cannot be statted, and is not missing.
	root := t.TempDir()
	h := home.Home{Root: root, Data: filepath.Join(root, "data"), State: filepath.Join(root, "state")}
	resolve := workResolver(t.Context(), h, func(string) (string, error) { return filepath.Join(root, "wid\x00gets"), nil }, func(context.Context, string) (string, error) { return "acme/widgets", nil })

	// Act
	repository, _, err := resolve("widgets")

	// Assert
	if err == nil {
		t.Fatalf("repository %q and no error, want the stat failure", repository)
	}
}
