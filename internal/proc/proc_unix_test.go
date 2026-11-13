//go:build linux || darwin

package proc

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// startSleeper starts a program that only sleeps, as a child of this
// process, in dir and with one variable of its own, and ends it with the
// test. The channel closes once the child has ended and been collected.
func startSleeper(t *testing.T, dir string) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("sleep", "600")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOBLINS_PROC_TEST=shown")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	return cmd, exited
}

func TestLookupReadsThisProcesssParentNameAndStart(t *testing.T) {
	// Act
	entry, err := Lookup(os.Getpid())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if entry.PID != os.Getpid() || entry.ParentPID != os.Getppid() {
		t.Errorf("Lookup = pid %d under %d, want %d under %d", entry.PID, entry.ParentPID, os.Getpid(), os.Getppid())
	}
	if name := filepath.Base(os.Args[0]); entry.ExeBase == "" || !strings.HasPrefix(name, entry.ExeBase) {
		t.Errorf("Lookup names the process %q, want the start of %q", entry.ExeBase, name)
	}
	// On Linux the machine's start is given in whole seconds, so a start
	// reads up to a second early.
	if age := time.Since(entry.Start); age < 0 || age > time.Hour {
		t.Errorf("Lookup says the process started %s ago", age)
	}
	if again, _ := Lookup(os.Getpid()); !again.Start.Equal(entry.Start) {
		t.Errorf("two readings of one process gave the starts %s and %s", entry.Start, again.Start)
	}
}

func TestProcessesListsAChildWithItsParentNameAndStart(t *testing.T) {
	// Arrange
	child, _ := startSleeper(t, t.TempDir())
	start, isFound := StartTime(child.Process.Pid)
	if !isFound {
		t.Fatal("the child's start could not be read")
	}

	// Act
	processes, err := Processes()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	at := slices.IndexFunc(processes, func(entry Entry) bool { return entry.PID == child.Process.Pid })
	if at < 0 {
		t.Fatalf("the %d processes listed leave out the child, pid %d", len(processes), child.Process.Pid)
	}
	if got, want := processes[at], (Entry{PID: child.Process.Pid, ParentPID: os.Getpid(), ExeBase: "sleep", Start: start}); got != want {
		t.Errorf("the child is listed as %+v, want %+v", got, want)
	}
}

func TestAncestryOfAChildWalksUpThroughThisProcess(t *testing.T) {
	// Arrange
	child, _ := startSleeper(t, t.TempDir())

	// Act
	entries, err := Ancestry(child.Process.Pid, 3)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, entry := range entries {
		pids = append(pids, entry.PID)
	}
	if want := []int{child.Process.Pid, os.Getpid(), os.Getppid()}; !slices.Equal(pids, want) {
		t.Errorf("Ancestry walked %v, want %v", pids, want)
	}
}

// A process that has exited keeps its ID until its parent collects it. It
// runs nothing, so it reads as gone and is left out of the processes listed,
// never read as a process that could not be read.
func TestAChildThatExitedReadsAsGoneBeforeItIsCollected(t *testing.T) {
	// Arrange
	child := exec.Command("true")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Wait() })

	// Act
	var err error
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if _, err = Lookup(child.Process.Pid); err != nil {
			break
		}
	}

	// Assert
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Lookup of an exited child = %v, want fs.ErrNotExist", err)
	}
	if err := syscall.Kill(child.Process.Pid, 0); err != nil {
		t.Errorf("the child was collected before the test read it, so the test proves nothing: %v", err)
	}
	if _, isFound := StartTime(child.Process.Pid); isFound {
		t.Error("StartTime found an exited child running")
	}
	processes, err := Processes()
	if err != nil {
		t.Errorf("Processes = %v", err)
	}
	if slices.ContainsFunc(processes, func(entry Entry) bool { return entry.PID == child.Process.Pid }) {
		t.Errorf("the processes listed hold the exited child, pid %d", child.Process.Pid)
	}
	if _, err := Instance(child.Process.Pid); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Instance of an exited child = %v, want fs.ErrNotExist", err)
	}
}

func TestIdentifyReadsAChildsImageArgumentsDirectoryAndEnvironment(t *testing.T) {
	// Arrange
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	child, _ := startSleeper(t, dir)
	start, _ := StartTime(child.Process.Pid)
	image, err := filepath.EvalSymlinks(child.Path)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	identity, err := Identify(child.Process.Pid, start)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if identity.Image != image {
		t.Errorf("the child's image reads %q, want %q", identity.Image, image)
	}
	if want := []string{"sleep", "600"}; !slices.Equal(identity.Arguments, want) {
		t.Errorf("the child's arguments read %q, want %q", identity.Arguments, want)
	}
	if identity.Directory != dir {
		t.Errorf("the child's directory reads %q, want %q", identity.Directory, dir)
	}
	if got := identity.Getenv("GOBLINS_PROC_TEST"); got != "shown" {
		t.Errorf("the child's GOBLINS_PROC_TEST reads %q, want it shown", got)
	}
	if _, err := Identify(child.Process.Pid, start.Add(-time.Hour)); err == nil || !strings.Contains(err.Error(), "another program") {
		t.Errorf("Identify of a process started at another time = %v, want it refused as another program", err)
	}
}

// TerminateVerified ends a process only when it shows the start time and
// the arguments it was asked about.
func TestTerminateVerifiedEndsOnlyTheProcessItWasAskedAbout(t *testing.T) {
	// Arrange
	child, exited := startSleeper(t, t.TempDir())
	start, isFound := StartTime(child.Process.Pid)
	if !isFound {
		t.Fatal("the child's start could not be read")
	}
	isRunning := func() bool {
		select {
		case <-exited:
			return false
		case <-time.After(200 * time.Millisecond): // stall-accepted: no exit is meant to arrive, so a stall only makes the wait longer.
			return true
		}
	}
	sleeper := func(arguments []string) error {
		if len(arguments) > 0 && arguments[0] == "sleep" {
			return nil
		}
		return errors.New("not the process asked about")
	}

	// Act and assert
	if err := TerminateVerified(child.Process.Pid, start.Add(-time.Hour), sleeper); err == nil || !isRunning() {
		t.Fatalf("a process whose start time differs was ended (err %v)", err)
	}
	if err := TerminateVerified(child.Process.Pid, start, func([]string) error { return errors.New("refused") }); err == nil || !isRunning() {
		t.Fatalf("a process whose arguments were refused was ended (err %v)", err)
	}
	if err := TerminateVerified(child.Process.Pid, start, sleeper); err != nil {
		t.Fatalf("TerminateVerified = %v, want the process ended", err)
	}
	select {
	case <-exited:
	case <-time.After(30 * time.Second):
		t.Fatal("the process asked about still runs")
	}
}

func TestProcessTreeListsAChildUnderItsParent(t *testing.T) {
	// Arrange
	child, _ := startSleeper(t, t.TempDir())

	// Act
	tree, services, err := ProcessTree(os.Getpid())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) == 0 || tree[0].PID != os.Getpid() {
		t.Fatalf("the tree does not start at this process: %+v", tree)
	}
	if !slices.ContainsFunc(tree, func(process ServiceProcess) bool {
		return process.PID == child.Process.Pid && process.ParentPID == os.Getpid() && process.ExeBase == "sleep"
	}) {
		t.Errorf("the tree leaves out the child, pid %d: %+v", child.Process.Pid, tree)
	}
	if len(services) != 0 {
		t.Errorf("a test and a sleeping child read as machine services: %v", services)
	}
}

// A fleet command is held to a budget of the files it opens (fsx.Opens),
// and what it reads of a process is no fleet file: the kernel makes it up
// as it is read. A lock that reads its holder must cost none of the budget.
func TestReadingAProcessCountsAsNoFleetOpen(t *testing.T) {
	// Arrange
	pid := os.Getpid()
	before := fsx.Opens()

	// Act
	_, lookupErr := Lookup(pid)
	_, instanceErr := Instance(pid)
	_, argumentsErr := Arguments(pid)
	_, environmentErr := Environment(pid)
	_, processesErr := Processes()

	// Assert
	if err := errors.Join(lookupErr, instanceErr, argumentsErr, environmentErr, processesErr); err != nil {
		t.Fatal(err)
	}
	if opened := fsx.Opens() - before; opened != 0 {
		t.Errorf("reading a process counted %d fleet open(s), want none", opened)
	}
}
