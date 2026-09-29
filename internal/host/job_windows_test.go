package host

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// launchFromTerminal is a terminal's program that launches a host of its own,
// as a CFO in a native terminal launches a goblin's, and stays open.
func launchFromTerminal(stateDir, id string) {
	record, err := Launch(stateDir, []string{os.Args[0]}, hostEnvironment(), Spec{ID: id, Args: []string{os.Args[0], "echo-child"}, Cols: 80, Rows: 25})
	if err != nil {
		fmt.Println("launch:", err)
	} else {
		fmt.Println("launched", record.HostPID, "contained", record.Contained)
	}
	time.Sleep(time.Minute)
}

// startFromTerminal is a terminal's program that starts an ordinary child,
// writes the child's pid to pidFile, and stays open.
func startFromTerminal(pidFile string) {
	child := exec.Command(os.Args[0], "sleep-child")
	if err := child.Start(); err != nil {
		fmt.Println("start:", err)
		return
	}
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
	time.Sleep(time.Minute)
}

// launchTerminal starts a host whose terminal runs args, ended by pid when the
// test ends.
func launchTerminal(t *testing.T, stateDir string, args ...string) Record {
	t.Helper()
	record, err := Launch(stateDir, []string{os.Args[0]}, hostEnvironment(), Spec{ID: "g1", Args: append([]string{os.Args[0]}, args...), Cols: 80, Rows: 25})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	t.Cleanup(func() { end(record.HostPID) })
	return record
}

// A CFO in a native terminal launches its goblins' hosts, and each must
// outlive that terminal: on 2026-09-29 every host the native CFO launched sat
// in its terminal's kill-on-close job, since that job allowed no breakaway,
// and would have died with the CFO's terminal.
func TestAHostLaunchedInsideATerminalOutlivesIt(t *testing.T) {
	stateDir := t.TempDir()
	outer := launchTerminal(t, stateDir, "launch-host", stateDir, "g2")
	var inner Record
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		record, err := ReadRecord(stateDir, "g2")
		if err == nil {
			inner = record
			break
		}
		if time.Now().After(deadline) {
			screen, _ := ReadScreen(outer)
			t.Fatalf("the terminal's host never recorded itself; the terminal shows:\n%s", strings.Join(screen, "\n"))
		}
	}
	t.Cleanup(func() { end(inner.HostPID) })

	err := Close(stateDir, outer, time.Second)

	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	time.Sleep(time.Second)
	if !running(inner.HostPID) {
		t.Errorf("host pid %d, launched from inside terminal g1, ended with it", inner.HostPID)
	}
}

// Anything else a terminal's program starts stays in the terminal's job and
// ends with it.
func TestAnOrdinaryChildOfATerminalEndsWithIt(t *testing.T) {
	stateDir := t.TempDir()
	pidFile := stateDir + `\child.pid`
	outer := launchTerminal(t, stateDir, "start-child", pidFile)
	var child int
	for deadline := time.Now().Add(30 * time.Second); child == 0; time.Sleep(50 * time.Millisecond) {
		if data, err := os.ReadFile(pidFile); err == nil {
			child, _ = strconv.Atoi(string(data))
		}
		if time.Now().After(deadline) {
			t.Fatal("the terminal's program never started its child")
		}
	}
	t.Cleanup(func() { end(child) })

	err := Close(stateDir, outer, time.Second)

	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !exited(child) {
		t.Errorf("child pid %d of terminal g1 outlived it", child)
	}
}

// launchWhenTold launches a host once a line arrives on its input, so the
// test can put this process in a job first, and says how the launch went.
func launchWhenTold(stateDir, id string) {
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		fmt.Println("read:", err)
		return
	}
	record, err := Launch(stateDir, []string{os.Args[0]}, hostEnvironment(), Spec{ID: id, Args: []string{os.Args[0], "echo-child"}, Cols: 80, Rows: 25})
	if err != nil {
		fmt.Println("launch:", err)
		return
	}
	fmt.Println("launched", record.HostPID, "contained", record.Contained)
}

// A launcher in a job that forbids breaking away cannot give its host a life
// of its own: the host still starts, inside that job, and the launch says so
// rather than leaving the host to die unannounced when the job closes.
func TestALaunchThatCannotBreakAwayReportsIt(t *testing.T) {
	stateDir := t.TempDir()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}
	// Closing the job ends the launcher and the host it could not free.
	t.Cleanup(func() { windows.CloseHandle(job) })
	launcher := exec.Command(os.Args[0], "launch-when-told", stateDir, "g2")
	launcher.Env = os.Environ()
	input, err := launcher.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := launcher.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := launcher.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		_ = launcher.Wait()
	})
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(launcher.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		t.Fatal(err)
	}

	_, _ = input.Write([]byte("go\n"))
	line, _ := bufio.NewReader(output).ReadString('\n')

	fields := strings.Fields(line)
	if len(fields) != 4 || fields[0] != "launched" || fields[3] != "true" {
		t.Fatalf("launcher said %q, want a host launched and contained", line)
	}
	logged, _ := os.ReadFile(filepath.Join(stateDir, "hosts", "g2.log"))
	if !strings.Contains(string(logged), "cannot break away") {
		t.Errorf("host log = %q, want the contained launch reported", logged)
	}
}
