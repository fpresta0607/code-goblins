package harness

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Each harness's program is the one its launch finds on PATH: Claude Code's
// native build as claude.exe, which its installer writes again on every
// update, and the npm script shims codex and pi run through, which every npm
// install of them writes again. Its last write is when the harness was last
// installed.
func TestProgramInstalledIsWhenItsProgramOnPathWasWritten(t *testing.T) {
	directory := t.TempDir()
	installed := time.Date(2026, 10, 6, 19, 10, 37, 0, time.UTC)
	for _, name := range []string{"claude.exe", "codex.cmd", "pi.cmd"} {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte("stand-in"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, installed, installed); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory)
	for kind, name := range map[Kind]string{Claude: "claude.exe", Codex: "codex.cmd", Pi: "pi.cmd"} {
		program, at, err := ProgramInstalled(kind)
		if err != nil || !filepath.IsAbs(program) || filepath.Base(program) != name || !at.Equal(installed) {
			t.Errorf("ProgramInstalled(%s) = %q, %s, %v; want %s written at %s", kind, program, at, err, name, installed)
		}
	}
}

// npm writes the script shim codex and pi run through once, when it links the
// bin, and the harness's own updater writes the package under it without
// touching the shim. The package is the install, so a package written after a
// session started reads as an update however old the shim is, and a shim npm
// linked again over an unchanged package is no update at all.
func TestProgramInstalledReadsTheNpmPackageUnderTheShim(t *testing.T) {
	directory := t.TempDir()
	linked := time.Date(2026, 10, 6, 19, 10, 37, 0, time.UTC)
	installed := linked.Add(-time.Hour)
	for kind, pkg := range map[Kind]string{Codex: "@openai/codex", Pi: "@earendil-works/pi-coding-agent"} {
		shim := filepath.Join(directory, string(kind)+".cmd")
		if err := os.WriteFile(shim, []byte("stand-in"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(shim, linked, linked); err != nil {
			t.Fatal(err)
		}
		manifest := filepath.Join(directory, "node_modules", filepath.FromSlash(pkg), "package.json")
		if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifest, []byte("{\"version\":\"1.0.0\"}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(manifest, installed, installed); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory)
	for _, test := range []struct {
		name string
		kind Kind
		pkg  string
		at   time.Time
	}{
		{"codex package written before npm linked the shim", Codex, "@openai/codex", installed},
		{"pi package written before npm linked the shim", Pi, "@earendil-works/pi-coding-agent", installed},
	} {
		t.Run(test.name, func(t *testing.T) {
			program, at, err := ProgramInstalled(test.kind)
			if err != nil || filepath.Base(program) != string(test.kind)+".cmd" || !at.Equal(test.at) {
				t.Errorf("ProgramInstalled(%s) = %q, %s, %v; want %s at the package's %s", test.kind, program, at, err, test.pkg, test.at)
			}
		})
	}

	// Act: an updater writes the package, and leaves the shim alone.
	updated := linked.Add(time.Hour)
	for _, pkg := range []string{"@openai/codex", "@earendil-works/pi-coding-agent"} {
		manifest := filepath.Join(directory, "node_modules", filepath.FromSlash(pkg), "package.json")
		if err := os.Chtimes(manifest, updated, updated); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	for kind, pkg := range map[Kind]string{Codex: "@openai/codex", Pi: "@earendil-works/pi-coding-agent"} {
		if _, at, err := ProgramInstalled(kind); err != nil || !at.Equal(updated) {
			t.Errorf("ProgramInstalled(%s) after its updater = %s, %v; want the package %s at %s", kind, at, err, pkg, updated)
		}
	}
}
