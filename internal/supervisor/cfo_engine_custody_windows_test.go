package supervisor

import (
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

func TestCFOEngineCustodyOnlyWaitsForWorkInTheCFOJob(t *testing.T) {
	for _, test := range []struct {
		name        string
		members     []proc.Entry
		services    map[int]proc.Service
		args        []string
		argumentErr error
		want        string
		wantError   bool
	}{
		{name: "no gate in the job"},
		{name: "CFO gate client", members: []proc.Entry{{PID: 2, ExeBase: "no-mistakes.exe"}}, want: "gate command"},
		{name: "machine gate daemon", members: []proc.Entry{{PID: 2, ExeBase: "no-mistakes.exe"}}, services: map[int]proc.Service{2: proc.GateDaemon}},
		{name: "gate worker under daemon", members: []proc.Entry{{PID: 2, ExeBase: "cfo.exe"}}, services: map[int]proc.Service{2: proc.GateDaemon}},
		{name: "pipeline command", members: []proc.Entry{{PID: 2, ExeBase: "cfo.exe"}}, args: []string{"cfo.exe", "pipeline", "run"}, want: "gate command"},
		{name: "contained worker", members: []proc.Entry{{PID: 2, ExeBase: "goblins.exe"}}, args: []string{"goblins.exe", "host"}, want: "worker contained"},
		{name: "ordinary CFO command", members: []proc.Entry{{PID: 2, ExeBase: "cfo.exe"}}, args: []string{"cfo.exe", "drain"}},
		{name: "unreadable command", members: []proc.Entry{{PID: 2, ExeBase: "cfo.exe"}}, args: []string{"cfo.exe"}, wantError: true},
		{name: "process access denied", members: []proc.Entry{{PID: 2, ExeBase: "cfo.exe"}}, argumentErr: errors.New("access denied"), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reason, err := cfoJobBlocking(test.members, test.services, 1, func(int) ([]string, error) { return test.args, test.argumentErr })
			if (err != nil) != test.wantError || !strings.Contains(reason, test.want) {
				t.Fatalf("reason=%q error=%v, want %q error=%v", reason, err, test.want, test.wantError)
			}
		})
	}
}

func TestCFOEngineReadsTheActualTerminalJob(t *testing.T) {
	_, record := shimTerminal(t, t.TempDir(), "codex")
	reason, err := cfoJobActivity(record)
	if err != nil || reason != "" {
		t.Fatalf("idle terminal custody = %q, %v", reason, err)
	}
}
