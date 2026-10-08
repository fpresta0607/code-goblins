package gatetest

import (
	"path/filepath"
	"slices"
	"testing"
)

var root = filepath.Join(`C:\`, "repo")

func pkg(path string, imports ...string) Package {
	return Package{ImportPath: "example.com/repo/" + path, Dir: filepath.Join(root, filepath.FromSlash(path)), Imports: imports}
}

var repo = []Package{
	pkg("internal/herdr"),
	pkg("internal/monitor", "example.com/repo/internal/herdr"),
	pkg("internal/reap", "example.com/repo/internal/herdr", "example.com/repo/internal/state"),
	pkg("internal/state"),
	pkg("internal/supervisor", "example.com/repo/internal/monitor"),
	pkg("cmd/cfo", "example.com/repo/internal/reap", "example.com/repo/internal/supervisor"),
}

// A package is tested when a file in its directory changed, testdata and
// embedded files included, and so is every package that imports it directly
// or transitively. CI still runs everything.
func TestSelectTakesChangedPackagesAndTheirDirectImporters(t *testing.T) {
	for name, test := range map[string]struct {
		files []string
		want  []string
	}{
		"a leaf package": {
			files: []string{"internal/reap/classify.go"},
			want:  []string{"example.com/repo/internal/reap (changed)", "example.com/repo/cmd/cfo (imports example.com/repo/internal/reap)"},
		},
		"a core package reaches all its importers": {
			files: []string{"internal/herdr/client.go"},
			want: []string{
				"example.com/repo/internal/herdr (changed)",
				"example.com/repo/cmd/cfo (imports example.com/repo/internal/reap)",
				"example.com/repo/internal/monitor (imports example.com/repo/internal/herdr)",
				"example.com/repo/internal/reap (imports example.com/repo/internal/herdr)",
				"example.com/repo/internal/supervisor (imports example.com/repo/internal/monitor)",
			},
		},
		"a testdata file belongs to its package": {
			files: []string{"internal/state/testdata/task.json"},
			want:  []string{"example.com/repo/internal/state (changed)", "example.com/repo/cmd/cfo (imports example.com/repo/internal/reap)", "example.com/repo/internal/reap (imports example.com/repo/internal/state)"},
		},
		"a changed importer is listed as changed": {
			files: []string{"internal/herdr/client.go", "internal/monitor/service.go"},
			want: []string{
				"example.com/repo/internal/herdr (changed)",
				"example.com/repo/internal/monitor (changed)",
				"example.com/repo/cmd/cfo (imports example.com/repo/internal/reap)",
				"example.com/repo/internal/reap (imports example.com/repo/internal/herdr)",
				"example.com/repo/internal/supervisor (imports example.com/repo/internal/monitor)",
			},
		},
		"files outside every package": {
			files: []string{"README.md", "docs/native-board.md", "frontend/src/app.ts"},
			want:  nil,
		},
		"a deleted package is not tested": {
			files: []string{"internal/retired/old.go"},
			want:  nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			choices, everything := Select(root, "example.com/repo", test.files, repo)

			// Assert
			var got []string
			for _, choice := range choices {
				got = append(got, choice.String())
			}
			if everything || !slices.Equal(got, test.want) {
				t.Errorf("Select = %q, everything %v; want %q", got, everything, test.want)
			}
		})
	}
}

// A package at the module root owns its build inputs and the files its embed
// patterns cover, not every file below it that no other package owns.
func TestSelectGivesARootPackageOnlyItsOwnFiles(t *testing.T) {
	packages := append([]Package{
		{ImportPath: "example.com/repo", Dir: root, EmbedPatterns: []string{"AGENTS.md", "all:.agents/skills", "docs/pipeline.md"}},
		pkg("internal/install", "example.com/repo"),
	}, repo...)
	chosen := []string{"example.com/repo (changed)", "example.com/repo/internal/install (imports example.com/repo)"}
	for name, test := range map[string]struct {
		files []string
		want  []string
	}{
		"an embedded file": {
			files: []string{"AGENTS.md"},
			want:  chosen,
		},
		"a deleted file under an embedded directory": {
			files: []string{".agents/skills/stow/SKILL.md"},
			want:  chosen,
		},
		"a Go file directly in the root": {
			files: []string{"home_files.go"},
			want:  chosen,
		},
		"a file beside the root package that is no build input": {
			files: []string{"README.md", ".no-mistakes.yaml"},
			want:  nil,
		},
		"an unembedded file in a directory with no package": {
			files: []string{"docs/other.md", "frontend/src/app.ts"},
			want:  nil,
		},
		"a deleted package": {
			files: []string{"internal/retired/old.go"},
			want:  nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			choices, everything := Select(root, "example.com/repo", test.files, packages)

			// Assert
			var got []string
			for _, choice := range choices {
				got = append(got, choice.String())
			}
			if everything || !slices.Equal(got, test.want) {
				t.Errorf("Select = %q, everything %v; want %q", got, everything, test.want)
			}
		})
	}
}

// A deleted package is not tested, but a package that still imports it, even
// only from its tests, is.
func TestSelectTakesTheImportersOfADeletedPackage(t *testing.T) {
	// Arrange
	packages := append([]Package{pkg("internal/legacy", "example.com/repo/internal/retired")}, repo...)

	// Act
	choices, everything := Select(root, "example.com/repo", []string{"internal/retired/old.go", "internal/retired/old_test.go"}, packages)

	// Assert
	var got []string
	for _, choice := range choices {
		got = append(got, choice.String())
	}
	if want := []string{"example.com/repo/internal/legacy (imports example.com/repo/internal/retired)"}; everything || !slices.Equal(got, want) {
		t.Errorf("Select = %q, everything %v; want %q", got, everything, want)
	}
}

// go.mod or go.sum can change what every package builds against, so no
// package list bounds it.
func TestSelectTakesEverythingWhenAModuleFileChanged(t *testing.T) {
	for _, file := range []string{"go.mod", "go.sum"} {
		if _, everything := Select(root, "example.com/repo", []string{"internal/reap/classify.go", file}, repo); !everything {
			t.Errorf("Select with %s changed did not take every package", file)
		}
	}
}

func TestSelectReachesTransitiveImporters(t *testing.T) {
	for _, test := range []struct {
		name     string
		files    []string
		packages []Package
		want     []Choice
	}{
		{
			name:     "chain",
			files:    []string{"internal/herdr/client.go"},
			packages: repo,
			want: []Choice{
				{ImportPath: "example.com/repo/internal/herdr"},
				{ImportPath: "example.com/repo/cmd/cfo", Imports: "example.com/repo/internal/reap"},
				{ImportPath: "example.com/repo/internal/monitor", Imports: "example.com/repo/internal/herdr"},
				{ImportPath: "example.com/repo/internal/reap", Imports: "example.com/repo/internal/herdr"},
				{ImportPath: "example.com/repo/internal/supervisor", Imports: "example.com/repo/internal/monitor"},
			},
		},
		{
			name:  "diamond_and_duplicate_edges",
			files: []string{"a/a.go"},
			packages: []Package{
				pkg("a"),
				pkg("b", "example.com/repo/a", "example.com/repo/a"),
				pkg("c", "example.com/repo/a"),
				pkg("d", "example.com/repo/c", "example.com/repo/b"),
			},
			want: []Choice{
				{ImportPath: "example.com/repo/a"},
				{ImportPath: "example.com/repo/b", Imports: "example.com/repo/a"},
				{ImportPath: "example.com/repo/c", Imports: "example.com/repo/a"},
				{ImportPath: "example.com/repo/d", Imports: "example.com/repo/b"},
			},
		},
		{
			name:  "multiple_roots",
			files: []string{"c/c.go", "a/a.go"},
			packages: []Package{
				pkg("a"),
				pkg("b", "example.com/repo/a"),
				pkg("c", "example.com/repo/b"),
				pkg("d", "example.com/repo/b", "example.com/repo/c"),
			},
			want: []Choice{
				{ImportPath: "example.com/repo/a"},
				{ImportPath: "example.com/repo/c"},
				{ImportPath: "example.com/repo/b", Imports: "example.com/repo/a"},
				{ImportPath: "example.com/repo/d", Imports: "example.com/repo/c"},
			},
		},
		{
			name:  "deleted_seed",
			files: []string{"a_retired/old.go", "a_retired/old_test.go", "z_changed/current.go"},
			packages: []Package{
				pkg("z_changed"),
				pkg("b_direct", "example.com/repo/a_retired", "example.com/repo/z_changed"),
				pkg("c_deleted", "example.com/repo/a_retired"),
				pkg("d_indirect", "example.com/repo/b_direct"),
				pkg("e_indirect", "example.com/repo/c_deleted"),
			},
			want: []Choice{
				{ImportPath: "example.com/repo/z_changed"},
				{ImportPath: "example.com/repo/b_direct", Imports: "example.com/repo/z_changed"},
				{ImportPath: "example.com/repo/c_deleted", Imports: "example.com/repo/a_retired"},
				{ImportPath: "example.com/repo/d_indirect", Imports: "example.com/repo/b_direct"},
				{ImportPath: "example.com/repo/e_indirect", Imports: "example.com/repo/c_deleted"},
			},
		},
		{
			name:  "connected_cycle",
			files: []string{"a/a.go"},
			packages: []Package{
				pkg("a", "example.com/repo/c"),
				pkg("b", "example.com/repo/a"),
				pkg("c", "example.com/repo/b"),
			},
			want: []Choice{
				{ImportPath: "example.com/repo/a"},
				{ImportPath: "example.com/repo/b", Imports: "example.com/repo/a"},
				{ImportPath: "example.com/repo/c", Imports: "example.com/repo/b"},
			},
		},
		{
			name:  "unrelated_cycle",
			files: []string{"a/a.go"},
			packages: []Package{
				pkg("a"),
				pkg("b", "example.com/repo/a"),
				pkg("c", "example.com/repo/d"),
				pkg("d", "example.com/repo/c"),
			},
			want: []Choice{
				{ImportPath: "example.com/repo/a"},
				{ImportPath: "example.com/repo/b", Imports: "example.com/repo/a"},
			},
		},
		{
			name:  "shuffled_input",
			files: []string{"a/a.go", "a/a_test.go"},
			packages: []Package{
				pkg("d", "example.com/repo/c", "example.com/repo/b"),
				pkg("c", "example.com/repo/a"),
				pkg("b", "example.com/repo/a", "example.com/repo/a"),
				pkg("a"),
			},
			want: []Choice{
				{ImportPath: "example.com/repo/a"},
				{ImportPath: "example.com/repo/b", Imports: "example.com/repo/a"},
				{ImportPath: "example.com/repo/c", Imports: "example.com/repo/a"},
				{ImportPath: "example.com/repo/d", Imports: "example.com/repo/b"},
			},
		},
		{
			name:     "empty_change",
			packages: repo,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			choices, everything := Select(root, "example.com/repo", test.files, test.packages)

			// Assert
			if everything || !slices.Equal(choices, test.want) {
				t.Errorf("Select = %v, everything %v; want %v", choices, everything, test.want)
			}
		})
	}
}
