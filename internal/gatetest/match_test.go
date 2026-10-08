package gatetest

import "testing"

// A policy names files by their path from the repository root: a segment
// matches as path.Match matches it, ** stands for any number of directories,
// none included, and case does not count, as Windows names files.
func TestMatchesReadsAPathPatternFromTheRepositoryRoot(t *testing.T) {
	for _, test := range []struct {
		pattern, file string
		want          bool
	}{
		{"install.ps1", "install.ps1", true},
		{"install.ps1", "Install.PS1", true},
		{"install.ps1", "tools/install.ps1", false},
		{"README.md", "docs/README.md", false},
		{"docs/**", "docs/install.md", true},
		{"docs/**", "docs/images/board/card.webp", true},
		{"docs/**", "documents/install.md", false},
		{"frontend/**", "frontendx/app.ts", false},
		{"**/*.go", "main.go", true},
		{"**/*.go", "internal/lock/lock.go", true},
		{"**/*.go", "internal/lock/lock.md", false},
		{".github/workflows/*.yml", ".github/workflows/go.yml", true},
		{".github/workflows/*.yml", ".github/workflows/reusable/build.yml", false},
		{"internal/**/testdata/**", "internal/quota/testdata/a/b.json", true},
		{"internal/**/testdata/**", "internal/quota/read.go", false},
		{"tools/pin-installer.ps1", "tools/pin-installer.ps1", true},
		{"[", "[", false},
	} {
		if got := matches(test.pattern, test.file); got != test.want {
			t.Errorf("matches(%q, %q) = %v; want %v", test.pattern, test.file, got, test.want)
		}
	}
}
