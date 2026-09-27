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
// embedded files included, and so is every package that imports it directly;
// a package further away is left to CI, which runs everything.
func TestSelectTakesChangedPackagesAndTheirDirectImporters(t *testing.T) {
	for name, test := range map[string]struct {
		files []string
		want  []string
	}{
		"a leaf package": {
			files: []string{"internal/reap/classify.go"},
			want:  []string{"example.com/repo/internal/reap (changed)", "example.com/repo/cmd/cfo (imports example.com/repo/internal/reap)"},
		},
		"a core package reaches its direct importers only": {
			files: []string{"internal/herdr/client.go"},
			want: []string{
				"example.com/repo/internal/herdr (changed)",
				"example.com/repo/internal/monitor (imports example.com/repo/internal/herdr)",
				"example.com/repo/internal/reap (imports example.com/repo/internal/herdr)",
			},
		},
		"a testdata file belongs to its package": {
			files: []string{"internal/state/testdata/task.json"},
			want:  []string{"example.com/repo/internal/state (changed)", "example.com/repo/internal/reap (imports example.com/repo/internal/state)"},
		},
		"a changed importer is listed as changed": {
			files: []string{"internal/herdr/client.go", "internal/monitor/service.go"},
			want: []string{
				"example.com/repo/internal/herdr (changed)",
				"example.com/repo/internal/monitor (changed)",
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
