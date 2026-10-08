package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// The install's home is chosen by what the machine holds, and every case ends
// in a home rather than a question: a home in use anywhere is kept, and
// otherwise the standard folder is used, as an update where Code Goblins is
// there already, a half-finished install included.
func TestFindTargetKeepsAHomeInUseAndOtherwiseUsesTheStandardFolder(t *testing.T) {
	for name, test := range map[string]struct {
		standard []string
		checkout []string
		cfoHome  string
		want     func(standard, checkout string) Target
	}{
		"nothing installed": {
			want: func(standard, _ string) Target { return Target{Root: standard} },
		},
		"an earlier standard install": {
			standard: []string{home.InstalledMarker, "state/"},
			cfoHome:  "standard",
			want:     func(standard, _ string) Target { return Target{Root: standard, Update: true} },
		},
		"the standard folder named in another spelling": {
			standard: []string{home.InstalledMarker, "state/"},
			cfoHome:  "standard, upper case with a trailing slash",
			want:     func(standard, _ string) Target { return Target{Root: standard, Update: true} },
		},
		"a half-finished install, its marker not yet written": {
			standard: []string{"state/"},
			want:     func(standard, _ string) Target { return Target{Root: standard, Update: true} },
		},
		"a checkout made the home, CFO_HOME naming it": {
			checkout: []string{"AGENTS.md", "cmd/cfo/", "state/", "data/", ".worktrees/", "cfo.exe"},
			cfoHome:  "checkout",
			want:     func(_, checkout string) Target { return Target{Root: checkout, Update: true, Kept: true, Checkout: true} },
		},
		"a home of its own outside the standard folder, CFO_HOME naming it": {
			checkout: []string{home.InstalledMarker, "AGENTS.md", "state/", "bin/"},
			cfoHome:  "checkout",
			want:     func(_, other string) Target { return Target{Root: other, Update: true, Kept: true} },
		},
		"CFO_HOME naming a folder with no fleet": {
			checkout: []string{"AGENTS.md"},
			cfoHome:  "checkout",
			want:     func(standard, _ string) Target { return Target{Root: standard} },
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			standard := filepath.Join(t.TempDir(), "CodeGoblins")
			checkout := filepath.Join(t.TempDir(), "code-goblins")
			lay(t, standard, test.standard)
			lay(t, checkout, test.checkout)
			values := map[string]string{}
			switch test.cfoHome {
			case "standard":
				values[homeVariable] = standard
			case "standard, upper case with a trailing slash":
				values[homeVariable] = strings.ToUpper(standard) + `\`
			case "checkout":
				values[homeVariable] = checkout
			}

			// Act
			got, err := FindTarget(newFakeEnv(values), standard)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if want := test.want(standard, checkout); got != want {
				t.Errorf("FindTarget = %+v, want %+v", got, want)
			}
		})
	}
}

// lay makes each entry under root: a folder where it ends in a slash, and
// otherwise an empty file.
func lay(t *testing.T, root string, entries []string) {
	t.Helper()
	for _, entry := range entries {
		path := filepath.Join(root, filepath.FromSlash(entry))
		if strings.HasSuffix(entry, "/") {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
