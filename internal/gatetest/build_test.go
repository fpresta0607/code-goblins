package gatetest

import (
	"slices"
	"strings"
	"testing"
)

// branch is what git and go say about a branch that changed the given files
// in a module with a leaf package a, a package b that imports it and a
// package c nothing imports.
func branch(changed ...string) findings {
	return findings{
		base:     "1111111111111111111111111111111111111111",
		commit:   "2222222222222222222222222222222222222222",
		root:     root,
		module:   "example.com/repo",
		changed:  changed,
		packages: []Package{pkg("a"), pkg("b", "example.com/repo/a"), pkg("c")},
	}
}

// With no level asked for, the plan runs the level the change requires; a
// level asked for runs instead, and the plan still says what is required.
func TestBuildRunsTheLevelTheChangeRequiresUnlessAnotherIsAskedFor(t *testing.T) {
	for name, test := range map[string]struct {
		asked Level
		level Level
		tests []string
	}{
		"nothing asked for": {"", Affected, []string{"example.com/repo/a", "example.com/repo/b"}},
		"fast":              {Fast, Fast, []string{"example.com/repo/a"}},
		"full":              {Full, Full, []string{"./..."}},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			plan := build(branch("a/a.go"), test.asked)

			// Assert
			if plan.Level != test.level || plan.Required != Affected || !slices.Equal(plan.Tests, test.tests) {
				t.Errorf("build = level %s, required %s, tests %q; want %s, affected, %q", plan.Level, plan.Required, plan.Tests, test.level, test.tests)
			}
		})
	}
}

// A changed module file reaches every package, so the change requires the
// full level, and the fast level asked for anyway vets everything, tests
// nothing and still says that full is required.
func TestBuildRequiresFullWhenAModuleFileChanged(t *testing.T) {
	// Act
	required := build(branch("go.sum"), "")
	fast := build(branch("go.sum"), Fast)

	// Assert
	everything := []string{"./..."}
	if required.Level != Full || required.Required != Full || required.Why != "go.mod or go.sum changed" || !slices.Equal(required.Tests, everything) {
		t.Errorf("build = level %s, required %s (%s), tests %q; want full for both, because go.mod or go.sum changed, testing ./...", required.Level, required.Required, required.Why, required.Tests)
	}
	wantLeft := []string{"tests of every package (go.mod or go.sum changed)"}
	if fast.Level != Fast || fast.Required != Full || len(fast.Tests) != 0 || !slices.Equal(fast.Vet, everything) || !slices.Equal(deferred(fast.Left), wantLeft) {
		t.Errorf("build at fast = level %s, required %s, vet %q, tests %q, left %q; want fast, full, ./... vetted, no tests, %q", fast.Level, fast.Required, fast.Vet, fast.Tests, deferred(fast.Left), wantLeft)
	}
}

// A policy the build cannot be sure it understands widens the run to every
// package and says why, rather than planning by a policy half read.
func TestBuildRequiresFullWhenThePolicyCannotBeRead(t *testing.T) {
	for name, policy := range map[string]string{
		"a later version": `{"version": 99, "slow_packages": ["a"]}`,
		"not JSON":        `slow_packages: [a]`,
		"an empty file":   ``,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			found := branch("a/a.go")
			found.policy, found.hasPolicy = policy, true

			// Act
			plan := build(found, "")

			// Assert
			if plan.Level != Full || plan.Required != Full || !strings.HasPrefix(plan.Why, "config/verify.json ") || !slices.Equal(plan.Tests, []string{"./..."}) {
				t.Errorf("build = level %s, required %s (%s), tests %q; want full for both, the reason naming config/verify.json, testing ./...", plan.Level, plan.Required, plan.Why, plan.Tests)
			}
			if want := "built-in defaults, as config/verify.json at 11111111 cannot be read"; plan.Policy != want {
				t.Errorf("plan.Policy = %q; want %q", plan.Policy, want)
			}
		})
	}
}

// A changed file a classifying policy does not account for makes the change
// require the full level, and the plan names the file: unknown means broader,
// never narrower.
func TestBuildRequiresFullWhenAChangedFileIsUnknownToThePolicy(t *testing.T) {
	for name, test := range map[string]struct {
		changed []string
		why     string
		unknown []string
	}{
		"one file": {
			changed: []string{"a/a.go", "tools/sign.ps1", "README.md"},
			why:     "tools/sign.ps1 is in no package, under no contract and not listed as outside the Go checks",
			unknown: []string{"tools/sign.ps1"},
		},
		"several files": {
			changed: []string{"tools/sign.ps1", "tools/pack.ps1", "Makefile"},
			why:     "Makefile and 2 more files are in no package, under no contract and not listed as outside the Go checks",
			unknown: []string{"Makefile", "tools/pack.ps1", "tools/sign.ps1"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			found := branch(test.changed...)
			found.policy, found.hasPolicy = `{"version": 1, "contracts": [], "outside": [{"paths": ["README.md"], "why": "documentation no Go check reads"}]}`, true

			// Act
			plan := build(found, "")

			// Assert
			if plan.Level != Full || plan.Required != Full || plan.Why != test.why || !slices.Equal(plan.Tests, []string{"./..."}) || !slices.Equal(plan.Unknown, test.unknown) {
				t.Errorf("build = level %s, required %s (%s), tests %q, unknown %q;\nwant full for both because %s, testing ./..., unknown %q", plan.Level, plan.Required, plan.Why, plan.Tests, plan.Unknown, test.why, test.unknown)
			}
		})
	}
}

// Under a classifying policy the plan selects the packages a contract names,
// tests them at the fast level too unless they are slow, as it does a changed
// package, and carries what is outside the Go checks with why. With nothing
// unknown the change requires the affected level.
func TestBuildSelectsByContractAndCarriesWhatIsOutside(t *testing.T) {
	// Arrange
	found := branch("install.ps1", "pack.ps1", "README.md")
	found.policy, found.hasPolicy = `{"version": 1, "slow_packages": ["b"], "contracts": [{"paths": ["install.ps1"], "packages": ["c"], "why": "its tests run the script"}, {"paths": ["pack.ps1"], "packages": ["b"], "why": "its tests run the packer"}], "outside": [{"paths": ["README.md"], "why": "documentation no Go check reads"}]}`, true

	// Act
	required := build(found, "")
	fast := build(found, Fast)

	// Assert
	both := []string{"example.com/repo/b", "example.com/repo/c"}
	if required.Level != Affected || required.Required != Affected || !slices.Equal(required.Tests, both) || len(required.Unknown) != 0 {
		t.Errorf("build = level %s, required %s, tests %q, unknown %q; want affected for both, testing %q, nothing unknown", required.Level, required.Required, required.Tests, required.Unknown, both)
	}
	if len(required.Outside) != 1 || required.Outside[0].String() != "README.md (documentation no Go check reads)" {
		t.Errorf("build outside = %v; want README.md with the policy's reason", required.Outside)
	}
	wantLeft := []string{"tests of example.com/repo/b (a slow package)"}
	if want := []string{"example.com/repo/c"}; !slices.Equal(fast.Tests, want) || !slices.Equal(fast.Vet, both) || !slices.Equal(deferred(fast.Left), wantLeft) {
		t.Errorf("build at fast = vet %q, tests %q, left %q; want %q vetted, %q tested, %q left", fast.Vet, fast.Tests, deferred(fast.Left), both, want, wantLeft)
	}
}

// A slow package is listed by its directory from the repository root, the
// root package as a dot, in any spelling of case, as Windows names
// directories.
func TestBuildFindsASlowPackageByItsDirectoryFromTheRoot(t *testing.T) {
	// Arrange
	found := branch("home.go", "cmd/cfo/main.go", "internal/tickets/read.go")
	found.packages = []Package{{ImportPath: "example.com/repo", Dir: root}, pkg("cmd/cfo"), pkg("internal/tickets")}
	found.policy, found.hasPolicy = `{"version": 1, "slow_packages": [".", "CMD/cfo"]}`, true

	// Act
	plan := build(found, Fast)

	// Assert
	wantLeft := []string{"tests of example.com/repo (a slow package)", "tests of example.com/repo/cmd/cfo (a slow package)"}
	if want := []string{"example.com/repo/internal/tickets"}; !slices.Equal(plan.Tests, want) || !slices.Equal(deferred(plan.Left), wantLeft) {
		t.Errorf("build at fast = tests %q, left %q; want %q and %q", plan.Tests, deferred(plan.Left), want, wantLeft)
	}
	if want := "config/verify.json version 1 at 11111111"; plan.Policy != want {
		t.Errorf("plan.Policy = %q; want %q", plan.Policy, want)
	}
}

func TestBuildLevelsKeepTheTransitiveClosure(t *testing.T) {
	all := []string{"example.com/repo/a", "example.com/repo/b", "example.com/repo/c"}
	for _, test := range []struct {
		name  string
		level Level
		vet   []string
		tests []string
		left  []string
	}{
		{
			name:  "fast",
			level: Fast,
			vet:   all,
			tests: []string{"example.com/repo/a"},
			left: []string{
				"tests of example.com/repo/b (imports example.com/repo/a)",
				"tests of example.com/repo/c (imports example.com/repo/b)",
			},
		},
		{name: "affected", level: Affected, vet: all, tests: all},
		{name: "full", level: Full, vet: []string{"./..."}, tests: []string{"./..."}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			found := branch("a/a.go")
			found.packages = []Package{pkg("a"), pkg("b", "example.com/repo/a"), pkg("c", "example.com/repo/b")}
			found.policy, found.hasPolicy = `{"version": 1, "slow_packages": ["b"]}`, true

			// Act
			plan := build(found, test.level)

			// Assert
			wantChoices := []Choice{
				{ImportPath: "example.com/repo/a"},
				{ImportPath: "example.com/repo/b", Imports: "example.com/repo/a"},
				{ImportPath: "example.com/repo/c", Imports: "example.com/repo/b"},
			}
			if plan.Level != test.level || plan.Required != Affected || !slices.Equal(plan.Choices, wantChoices) ||
				!slices.Equal(plan.Vet, test.vet) || !slices.Equal(plan.Tests, test.tests) || !slices.Equal(deferred(plan.Left), test.left) {
				t.Errorf("build = level %s, required %s, choices %v, vet %q, tests %q, left %q; want %s, affected, %v, %q, %q, %q",
					plan.Level, plan.Required, plan.Choices, plan.Vet, plan.Tests, deferred(plan.Left), test.level, wantChoices, test.vet, test.tests, test.left)
			}
		})
	}
}
