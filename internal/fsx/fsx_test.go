package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := AtomicWriteFile(path, []byte("hello\n")); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("content = %q, want %q", got, "hello\n")
	}
}

func TestAtomicWriteFileOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteFile(path, []byte("new")); err != nil {
		t.Fatalf("AtomicWriteFile over existing: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
}

func TestAtomicWriteFileLeavesNoTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	if err := AtomicWriteFile(filepath.Join(dir, "out.txt"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1 (no leftover temp files)", len(entries))
	}
}

// A commit of a new file puts it in a missing place whole, and refuses a
// place already taken without touching what is there, keeping the staged
// file for another try until Discard removes it.
func TestCommitNewPutsTheFileOnlyInAMissingPlace(t *testing.T) {
	for _, existing := range []string{"", "theirs"} {
		t.Run("existing="+existing, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			path := filepath.Join(dir, "record")
			if existing != "" {
				if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			staged, err := Stage(path, []byte("ours"))
			if err != nil {
				t.Fatal(err)
			}

			// Act
			commitErr := staged.CommitNewContext(t.Context())
			staged.Discard()

			// Assert
			want := "ours"
			if existing != "" {
				want = existing
				if !errors.Is(commitErr, os.ErrExist) {
					t.Errorf("commit over an existing file = %v, want os.ErrExist", commitErr)
				}
			} else if commitErr != nil {
				t.Errorf("commit into a missing place = %v", commitErr)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != want {
				t.Errorf("record = %q, %v; want %q", got, err, want)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Errorf("dir has %d entries, want 1 (no leftover staged file)", len(entries))
			}
		})
	}
}

func TestReadLines(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{name: "lf", content: "a\nb\n", want: []string{"a", "b"}},
		{name: "crlf", content: "a\r\nb\r\n", want: []string{"a", "b"}},
		{name: "mixed", content: "a\r\nb\nc", want: []string{"a", "b", "c"}},
		{name: "empty file", content: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f.txt")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := ReadLines(path)
			if err != nil {
				t.Fatalf("ReadLines: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("lines = %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestReadLinesMissingFile(t *testing.T) {
	_, err := ReadLines(filepath.Join(t.TempDir(), "absent.txt"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}
