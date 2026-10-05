package gatetest

import (
	"fmt"
	"slices"
)

// Level is how much of the repository a run verifies.
type Level string

const (
	// Fast vets what the change reaches and runs the changed packages' quick
	// tests: feedback while working, never what a merge requires.
	Fast Level = "fast"
	// Affected vets and tests the changed packages and the packages that
	// import them.
	Affected Level = "affected"
	// Full vets and tests every package.
	Full Level = "full"
)

// levels are the levels from narrowest to broadest.
var levels = []Level{Fast, Affected, Full}

// ParseLevel reads a level's name.
func ParseLevel(name string) (Level, error) {
	if level := Level(name); slices.Contains(levels, level) {
		return level, nil
	}
	return "", fmt.Errorf("gatetest: level %q is not fast, affected or full", name)
}

// Covers reports whether a run at l verifies at least what required asks.
func (l Level) Covers(required Level) bool {
	return slices.Index(levels, l) >= slices.Index(levels, required)
}

// Deferred is a check a level leaves to a broader one, or one a check
// besides Go's leaves to CI, and why.
type Deferred struct {
	Check string `json:"check"`
	Why   string `json:"why"`
}

func (d Deferred) String() string {
	return d.Check + " (" + d.Why + ")"
}

// scope is what level runs from a selection: the packages it vets, the
// packages whose tests it runs, and each test run it leaves to a broader
// level with why. everything says a module file changed, which reaches every
// package, and slow holds the import paths the policy lists as slow.
func scope(level Level, choices []Choice, everything bool, slow map[string]bool) (vet, tests []string, left []Deferred) {
	all := []string{"./..."}
	if level == Full || everything && level == Affected {
		return all, all, nil
	}
	if everything {
		return all, nil, []Deferred{{Check: "tests of every package", Why: "go.mod or go.sum changed"}}
	}
	for _, choice := range choices {
		vet = append(vet, choice.ImportPath)
		switch {
		case level == Affected:
			tests = append(tests, choice.ImportPath)
		case choice.Imports != "":
			left = append(left, Deferred{Check: "tests of " + choice.ImportPath, Why: "imports " + choice.Imports})
		case slow[choice.ImportPath]:
			left = append(left, Deferred{Check: "tests of " + choice.ImportPath, Why: "a slow package"})
		default:
			tests = append(tests, choice.ImportPath)
		}
	}
	return vet, tests, left
}
