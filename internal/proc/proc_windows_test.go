package proc

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAncestryIncludesSelfAndParent(t *testing.T) {
	entries, err := Ancestry(os.Getpid(), 16)
	if err != nil {
		t.Fatalf("Ancestry: %v", err)
	}
	if len(entries) < 1 || entries[0].PID != os.Getpid() {
		t.Fatalf("first entry must be self, got %+v", entries)
	}
	if entries[0].ExeBase == "" || entries[0].Start.IsZero() {
		t.Errorf("self entry incomplete: %+v", entries[0])
	}
	if len(entries) >= 2 && entries[1].PID != entries[0].ParentPID {
		t.Errorf("chain broken: %+v", entries[:2])
	}
}

func TestFindAncestorFindsSelfByName(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := baseNoExe(self)
	e, ok := FindAncestor(os.Getpid(), 16, base)
	if !ok || e.PID != os.Getpid() {
		t.Errorf("FindAncestor(%q) = %+v %v, want self", base, e, ok)
	}
}

func TestFindAncestorMissReturnsFalse(t *testing.T) {
	if _, ok := FindAncestor(os.Getpid(), 16, "no-such-process-name-xyz"); ok {
		t.Error("found an ancestor that cannot exist")
	}
}

func TestAncestryOfChildProcessSeesUs(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ping -n 3 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	entries, err := Ancestry(cmd.Process.Pid, 16)
	if err != nil {
		t.Fatalf("Ancestry(child): %v", err)
	}
	found := false
	for _, e := range entries {
		if e.PID == os.Getpid() {
			found = true
		}
	}
	if !found {
		t.Errorf("test process missing from child ancestry: %+v", entries)
	}
}

// Windows PowerShell's Start-Process -Wait waits for every process in the job
// it puts the started process in, including one whose parent has already
// exited, the way a harness leaves a server behind. JobProcesses lists
// exactly those for the waiting shell, and they can be named by their
// command line. The cmd waits a second before starting its background ping,
// because the shell adds it to the job only just after starting it.
func TestJobProcessesListsWhatStartProcessWaitIsWaitingOn(t *testing.T) {
	shell := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Start-Process -FilePath cmd.exe -ArgumentList '/c ping -n 2 127.0.0.1 >NUL & start /b ping -n 30 127.0.0.1 >NUL' -Wait -NoNewWindow")
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shell.Process.Kill(); _, _ = shell.Process.Wait() })

	var ping Entry
	for deadline := time.Now().Add(20 * time.Second); ping.PID == 0 && time.Now().Before(deadline); {
		time.Sleep(250 * time.Millisecond)
		jobbed, err := JobProcesses(shell.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range jobbed {
			if strings.EqualFold(entry.ExeBase, "PING.EXE") && entry.Start.After(time.Now().Add(-time.Minute)) {
				if line, err := CommandLine(entry.PID); err == nil && strings.Contains(line, "-n 30 127.0.0.1") {
					ping = entry
				}
			}
		}
	}
	if ping.PID == 0 {
		t.Fatal("the waiting shell's job never listed the ping its exited cmd left running")
	}
	t.Cleanup(func() {
		if process, err := os.FindProcess(ping.PID); err == nil {
			_ = process.Kill()
		}
	})
	if jobbed, err := JobProcesses(os.Getpid()); err != nil || len(jobbed) != 0 {
		t.Fatalf("a process with no wait of its own = %+v, %v; want nothing", jobbed, err)
	}
}

// A processor-time FILETIME is a duration, not a date: read as a date it
// loses the 1601 epoch offset, and every reading comes back hugely negative.
// Deltas on one process hid that; a sum across processes does not.
func TestCPUTimeIsTheProcessorTimeUsedSinceStart(t *testing.T) {
	start, ok := StartTime(os.Getpid())
	if !ok {
		t.Fatal("StartTime of this process failed")
	}
	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
	}

	used, ok := CPUTime(os.Getpid())
	if !ok {
		t.Fatal("CPUTime of this process failed")
	}
	if used <= 0 {
		t.Fatalf("CPUTime = %s, want the positive processor time this test just spent", used)
	}
	if limit := time.Since(start) * time.Duration(runtime.NumCPU()); used > limit {
		t.Fatalf("CPUTime = %s, more than %s of processor time since the process started", used, limit)
	}
}
