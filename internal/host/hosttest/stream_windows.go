package hosttest

import (
	"os"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
)

// takenLine is the line Claude Code writes as it takes one of his lines,
// which starts its turn.
const takenLine = `{"type":"user","isReplay":true,"uuid":"c1","parent_tool_use_id":null,"message":{"role":"user","content":"go"}}`

// RunStream hosts stream session id under stateDir in this process, its
// program the stand-in StreamChild names, waiting for a line or, with
// isWorking, in a turn, until the test closes it or ends. It returns the
// session's record once its host's state shows it so.
func RunStream(t testing.TB, stateDir, id string, isWorking bool) host.Record {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{program, StreamChild}
	if isWorking {
		args = append(args, takenLine)
	}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = host.Run(stateDir, host.Spec{ID: id, Args: args, IsStream: true})
	}()
	t.Cleanup(func() {
		if record, err := host.ReadRecord(stateDir, id); err == nil {
			_ = host.Close(stateDir, record, 15*time.Second)
		}
		select {
		case <-ended:
		case <-time.After(time.Minute):
			t.Error("the stream session's host did not end")
		}
	})
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
		if record, err := host.ReadRecord(stateDir, id); err == nil {
			if session, err := host.ReadStreamState(record); err == nil && session.IsWorking == isWorking {
				return record
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the stand-in Claude Code's lines never showed in its host's state")
		}
	}
}
