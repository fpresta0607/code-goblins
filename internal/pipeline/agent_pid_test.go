package pipeline

import (
	"os"
	"testing"
)

// deadAgentPID is above every supported platform's pid_max, so no process
// holds it and the liveness read must call it dead.
const deadAgentPID = 12345678

func TestReadAgentProcessStatusReadsThisProcessAndAGonePid(t *testing.T) {
	if got := readAgentProcessStatus(os.Getpid()); got != agentProcessAlive {
		t.Fatalf("this running test process read %v, not alive", got)
	}
	if got := readAgentProcessStatus(deadAgentPID); got != agentProcessDead {
		t.Fatalf("pid %d, above every supported pid_max, read %v, not dead", deadAgentPID, got)
	}
}
