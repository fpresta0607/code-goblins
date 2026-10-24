package spawn

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

func TestSpawnStartsAClaudeGoblinWithTheGoblinHooks(t *testing.T) {
	for _, test := range []struct {
		name         string
		kind         harness.Kind
		isGivenHooks bool
	}{
		{"a Claude goblin", harness.Claude, true},
		{"a Codex goblin", harness.Codex, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)
			f.service.Harness.Adapters[test.kind] = fixtureAdapter{events: &f.events, specs: &f.specs, buildErr: errors.New("nothing starts in this test")}
			f.request.Harness = test.kind

			// Act
			_, err := f.service.Spawn(context.Background(), f.request)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "nothing starts in this test") {
				t.Fatalf("Spawn = %v, want the build refusal", err)
			}
			settings := f.specs[len(f.specs)-1].Settings
			if !test.isGivenHooks {
				if settings != "" {
					t.Fatalf("Settings = %q, want none for a harness Claude Code does not run", settings)
				}
				return
			}
			if settings != filepath.Join(f.stateDir, goblinSettingsFile) {
				t.Fatalf("Settings = %q, want the home's goblin settings file", settings)
			}
			written, err := os.ReadFile(settings)
			if err != nil {
				t.Fatal(err)
			}
			want, err := install.GoblinSettings(f.service.HomeRoot, f.service.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(written, want) {
				t.Fatalf("the settings file holds %s, want this build's goblin hooks run by the home's cfo.exe", written)
			}
		})
	}
}

// A Codex goblin takes its native session hooks and its goblin seal in its
// own launch, run by the home's cfo.exe against the home's state, and a
// goblin of any other harness takes none that way: Claude Code's ride its
// settings file and Pi's its extensions. A home under a path with a space,
// as a profile named for a person can be, still starts its Codex goblin,
// without them.
func TestSpawnStartsACodexGoblinWithItsNativeSessionHooks(t *testing.T) {
	for _, test := range []struct {
		name         string
		kind         harness.Kind
		homeFolder   string
		isGivenHooks bool
	}{
		{"a Codex goblin", harness.Codex, "", true},
		{"a Codex goblin of a home under a path with a space", harness.Codex, "John Smith", false},
		{"a Claude goblin", harness.Claude, "", false},
		{"a Pi goblin", harness.Pi, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)
			f.service.Harness.Adapters[test.kind] = fixtureAdapter{events: &f.events, specs: &f.specs, buildErr: errors.New("nothing starts in this test")}
			f.request.Harness = test.kind
			if test.homeFolder != "" {
				f.service.HomeRoot = filepath.Join(t.TempDir(), test.homeFolder)
				if err := os.MkdirAll(f.service.HomeRoot, 0o700); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			_, err := f.service.Spawn(context.Background(), f.request)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "nothing starts in this test") {
				t.Fatalf("Spawn = %v, want the build refusal", err)
			}
			hooks := f.specs[len(f.specs)-1].CodexHooks
			if !test.isGivenHooks {
				if len(hooks) != 0 {
					t.Fatalf("CodexHooks = %q, want none", hooks)
				}
				return
			}
			want, err := nativehook.CodexGoblinArguments(filepath.Join(f.service.HomeRoot, home.BinDir, "cfo.exe"), f.service.HomeRoot, f.service.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(hooks, want) {
				t.Fatalf("CodexHooks = %q, want %q", hooks, want)
			}
			seal := hooks[len(hooks)-1]
			if !strings.HasPrefix(seal, "hooks.PreToolUse=") || !strings.Contains(seal, filepath.ToSlash(filepath.Join(f.service.HomeRoot, home.BinDir, "cfo.exe"))+" hook goblin-seal-codex") {
				t.Fatalf("the last override is %q, want the goblin seal before each shell command", seal)
			}
		})
	}
}

// A pi goblin's seal rides an extension of its own, written into the home's
// state anew at each launch and run by the home's hook program, beside the
// native one every pi session loads; a goblin of any other harness gets none.
func TestSpawnStartsAPiGoblinWithTheGoblinSeal(t *testing.T) {
	for _, test := range []struct {
		name        string
		kind        harness.Kind
		isGivenSeal bool
	}{
		{"a Pi goblin", harness.Pi, true},
		{"a Claude goblin", harness.Claude, false},
		{"a Codex goblin", harness.Codex, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t)
			f.service.Harness.Adapters[test.kind] = fixtureAdapter{events: &f.events, specs: &f.specs, buildErr: errors.New("nothing starts in this test")}
			f.request.Harness = test.kind

			// Act
			_, err := f.service.Spawn(context.Background(), f.request)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "nothing starts in this test") {
				t.Fatalf("Spawn = %v, want the build refusal", err)
			}
			extension := f.specs[len(f.specs)-1].PiExtensionPath
			if !test.isGivenSeal {
				if extension != "" {
					t.Fatalf("PiExtensionPath = %q, want none for a harness pi does not run", extension)
				}
				return
			}
			if extension != filepath.Join(f.stateDir, goblinPiExtensionFile) {
				t.Fatalf("PiExtensionPath = %q, want the home's goblin pi extension", extension)
			}
			written, err := os.ReadFile(extension)
			if err != nil {
				t.Fatal(err)
			}
			if want := install.GoblinPiExtension(install.HookCommand(filepath.Join(f.service.HomeRoot, home.BinDir))); string(written) != want || !strings.Contains(want, `["hook", "goblin-seal-pi"]`) {
				t.Fatalf("the extension holds %s, want this build's goblin seal run by the home's hook program", written)
			}
		})
	}
}
