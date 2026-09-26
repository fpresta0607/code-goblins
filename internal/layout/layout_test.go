package layout

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// tree lists every path under root, slash-separated, with each file's
// content, so a test can prove a call left a directory exactly as it was.
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	paths := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == root {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content := "<dir>"
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			content = string(data)
		}
		paths[filepath.ToSlash(rel)] = content
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func headings(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if name, ok := strings.CutPrefix(line, "## "); ok {
			found = append(found, name)
		}
	}
	return found
}

var laidOut = []string{Marker, "archive", "archive/finished", "archive/parked", Backlog, "projects"}

func TestEnsureLaysOutANewHome(t *testing.T) {
	cases := map[string]func(t *testing.T, data string){
		"no data folder yet": func(t *testing.T, data string) {},
		"an empty data folder": func(t *testing.T, data string) {
			if err := os.MkdirAll(data, 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"only the shipped lane policy": func(t *testing.T, data string) { write(t, filepath.Join(data, "routing.json"), `{"lanes":[]}`) },
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "data")
			arrange(t, data)

			created, legacy, err := Ensure(data)

			if err != nil || legacy {
				t.Fatalf("Ensure = %v, legacy %v; want a new home laid out", err, legacy)
			}
			slices.Sort(created)
			if !slices.Equal(created, laidOut) {
				t.Errorf("created %v, want %v", created, laidOut)
			}
			for _, path := range laidOut {
				if _, err := os.Stat(filepath.Join(data, filepath.FromSlash(path))); err != nil {
					t.Errorf("%s is missing after Ensure: %v", path, err)
				}
			}
			if got, want := headings(t, filepath.Join(data, Backlog)), []string{"Queued", "Parked", "Done"}; !slices.Equal(got, want) {
				t.Errorf("backlog sections = %v, want %v", got, want)
			}
		})
	}
}

func TestEnsureKeepsTheShippedLanePolicy(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	write(t, filepath.Join(data, "routing.json"), `{"lanes":["tuned"]}`)

	if _, _, err := Ensure(data); err != nil {
		t.Fatal(err)
	}

	if got := tree(t, data)["routing.json"]; got != `{"lanes":["tuned"]}` {
		t.Errorf("routing.json = %q, want it untouched", got)
	}
}

// A data folder that already holds someone's work predates the layout.
// Laying it out means moving their files, which is not an install's call.
func TestEnsureLeavesAHomeFromBeforeTheLayoutUntouched(t *testing.T) {
	cases := map[string]string{
		"a backlog":        "backlog.md",
		"a task folder":    "g1/brief.md",
		"directives":       "overlord.md",
		"project settings": "projects/acme/auth.json",
	}
	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "data")
			write(t, filepath.Join(data, filepath.FromSlash(file)), "the operator's own words\n")
			before := tree(t, data)

			created, legacy, err := Ensure(data)

			if err != nil || !legacy || len(created) != 0 {
				t.Fatalf("Ensure = created %v, legacy %v, err %v; want legacy and nothing created", created, legacy, err)
			}
			if after := tree(t, data); !maps.Equal(before, after) {
				t.Errorf("Ensure changed a home from before the layout:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

func TestEnsureRepairsALaidOutHomeWithoutOverwriting(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	if _, _, err := Ensure(data); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(data, Backlog), "# Backlog\n\n## Queued\n- [ ] g1 - the operator's task\n")
	write(t, filepath.Join(data, "g1", "brief.md"), "brief\n")
	if err := os.RemoveAll(filepath.Join(data, "archive", "parked")); err != nil {
		t.Fatal(err)
	}

	created, legacy, err := Ensure(data)

	if err != nil || legacy {
		t.Fatalf("Ensure = %v, legacy %v; a laid-out home is never legacy", err, legacy)
	}
	if !slices.Equal(created, []string{"archive/parked"}) {
		t.Errorf("created %v, want only the missing archive/parked", created)
	}
	files := tree(t, data)
	if files[Backlog] != "# Backlog\n\n## Queued\n- [ ] g1 - the operator's task\n" || files["g1/brief.md"] != "brief\n" {
		t.Errorf("Ensure overwrote the operator's files: %v", files)
	}
}

func TestEnsureOnACompleteHomeCreatesNothing(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	if _, _, err := Ensure(data); err != nil {
		t.Fatal(err)
	}
	before := tree(t, data)

	created, legacy, err := Ensure(data)

	if err != nil || legacy || len(created) != 0 {
		t.Fatalf("second Ensure = created %v, legacy %v, err %v; want nothing to do", created, legacy, err)
	}
	if after := tree(t, data); !maps.Equal(before, after) {
		t.Errorf("second Ensure changed the home:\nbefore %v\nafter  %v", before, after)
	}
}
