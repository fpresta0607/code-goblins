package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/gatehome"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// The carry the board's Dev Drive setting gives the supervisor runs goblins
// dev-drive move for the Dev Drive folder as a process of its own and reads
// how it ended: done, a gate that stayed busy, which the supervisor carries
// again by itself, or what the move last said of its failure, every line of
// it as one, without what an earlier carry left in the log.
func TestTheBoardsCarryReadsHowTheMoveEnded(t *testing.T) {
	for _, test := range []struct {
		name     string
		rule     standin.Rule
		wantBusy bool
		wantErr  string
	}{
		{name: "the home moved", rule: standin.Rule{Stdout: "gate home: moved\r\n"}},
		{name: "the gate stayed busy", rule: standin.Rule{Stderr: "goblins dev-drive move: gate home: nothing moved\r\n", Exit: exitGateBusy}, wantBusy: true},
		{name: "the copy failed", rule: standin.Rule{Stdout: "gate home: waiting\r\n", Stderr: "goblins dev-drive move: gate home: the copy failed, so nothing moved: disk full\r\n", Exit: 1}, wantErr: "gate home: the copy failed, so nothing moved: disk full"},
		{name: "the stopped daemon did not start again, said over two lines", rule: standin.Rule{Stdout: "gate home: waiting\r\n", Stderr: "goblins dev-drive move: D:\\CodeGoblins is not yet kept to you\r\ngoblins dev-drive move: gate home: nothing moved, and the daemon was stopped and did not start again on C:\\old: the lock is held\r\nno-mistakes daemon start exited 1: no socket\r\n", Exit: 1}, wantErr: "gate home: nothing moved, and the daemon was stopped and did not start again on C:\\old: the lock is held; no-mistakes daemon start exited 1: no socket"},
		{name: "it failed before it named itself", rule: standin.Rule{Stdout: "asked\r\n", Stderr: "the home could not be read\r\n", Exit: 1}, wantErr: "the home could not be read"},
		{name: "it said nothing", rule: standin.Rule{Exit: 2}, wantErr: "goblins dev-drive move exited 2 and said nothing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state")}
			if err := os.MkdirAll(h.State, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.State, gateCarryLog), []byte("goblins dev-drive move: what an earlier carry said\r\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			program := filepath.Join(root, "bin", "goblins-standin.exe")
			if err := os.MkdirAll(filepath.Dir(program), 0o700); err != nil {
				t.Fatal(err)
			}
			standin.Put(t, program)
			record := filepath.Join(root, "calls.log")
			test.rule.Any = true
			var startedIn string
			start := func(_, dir, logPath string, args ...string) (*exec.Cmd, error) {
				startedIn = dir
				log, err := fsx.OpenAppend(logPath, 0o600)
				if err != nil {
					return nil, err
				}
				defer log.Close()
				command := execx.Command(program, args...)
				command.Env = append(os.Environ(), standin.Env(record, test.rule)...)
				command.Stdout, command.Stderr = log, log
				return command, command.Start()
			}

			// Act
			err := boardDevDrive(h, defaultCommandRuntime(), start).CarryGateHome(context.Background(), `D:\CodeGoblins`)

			// Assert
			switch {
			case test.wantBusy:
				if !errors.Is(err, gatehome.ErrGateBusy) {
					t.Errorf("err = %v, want a busy gate", err)
				}
			case test.wantErr == "":
				if err != nil {
					t.Errorf("err = %v, want the carry done", err)
				}
			case err == nil || errors.Is(err, gatehome.ErrGateBusy) || err.Error() != test.wantErr:
				t.Errorf("err = %v, want %q", err, test.wantErr)
			}
			if startedIn != root {
				t.Errorf("started in %s, want the home %s", startedIn, root)
			}
			called, readErr := os.ReadFile(record)
			if want := "goblins-standin dev-drive move --to D:\\CodeGoblins\r\n"; readErr != nil || string(called) != want {
				t.Errorf("ran %q (%v), want %q", called, readErr, want)
			}
		})
	}
}

// The board's Dev Drive setting reads when the machine started, starts the
// drive's startup task and reads whether the task is this build's. Without
// them a missing drive gets the Attach item at once with no start of its
// task, and an old task is never renewed.
func TestTheBoardsDevDriveSettingLooksAfterTheStartupTask(t *testing.T) {
	// Act
	setting := boardDevDrive(home.Home{Root: t.TempDir()}, defaultCommandRuntime(), nil)

	// Assert
	if setting.Boot == nil || setting.StartTask == nil || setting.TaskCurrent == nil {
		t.Fatalf("Boot %v, StartTask %v, TaskCurrent %v; want each set", setting.Boot != nil, setting.StartTask != nil, setting.TaskCurrent != nil)
	}
	if boot := setting.Boot(); !boot.Before(time.Now()) {
		t.Errorf("Boot() = %s, want a time before now", boot)
	}
}
