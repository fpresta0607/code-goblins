package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

// writeManifestJSON lays a manifest down as raw JSON, which is what proves
// the on-disk key names parse rather than only that the struct round-trips.
func writeManifestJSON(t *testing.T, dataDir, project, content string) {
	t.Helper()
	path := ManifestPath(dataDir, project)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A project declares its local stack here so "how do I test this locally" is
// answered once, by the project, rather than re-derived from whatever files
// the checkout happens to hold.
func TestResolveReadsADeclaredLocalStack(t *testing.T) {
	dataDir := t.TempDir()
	writeManifestJSON(t, dataDir, "demo", `{
	  "project": "demo",
	  "local": {"up": ["npx supabase start", "npm run demo"], "down": ["npx supabase stop"]}
	}`)
	manifest, err := Resolve(dataDir, "demo")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(manifest.Local.Up) != 2 || manifest.Local.Up[1] != "npm run demo" {
		t.Errorf("up = %v, want both declared commands in order", manifest.Local.Up)
	}
	if len(manifest.Local.Down) != 1 || manifest.Local.Down[0] != "npx supabase stop" {
		t.Errorf("down = %v, want the declared teardown", manifest.Local.Down)
	}
}

// A manifest that predates the local block must keep working untouched.
func TestResolveAcceptsAManifestWithNoLocalBlock(t *testing.T) {
	dataDir := t.TempDir()
	writeManifestJSON(t, dataDir, "old", `{"project":"old","link":[".env"],"dependencies":{"install":["npm ci"]}}`)
	manifest, err := Resolve(dataDir, "old")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(manifest.Local.Up) != 0 || len(manifest.Local.Down) != 0 {
		t.Errorf("local = %+v, want nothing declared", manifest.Local)
	}
}

// A blank command reads as an answer while saying nothing, and a teardown
// with no way to start reads as half a manifest. Both fail here rather than
// printing an empty cell where the CFO expected a command.
func TestValidateRefusesAnIncompleteLocalStack(t *testing.T) {
	cases := map[string]string{
		"blank up command":   `{"project":"p","local":{"up":["  "]}}`,
		"blank down command": `{"project":"p","local":{"up":["npm run dev"],"down":[""]}}`,
		"down with no up":    `{"project":"p","local":{"down":["docker compose down"]}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			writeManifestJSON(t, dataDir, "p", content)
			if _, err := Resolve(dataDir, "p"); err == nil {
				t.Error("Resolve accepted an incomplete local stack")
			}
		})
	}
}
