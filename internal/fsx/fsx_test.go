package fsx

import (
	"errors"
	"fmt"
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

// replaceSyncFile swaps the flush for flush until the test ends.
func replaceSyncFile(t *testing.T, flush func(*os.File) error) {
	t.Helper()
	t.Cleanup(ReplaceSync(flush))
}

// Stage flushes the staged copy while it is still open and before Commit
// renames it over path: NTFS can journal the rename before it writes cached
// data, and a machine that stopped in between came back with each replaced
// file at full length and holding only zero bytes.
func TestStageFlushesBeforeTheCloseAndTheRename(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	flushes := 0
	replaceSyncFile(t, func(file *os.File) error {
		flushes++
		if _, err := file.Stat(); err != nil {
			t.Errorf("the flush met a closed file: %v", err)
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != "old" {
			t.Errorf("at the flush path holds %q, %v; want the old copy still", got, err)
		}
		return file.Sync()
	})

	// Act
	staged, err := Stage(path, []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	commitErr := staged.Commit()

	// Assert
	if flushes != 1 {
		t.Errorf("Stage flushed %d times, want 1", flushes)
	}
	if commitErr != nil {
		t.Errorf("Commit = %v", commitErr)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "new" {
		t.Errorf("path = %q, %v; want %q", got, err, "new")
	}
}

// A flush that fails fails the stage as a failed write does: the error comes
// back and no staged file is left for anything to rename.
func TestStageThatCannotFlushLeavesNoStagedFile(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	errFlush := errors.New("flush refused")
	replaceSyncFile(t, func(*os.File) error { return errFlush })

	// Act
	staged, err := Stage(filepath.Join(dir, "state.json"), []byte("new"))

	// Assert
	if !errors.Is(err, errFlush) || staged != nil {
		t.Errorf("Stage = %v, %v; want nil and the flush's error", staged, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("dir has %d entries, want 0 (no leftover staged file)", len(entries))
	}
}

// WriteFileSynced flushes the file it wrote while it is still open, and
// returns the flush's error.
func TestWriteFileSyncedFlushesBeforeTheClose(t *testing.T) {
	errFlush := errors.New("flush refused")
	for _, flushErr := range []error{nil, errFlush} {
		t.Run(fmt.Sprint(flushErr), func(t *testing.T) {
			// Arrange
			path := filepath.Join(t.TempDir(), "record")
			flushes := 0
			replaceSyncFile(t, func(file *os.File) error {
				flushes++
				if _, err := file.Stat(); err != nil {
					t.Errorf("the flush met a closed file: %v", err)
				}
				return errors.Join(file.Sync(), flushErr)
			})

			// Act
			err := WriteFileSynced(path, []byte("whole"), 0o600)

			// Assert
			if flushes != 1 {
				t.Errorf("WriteFileSynced flushed %d times, want 1", flushes)
			}
			if !errors.Is(err, flushErr) {
				t.Errorf("WriteFileSynced = %v, want %v", err, flushErr)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "whole" {
				t.Errorf("record = %q, %v; want %q", got, err, "whole")
			}
		})
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
