package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/layout"
)

func TestInstallLaysOutANewHomesData(t *testing.T) {
	cases := map[string]func(t *testing.T) *fixture{
		"a home outside a checkout": func(t *testing.T) *fixture {
			return installedFixture(t, map[string]string{"Path": `C:\Windows`}, codegoblins.Contract, codegoblins.Policy)
		},
		"a checkout": func(t *testing.T) *fixture {
			return newFixture(t, adopterSettings, map[string]string{"Path": `C:\Windows`})
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			f := arrange(t)

			output := f.install()

			data := filepath.Join(f.root, "data")
			for _, path := range []string{layout.Marker, layout.Backlog, "projects", "archive/finished", "archive/parked"} {
				if _, err := os.Stat(filepath.Join(data, filepath.FromSlash(path))); err != nil {
					t.Errorf("data/%s is missing after install: %v", path, err)
				}
			}
			if !strings.Contains(output, "laid out "+data) {
				t.Errorf("install did not report laying out %s:\n%s", data, output)
			}
			if again := f.install(); !strings.Contains(again, "already installed - nothing changed") {
				t.Errorf("a second install changed something:\n%s", again)
			}
		})
	}
}

// The live home is a checkout whose data folder has held the fleet's work
// since before the layout existed: an install must not reorganise it.
func TestInstallLeavesDataFromBeforeTheLayoutAsItIs(t *testing.T) {
	f := newFixture(t, adopterSettings, map[string]string{"Path": `C:\Windows`})
	data := filepath.Join(f.root, "data")
	writeFile(t, filepath.Join(data, "backlog.md"), "# Backlog\n\n## Queued\n- **g1** - the operator's task\n")
	writeFile(t, filepath.Join(data, "g1", "brief.md"), "brief\n")

	output := f.install()

	entries, err := os.ReadDir(data)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, ",") != "backlog.md,g1" {
		t.Errorf("data holds %v after install, want only the operator's backlog.md and g1", names)
	}
	if got := readFile(t, filepath.Join(data, "backlog.md")); got != "# Backlog\n\n## Queued\n- **g1** - the operator's task\n" {
		t.Errorf("install rewrote the backlog: %q", got)
	}
	if !strings.Contains(output, "kept "+data+" as it is") {
		t.Errorf("install did not say it kept the data folder as it is:\n%s", output)
	}
}
