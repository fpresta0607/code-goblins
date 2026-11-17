package proc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The ticks a process's start is counted in are read as hundredths of a
// second, which the kernel tells every process it starts.
func TestTheKernelCountsAProcesssStartInHundredthsOfASecond(t *testing.T) {
	// Arrange
	const clockTicksEntry = 17
	raw, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	var ticks uint64
	for ; len(raw) >= 16; raw = raw[16:] {
		if binary.NativeEndian.Uint64(raw) == clockTicksEntry {
			ticks = binary.NativeEndian.Uint64(raw[8:])
		}
	}

	// Assert
	if ticks != ticksPerSecond {
		t.Errorf("the kernel counts %d ticks to the second, and a start is read as %d", ticks, ticksPerSecond)
	}
}

// A start is pinned to the clock around the spawn, which no reading of
// /proc gives: the machine's start is cut to the second and a process's
// ticks to the tick, so a start reads early by less than a second and a
// tick, and never late.
func TestAChildsStartLiesBetweenTheSecondBeforeItsSpawnAndTheMomentAfterIt(t *testing.T) {
	// Arrange
	before := time.Now()
	child, _ := startSleeper(t, t.TempDir())
	after := time.Now()

	// Act
	start, isFound := StartTime(child.Process.Pid)

	// Assert
	if !isFound {
		t.Fatal("the child's start could not be read")
	}
	if earliest := before.Add(-time.Second - time.Second/ticksPerSecond); start.Before(earliest) || start.After(after) {
		t.Errorf("the child reads as started at %s, want between %s and %s", start.Format(time.RFC3339Nano), earliest.Format(time.RFC3339Nano), after.Format(time.RFC3339Nano))
	}
}

// An instance is the machine's boot ID and the process's start in ticks, as
// the kernel gives them, with no clock in either. proc(5) counts the start
// as the 22nd field of a process's line, and a program named sleep has no
// space in its name to move the count.
func TestAnInstanceIsTheBootIDAndTheStartTicksOfItsProcess(t *testing.T) {
	// Arrange
	child, _ := startSleeper(t, t.TempDir())
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	line, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", child.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(line))
	if len(fields) < 22 {
		t.Fatalf("the child's line %q has no 22nd field", line)
	}
	want := strings.TrimSpace(string(boot)) + "/" + fields[21]

	// Act
	instance, err := Instance(child.Process.Pid)
	again, againErr := Instance(child.Process.Pid)

	// Assert
	if err != nil || againErr != nil {
		t.Fatalf("Instance = %v, then %v", err, againErr)
	}
	if instance != want || again != want {
		t.Errorf("two readings of the child's instance gave %q and %q, want %q", instance, again, want)
	}
	if strings.ContainsAny(instance, " \t\r\n") {
		t.Errorf("the instance %q holds a space, and a waiter's ticket parts its fields at one", instance)
	}
}

// A process whose exec has not yet set up its arguments and environment
// shows the environment's end as 0, as a kernel thread does, which never has
// one; a kernel older than 3.5 shows no end at all.
func TestParseStatusTellsAnExecSettingUpFromAKernelThread(t *testing.T) {
	for _, test := range []struct {
		name            string
		flags, end      string
		fields          int
		shouldBePending bool
	}{
		{"exec in progress", "4194560", "0", 49, true},
		{"environment set up, empty or not", "4194560", "140735", 49, false},
		{"kernel thread", "2129984", "0", 49, false},
		{"kernel before 3.5", "4194560", "", 41, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: the fields after the name, the state first.
			fields := make([]string, test.fields)
			for index := range fields {
				fields[index] = "0"
			}
			fields[0] = "R"
			fields[6] = test.flags
			if test.end != "" {
				fields[48] = test.end
			}
			line := []byte("42 (a (name) here) " + strings.Join(fields, " ") + "\n")

			// Act
			read, err := parseStatus(line)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if read.isExecSettingUp != test.shouldBePending {
				t.Errorf("isExecSettingUp = %t, want %t", read.isExecSettingUp, test.shouldBePending)
			}
		})
	}
}

// A process refused to its own user is read again only while its exec may
// still be setting it up: when it runs wholly as this user, as proc(5)'s
// Uid: line gives its real, effective, saved and filesystem IDs, and
// started under a second before the machine's uptime, both read in
// hundredths of a second.
func TestARefusedProcessIsReadAgainOnlyWhileItStartsAsThisUser(t *testing.T) {
	const (
		user   = 1000
		uptime = "1000.50 3000.00\n"
	)
	for _, test := range []struct {
		name          string
		uptime        string
		uids          string
		started       uint64
		shouldBeRetry bool
	}{
		{"young process of this user", uptime, "1000\t1000\t1000\t1000", 100000, true},
		{"a second old", uptime, "1000\t1000\t1000\t1000", 99950, false},
		{"another user's", uptime, "1001\t1001\t1001\t1001", 100000, false},
		{"setuid, its effective user root", uptime, "1000\t0\t0\t0", 100000, false},
		{"unreadable uptime", "", "1000\t1000\t1000\t1000", 100000, false},
		{"status with no Uid: line", uptime, "", 100000, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			lines := "Name:\tsh\nState:\tR (running)\n"
			if test.uids != "" {
				lines += "Uid:\t" + test.uids + "\n"
			}
			lines += "Gid:\t1000\t1000\t1000\t1000\n"

			// Act
			isRetry := isStartingAs([]byte(test.uptime), []byte(lines), test.started, user)

			// Assert
			if isRetry != test.shouldBeRetry {
				t.Errorf("isStartingAs = %t, want %t", isRetry, test.shouldBeRetry)
			}
		})
	}
}

// A process of this user that refuses its environment to it for a moment
// after its start, as a process part-way through its exec does, is read
// once it shows it: Environment reads it again rather than take the refusal
// for good. The helper is this test binary, which makes itself unreadable
// (PR_SET_DUMPABLE) for 200ms.
func TestEnvironmentReadsAProcessThatRefusesItForAMomentAfterItsStart(t *testing.T) {
	if ready := os.Getenv("PROC_TEST_REFUSING"); ready != "" {
		if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
			os.Exit(7)
		}
		if err := os.WriteFile(ready, nil, 0o600); err != nil {
			os.Exit(7)
		}
		time.Sleep(200 * time.Millisecond)
		if err := unix.Prctl(unix.PR_SET_DUMPABLE, 1, 0, 0, 0); err != nil {
			os.Exit(7)
		}
		time.Sleep(10 * time.Minute)
		os.Exit(9)
	}
	// Arrange
	ready := filepath.Join(t.TempDir(), "ready")
	helper := exec.Command(os.Args[0], "-test.run=^TestEnvironmentReadsAProcessThatRefusesItForAMomentAfterItsStart$")
	helper.Env = append(os.Environ(), "PROC_TEST_REFUSING="+ready, "GOBLINS_PROC_TEST=shown")
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = helper.Process.Kill(); _ = helper.Wait() })
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper did not make itself unreadable within a minute")
		}
	}

	// Act
	environment, err := Environment(helper.Process.Pid)

	// Assert
	if err != nil {
		t.Fatalf("the helper's environment, refused for a moment after its start, read as %v", err)
	}
	if !slices.Contains(environment, "GOBLINS_PROC_TEST=shown") {
		t.Errorf("the helper's environment holds %d entries, without GOBLINS_PROC_TEST=shown", len(environment))
	}
}

// A child this test just started runs as this test's user and is under a
// second old, so its refusal is read as its exec still setting up; init,
// pid 1, runs as root since the machine started, so its refusal is for
// good.
func TestAJustStartedChildReadsAsStartingAsThisUserAndInitDoesNot(t *testing.T) {
	for _, test := range []struct {
		name             string
		pid              func(t *testing.T) int
		shouldBeStarting bool
	}{
		{"just started child", func(t *testing.T) int {
			child, _ := startSleeper(t, t.TempDir())
			return child.Process.Pid
		}, true},
		{"init", func(t *testing.T) int {
			if os.Getuid() == 0 {
				t.Skip("this test runs as root, as init does")
			}
			return 1
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			pid := test.pid(t)
			read, err := readStatus(pid)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			isStarting := isStartingAsThisUser(pid, read)

			// Assert
			if isStarting != test.shouldBeStarting {
				t.Errorf("isStartingAsThisUser(%d) = %t, want %t", pid, isStarting, test.shouldBeStarting)
			}
		})
	}
}

// A child that has exited and is not yet collected keeps its ID as a zombie,
// state Z in its line of /proc (proc(5)), and runs no program, so it has no
// arguments: Arguments refuses it at once, never reads it as started with
// none, and does not take it for an exec setting its arguments up.
func TestArgumentsRefusesAChildThatExitedBeforeItIsCollected(t *testing.T) {
	// Arrange
	child := exec.Command("sleep", "600")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Wait() })
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	statFile := fmt.Sprintf("/proc/%d/stat", child.Process.Pid)
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Millisecond) {
		line, err := os.ReadFile(statFile)
		if err != nil {
			t.Fatalf("the killed child was gone before the test read it, so the test proves nothing: %v", err)
		}
		if fields := strings.Fields(string(line[strings.LastIndexByte(string(line), ')')+1:])); len(fields) > 0 && fields[0] == "Z" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the killed child did not become a zombie within 30 seconds")
		}
	}

	// Act
	started := time.Now()
	arguments, err := Arguments(child.Process.Pid)
	took := time.Since(started)

	// Assert
	if !errors.Is(err, ErrCommandLineUnreadable) {
		t.Errorf("Arguments of an exited child = %q, %v; want an error that wraps ErrCommandLineUnreadable", arguments, err)
	}
	if took >= 500*time.Millisecond {
		t.Errorf("Arguments of an exited child took %s, want it refused at once", took)
	}
}

// A kernel thread runs no program, so it has no arguments and never will:
// kthreadd, pid 2 where this test sees the machine's own processes, reads
// as having none at once, not after waiting out an exec that is not there.
func TestArgumentsReadsAKernelThreadAsHavingNoneAtOnce(t *testing.T) {
	// Arrange
	name, err := os.ReadFile("/proc/2/comm")
	if err != nil || strings.TrimSpace(string(name)) != "kthreadd" {
		t.Skipf("pid 2 is not kthreadd here (%q, %v), as in a sandbox that hides the kernel's threads", name, err)
	}

	// Act
	started := time.Now()
	arguments, err := Arguments(2)
	took := time.Since(started)

	// Assert
	if err != nil || len(arguments) != 0 {
		t.Errorf("Arguments of kthreadd = %q, %v; want none and no error", arguments, err)
	}
	if took >= 500*time.Millisecond {
		t.Errorf("Arguments of kthreadd took %s, want it read at once", took)
	}
}
