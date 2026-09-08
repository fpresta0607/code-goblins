package pipeline

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestNativeDaemonLockExcludesApplyAndReleasesOnClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.lock")
	if err := os.WriteFile(path, []byte("holder record must remain unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	release, err := lockDaemon(path)
	if err != nil {
		t.Fatal(err)
	}
	r := Reader{Root: dir, Commands: execx.OSRunner{}}
	if unlock, err := r.Idle(context.Background()); !errors.Is(err, ErrBusy) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("held lock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := lockDaemon(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "holder record must remain unchanged" {
		t.Fatalf("lock modified holder record: %s %v", data, err)
	}
}

func TestIdleRefusesDurableActiveAndUnknownRuns(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	if out, err := exec.Command(sqlite, path, `CREATE TABLE runs(status TEXT); INSERT INTO runs VALUES('completed');`).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	r := Reader{Root: dir, Commands: execx.OSRunner{}}
	for _, status := range []string{"running", "pending", "unknown", "completed"} {
		if out, err := exec.Command(sqlite, path, `UPDATE runs SET status=`+sqlString(status)).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %s %v", out, err)
		}
		release, err := r.Idle(context.Background())
		if status == "completed" {
			if err != nil {
				t.Fatal(err)
			}
			if err := release(); err != nil {
				t.Fatal(err)
			}
		} else {
			if !errors.Is(err, ErrBusy) {
				if release != nil {
					release()
				}
				t.Fatalf("%s: %v", status, err)
			}
		}
	}
}
