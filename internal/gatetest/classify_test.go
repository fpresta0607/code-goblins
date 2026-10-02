package gatetest

import (
	"slices"
	"testing"
)

// classifying is a policy that says what every file of the test repository
// is: two contracts, for a script a package's tests run and for the guards
// that scan every Go file, and what is outside the Go checks.
var classifying = Policy{
	Version: 1,
	Contracts: []Contract{
		{Paths: []string{"install.ps1", "install.cmd"}, Packages: []string{"internal/state"}, Why: "the install tests run these scripts"},
		{Paths: []string{"**/*.go"}, Packages: []string{"internal/herdr"}, Why: "the guard scans every Go file"},
	},
	Outside: []Outside{
		{Paths: []string{"README.md", "docs/**"}, Why: "documentation no Go check reads"},
		{Paths: []string{"frontend/**"}, Why: "the frontend's own checks cover it"},
	},
}

func reached(reach Reach) (choices, outside []string) {
	for _, choice := range reach.Choices {
		choices = append(choices, choice.String())
	}
	for _, set := range reach.Outside {
		outside = append(outside, set.String())
	}
	return choices, outside
}

// Under a policy that classifies files, a changed file is a package's own, or
// under a contract, which selects the packages whose tests read it and says
// so, or listed as outside the Go checks, which the plan shows with the
// reason, or unknown. A package's importers follow a change to the package,
// not a contract on it.
func TestClassifySaysWhatEveryChangedFileReaches(t *testing.T) {
	for name, test := range map[string]struct {
		files   []string
		choices []string
		outside []string
		unknown []string
	}{
		"a docs-only change selects nothing and says why": {
			files:   []string{"README.md", "docs/install.md", "docs/images/card.webp"},
			outside: []string{"README.md, docs/install.md and 1 more (documentation no Go check reads)"},
		},
		"a frontend-only change selects nothing and says why": {
			files:   []string{"frontend/src/app.ts"},
			outside: []string{"frontend/src/app.ts (the frontend's own checks cover it)"},
		},
		"a script under a contract selects the packages that read it": {
			files:   []string{"install.ps1"},
			choices: []string{"example.com/repo/internal/state (the install tests run these scripts: install.ps1)"},
		},
		"a Go change selects its package, the contract's and the importers of its package alone": {
			files: []string{"internal/reap/classify.go", "internal/reap/classify_test.go"},
			choices: []string{
				"example.com/repo/internal/reap (changed)",
				"example.com/repo/internal/herdr (the guard scans every Go file: internal/reap/classify.go and 1 more)",
				"example.com/repo/cmd/cfo (imports example.com/repo/internal/reap)",
			},
		},
		"a package that changed is listed as changed, whatever contract names it": {
			files: []string{"internal/herdr/client.go"},
			choices: []string{
				"example.com/repo/internal/herdr (changed)",
				"example.com/repo/internal/monitor (imports example.com/repo/internal/herdr)",
				"example.com/repo/internal/reap (imports example.com/repo/internal/herdr)",
			},
		},
		"a file nothing accounts for is unknown": {
			files:   []string{"tools/sign.ps1", "README.md"},
			outside: []string{"README.md (documentation no Go check reads)"},
			unknown: []string{"tools/sign.ps1"},
		},
		"a file under a package's testdata is the package's, not unknown": {
			files: []string{"internal/state/testdata/task.json"},
			choices: []string{
				"example.com/repo/internal/state (changed)",
				"example.com/repo/internal/reap (imports example.com/repo/internal/state)",
			},
		},
		"a Go file of a package that is gone reaches its importers and the contract, and is not unknown": {
			files:   []string{"internal/retired/old.go"},
			choices: []string{"example.com/repo/internal/herdr (the guard scans every Go file: internal/retired/old.go)"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			reach := Classify(root, "example.com/repo", test.files, repo, classifying)

			// Assert
			choices, outside := reached(reach)
			if reach.Everything || !slices.Equal(choices, test.choices) || !slices.Equal(outside, test.outside) || !slices.Equal(reach.Unknown, test.unknown) {
				t.Errorf("Classify = choices %q, outside %q, unknown %q, everything %v;\nwant choices %q, outside %q, unknown %q", choices, outside, reach.Unknown, reach.Everything, test.choices, test.outside, test.unknown)
			}
		})
	}
}

// A file a package embeds is the package's, although the policy puts its
// folder outside the Go checks: what Go builds with is never outside them.
func TestClassifyGivesAnEmbeddedFileToItsPackageAlthoughItsFolderIsOutside(t *testing.T) {
	// Arrange
	packages := append([]Package{
		{ImportPath: "example.com/repo", Dir: root, EmbedPatterns: []string{"docs/pipeline.md"}},
		pkg("internal/install", "example.com/repo"),
	}, repo...)

	// Act
	reach := Classify(root, "example.com/repo", []string{"docs/pipeline.md", "docs/install.md"}, packages, classifying)

	// Assert
	choices, outside := reached(reach)
	wantChoices := []string{"example.com/repo (changed)", "example.com/repo/internal/install (imports example.com/repo)"}
	wantOutside := []string{"docs/install.md (documentation no Go check reads)"}
	if !slices.Equal(choices, wantChoices) || !slices.Equal(outside, wantOutside) || len(reach.Unknown) != 0 {
		t.Errorf("Classify = choices %q, outside %q, unknown %q; want %q, %q and nothing unknown", choices, outside, reach.Unknown, wantChoices, wantOutside)
	}
}

// A policy that names no contract and nothing outside the Go checks, as every
// policy did before it could, classifies nothing: the selection is Select's,
// and a file no package owns is neither outside nor unknown.
func TestClassifyLeavesTheSelectionAsItWasUnderAPolicyThatClassifiesNothing(t *testing.T) {
	// Arrange
	files := []string{"internal/herdr/client.go", "README.md", "tools/sign.ps1"}
	want, _ := Select(root, "example.com/repo", files, repo)

	// Act
	reach := Classify(root, "example.com/repo", files, repo, Policy{Version: 1, SlowPackages: []string{"cmd/cfo"}})

	// Assert
	if !slices.Equal(reach.Choices, want) || len(reach.Outside) != 0 || len(reach.Unknown) != 0 || reach.Everything {
		t.Errorf("Classify = %+v; want Select's choices %v and nothing outside or unknown", reach, want)
	}
}

// A changed module file reaches every package whatever the policy says of
// the other files.
func TestClassifyReachesEverythingWhenAModuleFileChanged(t *testing.T) {
	// Act
	reach := Classify(root, "example.com/repo", []string{"go.mod", "tools/sign.ps1"}, repo, classifying)

	// Assert
	if !reach.Everything || len(reach.Choices) != 0 {
		t.Errorf("Classify = %+v; want everything and no list of packages", reach)
	}
}
