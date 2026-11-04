package verify

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// A read opens no file whose size and times the worktree's own index already
// holds: on a loaded machine each open costs tens of milliseconds, and a read
// that opened each of a worktree's thousands of files ran past its limit
// (#1615). A file another program holds open with no sharing cannot be
// opened at all, so a read that still reads the source opened none.
func TestReadSourceOpensNoFileTheWorktreesIndexKnowsUnchanged(t *testing.T) {
	// Arrange
	dir := sourceWorktree(t)
	tree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")
	// A file written within the second its index was written in is racily
	// clean, which git must open to know unchanged, so a.txt's times are set
	// well before the index's, as a file's are that has not changed since.
	file := filepath.Join(dir, "a.txt")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, past, past); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "update-index", "--refresh")
	name, err := windows.UTF16PtrFromString(file)
	if err != nil {
		t.Fatal(err)
	}
	held, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(held)

	// Act
	source, err := ReadSource(context.Background(), execx.OSRunner{}, dir)

	// Assert
	if err != nil || source.Tree != tree || !source.IsComplete {
		t.Errorf("ReadSource = %+v, %v; want the commit's own complete tree %s without opening a.txt", source, err, tree)
	}
}
