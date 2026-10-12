// Package treetest makes a process tree for the tests of what ends with a
// run: the run's own process, as go test's is, a child it starts that runs
// on, as a test binary does, a relay the child starts and waits for, and a
// helper the relay starts and outlives, as a test's helper outlived the test
// process that started it under cfo gate prepush on 2026-10-12. Each writes
// its pid to a file named for it in the tree's folder, the helper only once
// its relay has exited, and none lives past a minute.
package treetest

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// lifetime is the longest any process of a tree lives.
const lifetime = time.Minute

// Args are the arguments that make a test binary whose TestMain calls Run
// the run's process of a tree in folder. With isBreakaway the child starts
// its relay in a job of its own that lets a process break away, as a
// terminal's job does, and the relay starts the helper out of that job, as
// the MSYS runtime starts what Git Bash runs. The run and its child hold
// until Release lets them end.
func Args(folder string, isBreakaway bool) []string {
	return []string{"treetest", "run", folder, strconv.FormatBool(isBreakaway)}
}

// Run runs this process as the process of a tree its arguments name, and
// exits, when they name one.
func Run() {
	if len(os.Args) < 5 || os.Args[1] != "treetest" {
		return
	}
	folder, isBreakaway := os.Args[3], os.Args[4] == "true"
	start := func(role string, extra ...string) *exec.Cmd {
		return execx.Command(os.Args[0], append([]string{"treetest", role, folder, os.Args[4]}, extra...)...)
	}
	fail := func(err error) {
		_ = os.WriteFile(filepath.Join(folder, "failure"), []byte(err.Error()), 0o600)
		os.Exit(9)
	}
	hold := func() {
		for deadline := time.Now().Add(lifetime); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(filepath.Join(folder, "release")); err == nil {
				break
			}
		}
	}
	switch os.Args[2] {
	case "run":
		say(folder, "run")
		if err := start("child").Start(); err != nil {
			fail(err)
		}
		hold()
	case "child":
		say(folder, "child")
		if isBreakaway {
			if err := joinBreakawayJob(); err != nil {
				fail(err)
			}
		}
		if err := start("relay").Run(); err != nil {
			fail(err)
		}
		hold()
	case "relay":
		helper := start("helper", strconv.Itoa(os.Getpid()))
		if isBreakaway {
			helper.SysProcAttr.CreationFlags |= windows.CREATE_BREAKAWAY_FROM_JOB
		}
		if err := helper.Start(); err != nil {
			fail(err)
		}
	case "helper":
		relay, err := strconv.Atoi(os.Args[5])
		if err != nil {
			fail(err)
		}
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if _, isRunning := proc.StartTime(relay); !isRunning {
				break
			}
			if time.Now().After(deadline) {
				fail(fmt.Errorf("the relay, pid %d, still ran after 10 seconds", relay))
			}
		}
		say(folder, "helper")
		time.Sleep(lifetime)
	}
	os.Exit(0)
}

// joinBreakawayJob puts this process in a job of its own that ends what it
// holds when it closes and lets a process break away from it, as a
// terminal's job is made (conpty).
func joinBreakawayJob() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return err
	}
	return windows.AssignProcessToJobObject(job, windows.CurrentProcess())
}

// say writes this process's pid to the file named for it in folder.
func say(folder, name string) {
	_ = os.WriteFile(filepath.Join(folder, name+".pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
}

// Release lets the run of the tree in folder end.
func Release(folder string) error {
	return os.WriteFile(filepath.Join(folder, "release"), nil, 0o600)
}

// Member is a process of a tree, known by its pid and when it started.
type Member struct {
	PID     int
	Started time.Time
}

// IsRunning reports whether the member still runs.
func (m Member) IsRunning() bool {
	started, isRunning := proc.StartTime(m.PID)
	return m.PID != 0 && isRunning && started.Equal(m.Started)
}

// Ends reports whether the member ends within a moment, as the end of the
// job it is in reaches it.
func (m Member) Ends() bool {
	for deadline := time.Now().Add(15 * time.Second); m.IsRunning(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			return false
		}
	}
	return true
}

// Found is what Find found of a tree: its run's process, the child that
// runs on and its helper, each the zero Member when it was not found.
type Found struct {
	Run, Child, Helper Member
}

// Find waits, for a minute at most, for the tree in folder to hold its
// helper, whose relay has exited, and returns what it found, with why it
// found no more.
func Find(folder string) (Found, error) {
	var found Found
	for _, member := range []struct {
		name string
		into *Member
	}{{"run", &found.Run}, {"child", &found.Child}, {"helper", &found.Helper}} {
		for deadline := time.Now().Add(lifetime); ; time.Sleep(10 * time.Millisecond) {
			if failure, err := fsx.ReadFile(filepath.Join(folder, "failure")); err == nil {
				return found, fmt.Errorf("the tree could not be made: %s", failure)
			}
			data, err := fsx.ReadFile(filepath.Join(folder, member.name+".pid"))
			if err == nil && len(data) > 0 {
				pid, err := strconv.Atoi(string(data))
				if err != nil {
					return found, err
				}
				started, isRunning := proc.StartTime(pid)
				if !isRunning {
					return found, fmt.Errorf("the tree's %s, pid %d, had ended as it said its pid", member.name, pid)
				}
				*member.into = Member{PID: pid, Started: started}
				break
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return found, err
			}
			if time.Now().After(deadline) {
				return found, fmt.Errorf("after a minute the tree's %s had not said its pid", member.name)
			}
		}
	}
	return found, nil
}

// Members are the processes found, the run's first.
func (f Found) Members() []Member {
	return []Member{f.Run, f.Child, f.Helper}
}

// End ends by its pid each process found that still runs.
func (f Found) End() {
	for _, member := range f.Members() {
		if member.IsRunning() {
			_ = proc.TerminateVerified(member.PID, member.Started, func([]string) error { return nil })
		}
	}
}
