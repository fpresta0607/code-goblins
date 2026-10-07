package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHelperFixture(t *testing.T, dir string, metas ...TaskMeta) {
	t.Helper()
	for _, meta := range metas {
		if meta.Window == "" {
			meta.Window, meta.Harness, meta.Backend, meta.Worktree = "native", "claude", "native", `C:\home\worktrees\app\`+meta.ID
		}
		if err := WriteTaskMeta(dir, meta); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHelperParentRefusesEachCapAndSaysWhenToAskAgain(t *testing.T) {
	cases := []struct {
		name   string
		metas  []TaskMeta
		parent string
		want   []string
	}{
		{"a task that is not live", nil, "g1", []string{"g1 is not a live task"}},
		{"a helper asking for a helper", []TaskMeta{{ID: "g1"}, {ID: "g1-h1", Parent: "g1"}}, "g1-h1", []string{"a helper cannot start helpers", "ask g1"}},
		{"a second helper", []TaskMeta{{ID: "g1"}, {ID: "g1-h1", Parent: "g1"}}, "g1", []string{"already has a helper, g1-h1", "one helper at a time", "cfo helper merge g1", "cfo kill g1-h1"}},
		{"a second helper named in another case", []TaskMeta{{ID: "g1"}, {ID: "g1-h1", Parent: "G1"}}, "g1", []string{"already has a helper, g1-h1"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			writeHelperFixture(t, dir, test.metas...)

			// Act
			_, err := HelperParent(dir, test.parent)

			// Assert
			if err == nil {
				t.Fatal("HelperParent = nil, want refused")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not say %q", err, want)
				}
			}
		})
	}
}

func TestHelperParentAdmitsALiveGoblinWhoseHelpersAreGone(t *testing.T) {
	// Arrange: g1's first helper finished and was cleaned up, which leaves
	// only its status log; another goblin has a helper of its own.
	dir := t.TempDir()
	writeHelperFixture(t, dir, TaskMeta{ID: "g1", Title: "Parent"}, TaskMeta{ID: "g2"}, TaskMeta{ID: "g2-h1", Parent: "g2"})
	if err := AppendStatus(dir, "g1-h1", "done: ready to merge"); err != nil {
		t.Fatal(err)
	}

	// Act
	parent, err := HelperParent(dir, "g1")

	// Assert
	if err != nil || parent.ID != "g1" || parent.Title != "Parent" {
		t.Errorf("HelperParent = %+v, %v; want g1's record", parent, err)
	}
}

func TestNextHelperIDNeverReusesAnIDAStatusLogKeeps(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	writeHelperFixture(t, dir, TaskMeta{ID: "g1"})
	for _, id := range []string{"g1-h1", "g1-h3", "G1-h4x", "g10-h9"} {
		if err := AppendStatus(dir, id, "done: ready to merge"); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	first, err := NextHelperID(dir, "g1")
	fresh, freshErr := NextHelperID(t.TempDir(), "g1")

	// Assert
	if err != nil || first != "g1-h4" {
		t.Errorf("NextHelperID after h1 and h3 = %q, %v; want g1-h4", first, err)
	}
	if freshErr != nil || fresh != "g1-h1" {
		t.Errorf("NextHelperID with no helpers yet = %q, %v; want g1-h1", fresh, freshErr)
	}
}

func TestNextHelperIDRefusesAParentWhoseHelperIDWouldNotFit(t *testing.T) {
	dir := t.TempDir()
	parent := strings.Repeat("p", 62)

	_, err := NextHelperID(dir, parent)

	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("NextHelperID(%d-byte parent) = %v, want too long", len(parent), err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, parent+"-h1.meta")); statErr == nil {
		t.Error("NextHelperID wrote a record")
	}
}
