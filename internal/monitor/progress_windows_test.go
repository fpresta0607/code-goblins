package monitor

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// The fakes above stand for a process table; this reads the real one, with
// this test process as the harness and a busy stand-in it started as its job.
func TestHarnessJobsReadsTheLiveProcessTable(t *testing.T) {
	fixture := newPollFixture(t)
	command := exec.Command(fixture.standIn("bash.exe"))
	command.Env = append(os.Environ(), "CFO_POLL_STANDIN=busy")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})
	time.Sleep(300 * time.Millisecond)

	processes, err := proc.Processes()
	if err != nil {
		t.Fatal(err)
	}
	jobs, used := harnessJobs(os.Getpid(), processes, 0, proc.StartTime, proc.CPUTime)
	want := "bash.exe (pid " + strconv.Itoa(command.Process.Pid) + ")"
	if !slices.Contains(jobs, want) {
		t.Fatalf("jobs = %v, want %s", jobs, want)
	}
	if used < 100*time.Millisecond {
		t.Errorf("processor time = %s, want the busy child's", used)
	}
}
