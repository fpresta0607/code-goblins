package spawn

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/install"
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
			want, err := install.GoblinSettings(f.service.HomeRoot)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(written, want) {
				t.Fatalf("the settings file holds %s, want this build's goblin hooks run by the home's cfo.exe", written)
			}
		})
	}
}
