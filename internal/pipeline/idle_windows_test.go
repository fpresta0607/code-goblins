package pipeline

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if out, err := exec.Command(sqlite, path, runsFixture+`INSERT INTO runs VALUES('01RUN','repo','feat/a','completed',1);`).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	r := Reader{Root: dir, Commands: execx.OSRunner{}}
	for _, test := range []struct {
		status string
		isIdle bool
	}{
		{"running", false},
		{"pending", false},
		{"unknown", false},
		{"completed", true},
		{"failed", true},
		{"cancelled", true},
		{"ci_monitor_interrupted", true},
		{"", false},
	} {
		t.Run(test.status, func(t *testing.T) {
			value := sqlString(test.status)
			if test.status == "" {
				value = "NULL"
			}
			if out, err := exec.Command(sqlite, path, `UPDATE runs SET status=`+value).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %s %v", out, err)
			}
			release, err := r.Idle(context.Background())
			if test.isIdle {
				if err != nil {
					t.Fatal(err)
				}
				if err := release(); err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrBusy) {
				if release != nil {
					release()
				}
				t.Fatalf("%s: %v", test.status, err)
			}
		})
	}
}

const runsFixture = `CREATE TABLE repos(id TEXT, working_path TEXT); CREATE TABLE runs(id TEXT, repo_id TEXT, branch TEXT, status TEXT, created_at INTEGER); INSERT INTO repos VALUES('repo','C:/work/code-goblins');`

func TestIdleNamesTheRunsItWaitsFor(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	for _, test := range []struct {
		name          string
		runs          string
		isDaemonAlive bool
		want          []string
	}{
		{"runs in flight", `('01RUNNING','repo','fix/a','running',2),('01PENDING','repo','feat/b','pending',3),('01DONE','repo','feat/c','completed',1)`, false, []string{"waits for 2 gate runs in flight", "01RUNNING (code-goblins fix/a, running)", "01PENDING (code-goblins feat/b, pending)"}},
		{"runs in flight under a live daemon", `('01RUNNING','repo','fix/a','running',2)`, true, []string{"waits for 1 gate run in flight", "01RUNNING (code-goblins fix/a, running)"}},
		{"a live daemon with no run in flight", `('01DONE','repo','feat/c','completed',1)`, true, []string{"the no-mistakes daemon is running", "no gate run is in flight"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), runsFixture+`INSERT INTO runs VALUES`+test.runs+`;`).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %s %v", out, err)
			}
			lock := filepath.Join(dir, "daemon.lock")
			if test.isDaemonAlive {
				release, err := lockDaemon(lock)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			release, err := Reader{Root: dir, Commands: execx.OSRunner{}}.Idle(context.Background())
			if !errors.Is(err, ErrBusy) {
				if release != nil {
					release()
				}
				t.Fatalf("idle=%v, want a refusal", err)
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not say %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "01DONE") {
				t.Fatalf("refusal names a finished run: %v", err)
			}
			if !test.isDaemonAlive {
				again, err := lockDaemon(lock)
				if err != nil {
					t.Fatalf("a refusal kept the daemon lock: %v", err)
				}
				again()
			}
		})
	}
}
