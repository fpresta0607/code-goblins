package monitor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// TestMain doubles the test binary as the stand-ins the process tests run.
// Copied as lavish-axi.exe it is a poll that waits to be stopped; copied as
// claude.exe or cfo.exe it starts one below itself and prints its pid; run
// busy it keeps a processor busy, the way a build does. A stand-in nobody
// stops ends on its own after a minute.
func TestMain(m *testing.M) {
	switch os.Getenv("CFO_POLL_STANDIN") {
	case "":
		os.Exit(m.Run())
	case "busy":
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); {
		}
		os.Exit(0)
	case "parent":
		child := exec.Command(os.Getenv("CFO_POLL_CHILD"), os.Args[1:]...)
		child.Dir = os.Getenv("CFO_POLL_CHILD_DIR")
		child.Env = append(os.Environ(), "CFO_POLL_STANDIN=wait")
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		fmt.Println(child.Process.Pid)
		if os.Getenv("CFO_POLL_ORPHAN") != "" {
			os.Exit(0)
		}
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

type pollFixture struct {
	t    *testing.T
	root string
	bin  string
}

func newPollFixture(t *testing.T) pollFixture {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return pollFixture{t: t, root: root, bin: bin}
}

// dir makes a folder under the fixture root.
func (f pollFixture) dir(parts ...string) string {
	f.t.Helper()
	path := filepath.Join(append([]string{f.root}, parts...)...)
	if err := os.MkdirAll(path, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return path
}

// standIn copies this test binary into the fixture under name, so the
// process table shows a process of that name.
func (f pollFixture) standIn(name string) string {
	f.t.Helper()
	path := filepath.Join(f.bin, name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	self, err := os.Executable()
	if err != nil {
		f.t.Fatal(err)
	}
	source, err := os.Open(self)
	if err != nil {
		f.t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(path)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		f.t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		f.t.Fatal(err)
	}
	return path
}

// poll starts a lavish-axi stand-in in dir with args and stops it by pid
// when the test ends.
func (f pollFixture) poll(dir string, args ...string) int {
	f.t.Helper()
	command := exec.Command(f.standIn("lavish-axi.exe"), args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "CFO_POLL_STANDIN=wait")
	if err := command.Start(); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})
	return command.Process.Pid
}

// pollUnder starts parent in parentDir, which starts a lavish-axi stand-in in
// childDir with args. Both are stopped by pid when the test ends; an orphaned
// poll's parent exits before this returns.
func (f pollFixture) pollUnder(parent, parentDir, childDir string, orphan bool, args ...string) int {
	f.t.Helper()
	command := exec.Command(parent, args...)
	command.Dir = parentDir
	command.Env = append(os.Environ(), "CFO_POLL_STANDIN=parent", "CFO_POLL_CHILD="+f.standIn("lavish-axi.exe"), "CFO_POLL_CHILD_DIR="+childDir)
	if orphan {
		command.Env = append(command.Env, "CFO_POLL_ORPHAN=1")
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		f.t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		f.t.Fatalf("the parent stand-in printed no child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		f.t.Fatalf("the parent stand-in printed %q, want the child pid", line)
	}
	f.t.Cleanup(func() {
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
		}
	})
	if orphan {
		if err := command.Wait(); err != nil {
			f.t.Fatalf("the orphaning parent failed: %v", err)
		}
	}
	return pid
}

// listed runs the production prober and returns what it reports for pid,
// ignoring every other process on the machine.
func listed(t *testing.T, pid int) (Poll, bool) {
	t.Helper()
	polls, err := ProcessPolls{}.Polls(context.Background())
	if err != nil {
		t.Fatalf("Polls: %v", err)
	}
	for _, poll := range polls {
		if poll.PID == pid {
			return poll, true
		}
	}
	return Poll{}, false
}

func sameFile(t *testing.T, left, right string) bool {
	t.Helper()
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(leftInfo, rightInfo)
}

func TestProcessPollsListsAPollRunningInAGoblinsWorktree(t *testing.T) {
	f := newPollFixture(t)
	worktree := f.dir("app", ".worktrees", "gb-pollfix-1")
	page := filepath.Join(f.dir("app", ".worktrees", "gb-pollfix-1", ".lavish"), "my plan.html")
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	pid := f.poll(worktree, "poll", `.lavish\my plan.html`, "--timeout-ms", "600000")

	poll, ok := listed(t, pid)
	if !ok {
		t.Fatalf("the poll (pid %d) running in gb-pollfix-1 was not listed", pid)
	}
	if poll.Task != "pollfix-1" || poll.Start.IsZero() {
		t.Errorf("poll = %+v, want goblin pollfix-1 with its start time", poll)
	}
	if !sameFile(t, poll.Page, page) {
		t.Errorf("page = %q, want the relative page resolved against the poll's folder, %q", poll.Page, page)
	}
}

// A goblin that changed folder before polling is still placed, by the
// harness it runs under, which runs in its worktree.
func TestProcessPollsPlacesAPollByTheGoblinAboveIt(t *testing.T) {
	f := newPollFixture(t)
	worktree := f.dir("app", ".worktrees", "gb-pollfix-2")
	elsewhere := f.dir("pages")

	pid := f.pollUnder(f.standIn("claude.exe"), worktree, elsewhere, false, "poll", filepath.Join(elsewhere, "plan.html"))

	dir, err := proc.WorkingDirectory(pid)
	if err != nil || worktreeTask(dir) != "" {
		t.Fatalf("the poll runs in %q (%v); the fixture needs it outside every worktree", dir, err)
	}
	if poll, ok := listed(t, pid); !ok || poll.Task != "pollfix-2" {
		t.Fatalf("poll = %+v, %v; want it placed with pollfix-2 by the claude above it", poll, ok)
	}
}

// The supervisor polls a page wait for the CFO, which is the flow a goblin
// should have used, so a poll cfo started is never flagged, even one running
// in a goblin's worktree.
func TestProcessPollsNeverListsAPollCFOStarted(t *testing.T) {
	f := newPollFixture(t)
	worktree := f.dir("app", ".worktrees", "gb-pollfix-3")

	pid := f.pollUnder(f.standIn("cfo.exe"), worktree, worktree, false, "poll", filepath.Join(worktree, "plan.html"), "--timeout-ms", "300000")

	chain, err := proc.Ancestry(pid, 2)
	if err != nil || len(chain) != 2 || !strings.EqualFold(chain[1].ExeBase, "cfo.exe") {
		t.Fatalf("chain = %+v (%v); the fixture needs the poll directly under cfo.exe", chain, err)
	}
	if dir, err := proc.WorkingDirectory(pid); err != nil || worktreeTask(dir) != "pollfix-3" {
		t.Fatalf("the poll runs in %q (%v); the fixture needs it in gb-pollfix-3 so only cfo keeps it out", dir, err)
	}
	if poll, ok := listed(t, pid); ok {
		t.Fatalf("the supervisor's poll was listed as a goblin's: %+v", poll)
	}
}

func TestProcessPollsListsNothingButAGoblinsPoll(t *testing.T) {
	f := newPollFixture(t)
	worktree := f.dir("app", ".worktrees", "gb-pollfix-4")
	elsewhere := f.dir("pages")

	server := f.poll(worktree, "server", "--port", "4387")
	orphan := f.pollUnder(f.standIn("launcher.exe"), elsewhere, elsewhere, true, "poll", filepath.Join(elsewhere, "plan.html"))

	if args, err := proc.Arguments(server); err != nil || len(args) < 2 || args[1] != "server" {
		t.Fatalf("server stand-in arguments = %q (%v), want it running", args, err)
	}
	if chain, err := proc.Ancestry(orphan, pollAncestry); err != nil || len(chain) != 1 {
		t.Fatalf("orphan chain = %+v (%v), want the poll alone once its parent exited", chain, err)
	}
	if poll, ok := listed(t, server); ok {
		t.Errorf("the review server was listed as a poll: %+v", poll)
	}
	if poll, ok := listed(t, orphan); ok {
		t.Errorf("a poll in no goblin's worktree, with nothing above it, was listed: %+v", poll)
	}
}
