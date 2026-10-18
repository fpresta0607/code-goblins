package main

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// linkedPackages are the repository's packages the setup links: what its plan
// reads and what those read, and nothing more.
var linkedPackages = []string{
	"internal/devdrive",
	"internal/disk",
	"internal/execx",
	"internal/fsx",
	"internal/home",
	"internal/installbase",
	"internal/proc",
}

// The setup links none of the repository's packages but linkedPackages, as
// the release builds it. Windows judges a downloaded program by the
// reputation of its one file, and a release whose setup builds to the last
// release's file publishes that signed setup again, so each package the setup
// links is a way its file changes between releases.
func TestTheSetupLinksOnlyWhatItsPlanReads(t *testing.T) {
	// Arrange
	const module = "github.com/fpresta0607/code-goblins/"
	command := exec.Command("go", "list", "-deps", "-tags", "production", ".")
	command.Env = append(os.Environ(), "GOOS=windows", "GOFLAGS=")

	// Act
	out, err := command.Output()

	// Assert
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	listed := strings.Fields(string(out))
	if !slices.Contains(listed, module+"cmd/goblins-setup") {
		t.Fatalf("go list -deps listed %q, want the setup among them", listed)
	}
	var extra []string
	for _, path := range listed {
		if name, isOurs := strings.CutPrefix(path, module); isOurs && name != "cmd/goblins-setup" && !slices.Contains(linkedPackages, name) {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		t.Errorf("the setup links %s beside %s: each package the setup links is a way its file changes between releases, and a changed file starts SmartScreen's reputation over, so link only what the setup's plan reads", strings.Join(extra, ", "), strings.Join(linkedPackages, ", "))
	}
}
