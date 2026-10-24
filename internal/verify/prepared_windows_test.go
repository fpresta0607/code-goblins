package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestPreparedGenerationRejectsJunctions(t *testing.T) {
	for _, location := range []string{"root", "ancestor", "required directory"} {
		t.Run(location, func(t *testing.T) {
			root, inventory := preparedFixture(t)
			link := filepath.Join(t.TempDir(), "junction")
			target := root
			switch location {
			case "root":
				root = link
			case "ancestor":
				target = filepath.Dir(root)
				root = filepath.Join(link, filepath.Base(root))
			case "required directory":
				target = filepath.Join(root, "runtime")
				link = filepath.Join(root, "linked-runtime")
				inventory.Files[0].Path = "linked-runtime/v8_context_snapshot.bin"
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if output, err := execx.CommandContext(ctx, "cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
				t.Fatalf("create fixture junction: %v: %s", err, output)
			}
			t.Cleanup(func() {
				if err := os.Remove(link); err != nil {
					t.Error(err)
				}
			})

			err := ValidatePreparedGeneration(root, inventory)

			if err == nil || !strings.Contains(err.Error(), "reparse point") {
				t.Fatalf("junction passed: %v", err)
			}
		})
	}
}

func TestPreparedGenerationRejectsUnreadableFiles(t *testing.T) {
	for _, name := range []string{PreparedEvidenceFile, "runtime/v8_context_snapshot.bin"} {
		t.Run(name, func(t *testing.T) {
			root, inventory := preparedFixture(t)
			path, err := windows.UTF16PtrFromString(filepath.Join(root, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(handle)
			var overlapped windows.Overlapped
			if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
				t.Fatal(err)
			}

			err = ValidatePreparedGeneration(root, inventory)

			if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
				t.Fatalf("unreadable file = %v, want lock violation", err)
			}
			if err := windows.UnlockFileEx(handle, 0, 1, 0, &overlapped); err != nil {
				t.Fatal(err)
			}
			if err := ValidatePreparedGeneration(root, inventory); err != nil {
				t.Fatalf("readable fixture should pass again: %v", err)
			}
		})
	}
}
