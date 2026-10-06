package install

import (
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// Target is the home an install puts Code Goblins in, chosen by what is
// already on the machine, so no install ever stops to ask.
type Target struct {
	// Root is the home.
	Root string
	// Update says Code Goblins is there already, so the install updates it.
	Update bool
	// Kept says Root is a home in use outside the standard folder, which the
	// install keeps rather than moves: moving a fleet is the person's choice,
	// never a step of an install.
	Kept bool
}

// FindTarget picks the home for an install: the one CFO_HOME names where a
// fleet already lives there, whatever folder that is, and otherwise the
// standard folder.
func FindTarget(env EnvStore, standard string) (Target, error) {
	current, set, err := env.Get(homeVariable)
	if err != nil {
		return Target{}, err
	}
	if set && !sameDirectory(current, standard) && isDir(filepath.Join(current, "state")) {
		return Target{Root: current, Update: true, Kept: true}, nil
	}
	_, err = os.Stat(filepath.Join(standard, home.InstalledMarker))
	return Target{Root: standard, Update: err == nil || isDir(filepath.Join(standard, "state"))}, nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
