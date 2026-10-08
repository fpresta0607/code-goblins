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
	// made with cfo home move, never a step of an install.
	Kept bool
	// Checkout says Root is a code-goblins checkout an older build made the
	// home.
	Checkout bool
}

// FindTarget picks the home for an install: the one CFO_HOME names where a
// fleet already lives there, whatever folder that is, and otherwise the
// standard folder. A CFO_HOME naming a folder with no fleet in it is stale,
// and the install takes the machine over from it.
func FindTarget(env EnvStore, standard string) (Target, error) {
	current, set, err := env.Get(homeVariable)
	if err != nil {
		return Target{}, err
	}
	if set && !sameDirectory(current, standard) && isDir(filepath.Join(current, "state")) {
		root := filepath.Clean(current)
		return Target{Root: root, Update: true, Kept: true, Checkout: isCheckout(root)}, nil
	}
	_, err = os.Stat(filepath.Join(standard, home.InstalledMarker))
	return Target{Root: standard, Update: err == nil || isDir(filepath.Join(standard, "state"))}, nil
}

// isCheckout reports whether root is a code-goblins checkout, as the install
// script recognises one: its contract and the source of cfo beside each other.
func isCheckout(root string) bool {
	_, err := os.Stat(filepath.Join(root, "AGENTS.md"))
	return err == nil && isDir(filepath.Join(root, "cmd", "cfo"))
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
