package gatetest

import (
	"slices"
	"strings"
	"testing"
)

// A level is named by one of three words, and anything else is refused with
// the three, so a mistyped level never runs as some other one.
func TestParseLevelKnowsTheThreeLevels(t *testing.T) {
	for name, want := range map[string]Level{"fast": Fast, "affected": Affected, "full": Full} {
		if got, err := ParseLevel(name); err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, name := range []string{"", "quick", "Full", "all"} {
		_, err := ParseLevel(name)
		if err == nil || !strings.Contains(err.Error(), "fast, affected or full") {
			t.Errorf("ParseLevel(%q) error = %v; want a refusal naming fast, affected or full", name, err)
		}
	}
}

// A run satisfies what a change requires only at that level or a broader one.
func TestALevelCoversItselfAndTheNarrowerLevels(t *testing.T) {
	for _, test := range []struct {
		ran, required Level
		want          bool
	}{
		{Fast, Fast, true}, {Fast, Affected, false}, {Fast, Full, false},
		{Affected, Fast, true}, {Affected, Affected, true}, {Affected, Full, false},
		{Full, Fast, true}, {Full, Affected, true}, {Full, Full, true},
	} {
		if got := test.ran.Covers(test.required); got != test.want {
			t.Errorf("%s covers %s = %v; want %v", test.ran, test.required, got, test.want)
		}
	}
}

var selection = []Choice{
	{ImportPath: "example.com/repo/cmd/cfo"},
	{ImportPath: "example.com/repo/internal/tickets"},
	{ImportPath: "example.com/repo/internal/install", Imports: "example.com/repo/internal/tickets"},
}

var slowPackages = map[string]bool{"example.com/repo/cmd/cfo": true}

func deferred(left []Deferred) []string {
	var lines []string
	for _, one := range left {
		lines = append(lines, one.String())
	}
	return lines
}

// The fast level vets everything the change reaches, runs the tests of the
// changed packages that are quick, and names each test run it leaves to the
// affected level with why, so nothing drops out of sight.
func TestScopeAtFastTestsTheQuickChangedPackagesAndNamesWhatItLeaves(t *testing.T) {
	// Act
	vet, tests, left := scope(Fast, selection, false, slowPackages)

	// Assert
	wantVet := []string{"example.com/repo/cmd/cfo", "example.com/repo/internal/tickets", "example.com/repo/internal/install"}
	wantTests := []string{"example.com/repo/internal/tickets"}
	wantLeft := []string{
		"tests of example.com/repo/cmd/cfo (a slow package)",
		"tests of example.com/repo/internal/install (imports example.com/repo/internal/tickets)",
	}
	if !slices.Equal(vet, wantVet) || !slices.Equal(tests, wantTests) || !slices.Equal(deferred(left), wantLeft) {
		t.Errorf("scope(fast) = vet %q, tests %q, left %q; want %q, %q, %q", vet, tests, deferred(left), wantVet, wantTests, wantLeft)
	}
}

// The affected level vets and tests every package the change reaches, slow
// ones included, and leaves nothing.
func TestScopeAtAffectedTestsEveryPackageTheChangeReaches(t *testing.T) {
	// Act
	vet, tests, left := scope(Affected, selection, false, slowPackages)

	// Assert
	want := []string{"example.com/repo/cmd/cfo", "example.com/repo/internal/tickets", "example.com/repo/internal/install"}
	if !slices.Equal(vet, want) || !slices.Equal(tests, want) || len(left) != 0 {
		t.Errorf("scope(affected) = vet %q, tests %q, left %q; want %q for both and nothing left", vet, tests, deferred(left), want)
	}
}

// The full level takes every package whatever changed, and so does the
// affected level once a module file changed, because that reaches them all.
func TestScopeTakesEveryPackageAtFullOrWhenAModuleFileChanged(t *testing.T) {
	for name, test := range map[string]struct {
		level      Level
		everything bool
	}{
		"full for a small change":             {Full, false},
		"full when a module file changed":     {Full, true},
		"affected when a module file changed": {Affected, true},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			vet, tests, left := scope(test.level, selection, test.everything, slowPackages)

			// Assert
			if want := []string{"./..."}; !slices.Equal(vet, want) || !slices.Equal(tests, want) || len(left) != 0 {
				t.Errorf("scope = vet %q, tests %q, left %q; want ./... for both and nothing left", vet, tests, deferred(left))
			}
		})
	}
}

// The fast level after a module file changed vets every package and runs no
// test, and says so rather than looking as if there were none to run.
func TestScopeAtFastAfterAModuleFileChangedVetsEverythingAndSaysTheTestsAreLeft(t *testing.T) {
	// Act
	vet, tests, left := scope(Fast, nil, true, slowPackages)

	// Assert
	wantLeft := []string{"tests of every package (go.mod or go.sum changed)"}
	if !slices.Equal(vet, []string{"./..."}) || len(tests) != 0 || !slices.Equal(deferred(left), wantLeft) {
		t.Errorf("scope(fast, module file changed) = vet %q, tests %q, left %q; want ./..., no tests, %q", vet, tests, deferred(left), wantLeft)
	}
}
