package gatetest

import "testing"

// A policy names files by their path from the repository root: a segment
// matches as path.Match matches it, ** stands for any number of directories,
// none included, and case does not count where the file system ignores it.
func TestMatchesReadsAPathPatternFromTheRepositoryRoot(t *testing.T) {
	for _, test := range []struct {
		pattern, file string
		isFolded      bool
		want          bool
	}{
		{"install.ps1", "install.ps1", false, true},
		{"install.ps1", "Install.PS1", true, true},
		{"install.ps1", "Install.PS1", false, false},
		{"install.ps1", "tools/install.ps1", false, false},
		{"README.md", "docs/README.md", false, false},
		{"docs/**", "docs/install.md", false, true},
		{"docs/**", "docs/images/board/card.webp", false, true},
		{"docs/**", "documents/install.md", false, false},
		{"frontend/**", "frontendx/app.ts", false, false},
		{"**/*.go", "main.go", false, true},
		{"**/*.go", "internal/lock/lock.go", false, true},
		{"**/*.go", "internal/lock/lock.md", false, false},
		{".github/workflows/*.yml", ".github/workflows/go.yml", false, true},
		{".github/workflows/*.yml", ".github/workflows/reusable/build.yml", false, false},
		{"internal/**/testdata/**", "internal/quota/testdata/a/b.json", false, true},
		{"internal/**/testdata/**", "internal/quota/read.go", false, false},
		{"tools/pin-installer.ps1", "tools/pin-installer.ps1", false, true},
		{"[", "[", false, false},
	} {
		if got := matches(test.pattern, test.file, test.isFolded); got != test.want {
			t.Errorf("matches(%q, %q, %t) = %v; want %v", test.pattern, test.file, test.isFolded, got, test.want)
		}
	}
}
