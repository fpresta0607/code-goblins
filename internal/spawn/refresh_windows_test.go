//go:build windows

package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

// lockFileExclusively holds path open with no sharing, the way another
// process mid-write does, so every read of it fails with a sharing violation
// until the test ends.
func lockFileExclusively(t *testing.T, path string) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.CloseHandle(handle) })
}

func TestAuthRefreshSkipsAnUnreadableTaskRecordAndRefreshesTheRest(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	store := useCredentialStore(t)
	if err := store.Set(auth.Scoped(project, "FLY_API_TOKEN"), "fly_new_token"); err != nil {
		t.Fatal(err)
	}
	live := writeTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	// A sibling record that cannot be read right now must not stop the
	// refresh: the store already changed, and every other live goblin of the
	// project would otherwise stay on its stale snapshot.
	stuck := writeTaskMeta(t, stateDir, "stuck-1", project, "pane-stuck", true)
	lockFileExclusively(t, filepath.Join(stateDir, "stuck-1.meta"))
	refresher := AuthRefresher{StateDir: stateDir, Store: store, Panes: stubPanes{live: map[string]bool{"pane-live": true, "pane-stuck": true}}}
	result, err := refresher.RefreshProject(context.Background(), project)
	if err == nil || !strings.Contains(err.Error(), "stuck-1") {
		t.Fatalf("err = %v, want the unreadable record named", err)
	}
	if len(result.Refreshed) != 1 || result.Refreshed[0].ID != "live-1" {
		t.Fatalf("refreshed = %v, want live-1 refreshed despite the unreadable record", result.Refreshed)
	}
	if _, err := os.Stat(filepath.Join(live.TaskTmp, "auth.ps1")); err != nil {
		t.Fatalf("live task's script = %v, want it regenerated despite the unreadable record", err)
	}
	if _, err := os.Stat(filepath.Join(stuck.TaskTmp, "auth.ps1")); !os.IsNotExist(err) {
		t.Errorf("unreadable task's script = %v, want it untouched", err)
	}
}
