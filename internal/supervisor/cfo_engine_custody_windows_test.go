package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

func TestCFOEngineCustodyWaitsForUnprovenWork(t *testing.T) {
	for _, test := range []struct {
		name        string
		member      proc.Entry
		services    map[int]proc.Service
		custody     fleettree.Custody
		args        []string
		argumentErr error
		want        string
	}{
		{name: "gate client", member: proc.Entry{PID: 2, ExeBase: "no-mistakes.exe"}, custody: fleettree.CustodyWork, want: "no-mistakes.exe"},
		{name: "protected gate daemon", member: proc.Entry{PID: 2, ExeBase: "no-mistakes.exe"}, services: map[int]proc.Service{2: proc.GateDaemon}},
		{name: "protected daemon worker", member: proc.Entry{PID: 2, ExeBase: "python.exe"}, services: map[int]proc.Service{2: proc.GateDaemon}},
		{name: "detached python", member: proc.Entry{PID: 2, ParentPID: 900, ExeBase: "python.exe"}, custody: fleettree.CustodyWork, want: "python.exe"},
		{name: "node", member: proc.Entry{PID: 2, ExeBase: "node.exe"}, custody: fleettree.CustodyWork, want: "node.exe"},
		{name: "powershell", member: proc.Entry{PID: 2, ExeBase: "powershell.exe"}, custody: fleettree.CustodyWork, want: "powershell.exe"},
		{name: "shell", member: proc.Entry{PID: 2, ExeBase: "bash.exe"}, custody: fleettree.CustodyWork, want: "bash.exe"},
		{name: "worker", member: proc.Entry{PID: 2, ExeBase: "goblins.exe"}, custody: fleettree.CustodyWork, want: "goblins.exe"},
		// A process nothing can place is ended by the close all the same, so
		// it is taken for work.
		{name: "unplaced process", member: proc.Entry{PID: 2, ExeBase: "python.exe"}, want: "python.exe"},
		{name: "harness", member: proc.Entry{PID: 2, ExeBase: "codex.exe"}, custody: fleettree.CustodyHarness},
		{name: "launch shim", member: proc.Entry{PID: 2, ExeBase: "cmd.exe"}, custody: fleettree.CustodyHarness},
		{name: "terminal waker", member: proc.Entry{PID: 2, ParentPID: 10, ExeBase: "goblins.exe"}, args: []string{"cfo-conpty-waker"}},
		{name: "unproven waker parent", member: proc.Entry{PID: 2, ParentPID: 9, ExeBase: "goblins.exe"}, args: []string{"cfo-conpty-waker"}, custody: fleettree.CustodyWork, want: "goblins.exe"},
		{name: "unreadable candidate", member: proc.Entry{PID: 2, ParentPID: 10, ExeBase: "goblins.exe"}, custody: fleettree.CustodyWork, argumentErr: errors.New("access denied"), want: "goblins.exe"},
	} {
		t.Run(test.name, func(t *testing.T) {
			custody := map[int]fleettree.Custody{}
			if test.custody != fleettree.CustodyUnplaced {
				custody[test.member.PID] = test.custody
			}
			reason, err := cfoJobBlocking([]proc.Entry{test.member}, test.services, custody, 10, func(int) ([]string, error) { return test.args, test.argumentErr })
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

// A Codex or pi CFO's program is cmd running an npm .cmd shim, which starts
// the launcher that starts the harness. Those are the terminal's own program
// line, and are replaced by the restart rather than stranded by it.
func TestCFOEngineTakesTheShimAndTheHarnessForTheTerminalsOwnProgram(t *testing.T) {
	_, record := shimTerminal(t, t.TempDir(), "codex")
	reason, err := cfoJobActivity(record)
	if err != nil || reason != "" {
		t.Fatalf("live shim terminal custody = %q, %v", reason, err)
	}
}

// What the harness starts after launching is work, and a restart would strand
// it. Nothing but the launch window tells it from the harness's own MCP
// servers, so with no window to read a child of the program as the harness,
// it is taken for work.
func TestCFOEngineWaitsForAChildItCannotProveIsTheHarness(t *testing.T) {
	shrinkHarnessLaunch(t)
	_, record := shimTerminal(t, t.TempDir(), "codex")
	reason, err := cfoJobActivity(record)
	if err != nil || !strings.Contains(reason, "supervisor.test.exe") || !strings.Contains(reason, "pid ") {
		t.Fatalf("live terminal child custody = %q, %v", reason, err)
	}
}

func shrinkHarnessLaunch(t *testing.T) {
	t.Helper()
	before := cfoHarnessLaunch
	cfoHarnessLaunch = 0
	t.Cleanup(func() { cfoHarnessLaunch = before })
}
