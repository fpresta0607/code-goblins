package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

func TestCFOEngineCustodyWaitsForUnprovenWork(t *testing.T) {
	for _, test := range []struct {
		name        string
		member      proc.Entry
		services    map[int]proc.Service
		args        []string
		argumentErr error
		want        string
	}{
		{name: "gate client", member: proc.Entry{PID: 2, ExeBase: "no-mistakes.exe"}, want: "no-mistakes.exe"},
		{name: "protected gate daemon", member: proc.Entry{PID: 2, ExeBase: "no-mistakes.exe"}, services: map[int]proc.Service{2: proc.GateDaemon}},
		{name: "protected daemon worker", member: proc.Entry{PID: 2, ExeBase: "python.exe"}, services: map[int]proc.Service{2: proc.GateDaemon}},
		{name: "detached python", member: proc.Entry{PID: 2, ParentPID: 900, ExeBase: "python.exe"}, want: "python.exe"},
		{name: "node", member: proc.Entry{PID: 2, ExeBase: "node.exe"}, want: "node.exe"},
		{name: "powershell", member: proc.Entry{PID: 2, ExeBase: "powershell.exe"}, want: "powershell.exe"},
		{name: "shell", member: proc.Entry{PID: 2, ExeBase: "bash.exe"}, want: "bash.exe"},
		{name: "worker", member: proc.Entry{PID: 2, ExeBase: "goblins.exe"}, want: "goblins.exe"},
		{name: "terminal waker", member: proc.Entry{PID: 2, ParentPID: 10, ExeBase: "goblins.exe"}, args: []string{"cfo-conpty-waker"}},
		{name: "unproven waker parent", member: proc.Entry{PID: 2, ParentPID: 9, ExeBase: "goblins.exe"}, args: []string{"cfo-conpty-waker"}, want: "goblins.exe"},
		{name: "unreadable candidate", member: proc.Entry{PID: 2, ParentPID: 10, ExeBase: "goblins.exe"}, argumentErr: errors.New("access denied"), want: "goblins.exe"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reason, err := cfoJobBlocking([]proc.Entry{test.member}, test.services, 1, 10, func(int) ([]string, error) { return test.args, test.argumentErr })
			if err != nil || !strings.Contains(reason, test.want) || (test.want == "" && reason != "") {
				t.Fatalf("reason=%q error=%v, want %q", reason, err, test.want)
			}
		})
	}
}

func TestCFOEngineReadsTheActualTerminalJob(t *testing.T) {
	stateDir := t.TempDir()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	typed := filepath.Join(t.TempDir(), "typed.txt")
	hostProgram(t, stateDir, "cfo", typed, program, "-test.run=^TestNativeTerminalProgram$", "--", "native-terminal-program", typed, stateDir)
	record, err := host.ReadRecord(stateDir, "cfo")
	if err != nil {
		t.Fatal(err)
	}
	reason, err := cfoJobActivity(record)
	if err != nil || reason != "" {
		t.Fatalf("idle terminal custody = %q, %v", reason, err)
	}
}

func TestCFOEngineWaitsForLiveUnprovenTerminalChild(t *testing.T) {
	_, record := shimTerminal(t, t.TempDir(), "codex")
	reason, err := cfoJobActivity(record)
	if err != nil || !strings.Contains(reason, "supervisor.test.exe") || !strings.Contains(reason, "pid ") {
		t.Fatalf("live terminal child custody = %q, %v", reason, err)
	}
}
