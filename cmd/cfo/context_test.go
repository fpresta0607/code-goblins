package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestLavishPortableRecapRetainsRelativeImage(t *testing.T) {
	cli := filepath.Join(os.Getenv("APPDATA"), "npm", "node_modules", "lavish-axi", "dist", "cli.mjs")
	if _, err := os.Stat(cli); os.IsNotExist(err) {
		t.Skip("real Lavish export requires installed lavish-axi")
	}
	source := t.TempDir()
	dest := filepath.Join(t.TempDir(), "saved recap.html")
	pixel, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aD0sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "evidence image.png"), pixel, 0600); err != nil {
		t.Fatal(err)
	}
	html := filepath.Join(source, "recap.html")
	if err = os.WriteFile(html, []byte(`<!doctype html><html><head><title>Synthetic recap</title></head><body><img src="evidence image.png" alt="Synthetic fixture"></body></html>`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = exportRecap(context.Background(), execx.OSRunner{}, html, dest); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "data:image/png;base64,") || strings.Contains(string(saved), `src="evidence image.png"`) {
		t.Fatalf("relative screenshot was not inlined")
	}
	if err = os.Remove(filepath.Join(source, "evidence image.png")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dest); err != nil {
		t.Fatal("durable export was coupled to source assets")
	}
}
