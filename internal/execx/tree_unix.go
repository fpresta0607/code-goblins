//go:build linux || darwin

package execx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// TreeVariable is the environment variable every process of a tree starts
// with (StartTree). It lists the marks of the trees the process is in,
// separated by spaces, so a tree started inside another stays in both.
const TreeVariable = "GOBLINS_TREE"

// gentleFor is how long EndAndWait asks a tree's processes to end
// (SIGTERM) before it ends them (SIGKILL).
const gentleFor = time.Second

// Tree is a started process with everything it starts. Linux and macOS have
// no job to hold a tree in, so the tree is found each time it is read: every
// process that runs with the tree's mark in its environment, every process
// in the process group its first process leads, when it was started in one
// of its own, and everything those started, by their parent chain. A
// process that clears its environment, leaves the group and outlives the
// process between it and the tree is not found
// (docs/plans/2026-11-11-linux-port.md, "Left out on macOS at first").
type Tree struct {
	cmd   *exec.Cmd
	mark  string
	group int

	mu      sync.Mutex
	isDone  bool
	ended   []EndedProcess
	doneErr error
	// marks says of each process read whether it started with the tree's
	// mark. What a process started with never changes, so each is read once.
	marks map[run]bool
	// seen holds every process of the tree read, which TotalProcesses counts.
	seen map[run]bool
	// peak and cpu are the most the tree was read to commit and to have
	// used, under which no later read falls.
	peak uint64
	cpu  time.Duration
}

// run is one process, by its pid and its start, so a reused pid is another.
type run struct {
	pid   int
	start time.Time
}

// StartTree starts cmd, made with Command or CommandContext, as a tree:
// every process it starts inherits the tree's mark (TreeVariable), and a cmd
// started in a process group of its own (Setpgid) keeps what it starts in
// that group, so End ends all of it, a helper whose parent already exited
// included. Nothing ends the tree should this process end first, as a job
// that ends with its last handle does on Windows. A cmd made with
// CommandContext is ended as a tree when its context ends. Once cmd has been
// waited for, the caller ends the tree or releases it.
func StartTree(cmd *exec.Cmd) (*Tree, error) {
	mark := make([]byte, 16)
	if _, err := rand.Read(mark); err != nil {
		return nil, fmt.Errorf("execx: make a tree's mark: %w", err)
	}
	return startMarked(cmd, hex.EncodeToString(mark))
}

// StartNamedTree is StartTree with name as the tree's mark, so a later
// process can find the tree by its name (EndNamedJob) after this one has
// gone. A name a running process already carries is refused before anything
// starts.
func StartNamedTree(cmd *exec.Cmd, name string) (*Tree, error) {
	if name == "" || strings.ContainsAny(name, " \t\n") {
		return nil, fmt.Errorf("execx: a tree's name %q must be one word", name)
	}
	found, err := (&Tree{mark: name}).members()
	if err != nil {
		return nil, err
	}
	if len(found) > 0 {
		return nil, fmt.Errorf("execx: tree %s already runs", name)
	}
	return startMarked(cmd, name)
}

func startMarked(cmd *exec.Cmd, mark string) (*Tree, error) {
	tree := &Tree{cmd: cmd, mark: mark}
	cmd.Env = withMark(cmd.Environ(), mark)
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			_, err := tree.End()
			return err
		}
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if attributes := cmd.SysProcAttr; attributes != nil && attributes.Setpgid && attributes.Pgid == 0 {
		tree.group = cmd.Process.Pid
	}
	return tree, nil
}

// withMark is environment with mark added to the trees TreeVariable lists.
func withMark(environment []string, mark string) []string {
	marks := []string{}
	kept := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if value, isMarks := strings.CutPrefix(entry, TreeVariable+"="); isMarks {
			marks = strings.Fields(value)
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, TreeVariable+"="+strings.Join(append(marks, mark), " "))
}

// isMarked reports whether environment carries mark.
func isMarked(environment []string, mark string) bool {
	for _, entry := range environment {
		if value, isMarks := strings.CutPrefix(entry, TreeVariable+"="); isMarks && slices.Contains(strings.Fields(value), mark) {
			return true
		}
	}
	return false
}

// members reads the processes of the tree now, sorted by pid. A process
// whose environment this process may not read carries no mark it can see,
// and is found only by its group or its parent chain.
func (t *Tree) members() ([]proc.Entry, error) {
	processes, err := proc.Processes()
	if err != nil {
		return nil, fmt.Errorf("execx: list the tree's processes: %w", err)
	}
	marks := make(map[run]bool, len(processes))
	isMember := map[int]bool{}
	children := map[int][]int{}
	for _, process := range processes {
		key := run{process.PID, process.Start}
		hasMark, isKnown := t.marks[key]
		if !isKnown {
			environment, err := proc.Environment(process.PID)
			hasMark = err == nil && isMarked(environment, t.mark)
		}
		marks[key] = hasMark
		if hasMark || (t.group != 0 && process.Group == t.group) {
			isMember[process.PID] = true
		}
		children[process.ParentPID] = append(children[process.ParentPID], process.PID)
	}
	t.marks = marks
	queue := make([]int, 0, len(isMember))
	for pid := range isMember {
		queue = append(queue, pid)
	}
	for len(queue) > 0 {
		pid := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, child := range children[pid] {
			if !isMember[child] {
				isMember[child] = true
				queue = append(queue, child)
			}
		}
	}
	var members []proc.Entry
	for _, process := range processes {
		if isMember[process.PID] {
			members = append(members, process)
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].PID < members[j].PID })
	return members, nil
}

// Usage reads what the tree's processes use now, found as the tree is, not
// by the parent chain alone, so workers whose parents exited are counted.
// The processor time is the most its running processes, with the children
// they collected, were read to have used together: what a process collected
// by none of them used goes with it. The memory is their private memory, and
// its peak the most read. A failed read returns an error, never a zero
// sample.
func (t *Tree) Usage() (TreeUsage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.isDone {
		return TreeUsage{}, errors.New("execx: tree accounting is closed")
	}
	members, err := t.members()
	if err != nil {
		return TreeUsage{}, err
	}
	return t.usageOf(members)
}

// usageOf is Usage over members, as just read.
func (t *Tree) usageOf(members []proc.Entry) (TreeUsage, error) {
	if t.seen == nil {
		t.seen = map[run]bool{}
	}
	var usage TreeUsage
	var cpu time.Duration
	for _, member := range members {
		use, err := proc.UseOf(member.PID)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue // It has exited since it was listed.
		case errors.Is(err, fs.ErrPermission):
			usage.UnreadableMembers++
		case err != nil:
			return TreeUsage{}, fmt.Errorf("execx: read what tree member %d uses: %w", member.PID, err)
		}
		usage.ActiveProcesses++
		t.seen[run{member.PID, member.Start}] = true
		cpu += use.CPU
		usage.CommitBytes += use.PrivateBytes
	}
	t.cpu = max(t.cpu, cpu)
	t.peak = max(t.peak, usage.CommitBytes)
	usage.CPU = t.cpu
	usage.PeakCommitBytes = t.peak
	usage.SampledPrivateWorkingSetBytes = usage.CommitBytes
	usage.TotalProcesses = uint32(len(t.seen))
	return usage, nil
}

// Members names the processes still running in the tree, by name and pid,
// sorted by pid.
func (t *Tree) Members() ([]EndedProcess, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.isDone {
		return nil, errors.New("execx: tree accounting is closed")
	}
	members, err := t.members()
	if err != nil {
		return nil, err
	}
	return endedProcesses(members), nil
}

func endedProcesses(entries []proc.Entry) []EndedProcess {
	processes := make([]EndedProcess, len(entries))
	for index, entry := range entries {
		processes[index] = EndedProcess{Name: entry.ExeBase, PID: entry.PID}
	}
	return processes
}

// End ends every process still in the tree but a machine service and what
// runs under it (proc.RunningServicesAmong), and returns what it ended,
// sorted by pid. Once the tree is ended or released, End does nothing more
// and returns what that did.
func (t *Tree) End() ([]EndedProcess, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.isDone {
		return t.ended, t.doneErr
	}
	t.isDone = true
	ended, _, err := t.endMembers(context.Background(), true, false)
	t.ended = endedProcesses(ended)
	sort.Slice(t.ended, func(i, j int) bool { return t.ended[i].PID < t.ended[j].PID })
	// The tree's end has ended the process, unless it has exited. Ending it
	// again says only that it has ended.
	_ = t.cmd.Process.Kill()
	t.doneErr = err
	return t.ended, t.doneErr
}

// Release lets go of a tree whose process ended by itself: what the process
// left running runs on, as it would have had its process not been started as
// a tree. Once the tree is ended or released, Release does nothing more.
func (t *Tree) Release() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.isDone = true
	return t.doneErr
}

// TerminateAndWait ends every process of the tree, sparing none, and
// returns once none is left, within five seconds or the context's limit.
// An error keeps the tree for a retry.
func (t *Tree) TerminateAndWait(ctx context.Context) (TreeUsage, error) {
	usage, _, err := t.endAndWait(ctx, false, false)
	return usage, err
}

// EndAndWait is TerminateAndWait for a run whose end must not end a machine
// service it started: it ends every process but a machine service and what
// runs under it, as End does, and returns the services it spared once
// nothing else runs in the tree. It asks each process to end (SIGTERM) for
// a second before it ends it (SIGKILL). An error keeps the tree for a retry.
func (t *Tree) EndAndWait(ctx context.Context) (TreeUsage, []EndedProcess, error) {
	return t.endAndWait(ctx, true, true)
}

func (t *Tree) endAndWait(ctx context.Context, shouldSpareServices, isGentle bool) (TreeUsage, []EndedProcess, error) {
	ctx, cancel := awake.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.isDone {
		return TreeUsage{}, nil, errors.New("execx: tree custody is closed")
	}
	_, spared, err := t.endMembers(ctx, shouldSpareServices, isGentle)
	if err != nil {
		return TreeUsage{}, nil, err
	}
	usage, err := t.usageOf(nil)
	if err != nil {
		return TreeUsage{}, nil, err
	}
	t.isDone = true
	return usage, endedProcesses(spared), nil
}

// endMembers ends the tree's processes but, when shouldSpareServices, the
// machine services among them and what runs under them, reading the tree
// again until nothing else is left, within ctx or five seconds, and returns
// what it ended and the services it spared. Each is ended only while it is
// still the process that was read (proc.Signal), so a reused pid never ends
// another process. When isGentle, each is asked to end for gentleFor first.
func (t *Tree) endMembers(ctx context.Context, shouldSpareServices, isGentle bool) (ended, spared []proc.Entry, err error) {
	ctx, cancel := awake.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	began := time.Now()
	for {
		members, err := t.members()
		if err != nil {
			return ended, nil, err
		}
		services := map[int]proc.Service{}
		if shouldSpareServices && len(members) > 0 {
			if services, err = proc.RunningServicesAmong(members); err != nil {
				return ended, nil, err
			}
		}
		spared = spared[:0]
		var rest []int
		signal := syscall.SIGKILL
		if isGentle && time.Since(began) < gentleFor {
			signal = syscall.SIGTERM
		}
		for _, member := range members {
			if services[member.PID] != proc.NoService {
				spared = append(spared, member)
				continue
			}
			rest = append(rest, member.PID)
			if proc.Signal(member.PID, member.Start, signal) == nil && !slices.Contains(ended, member) {
				ended = append(ended, member)
			}
		}
		if len(rest) == 0 {
			return ended, spared, nil
		}
		select {
		case <-ctx.Done():
			return ended, spared, fmt.Errorf("execx: the tree's processes %v outlived its end: %w", rest, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// EndNamedJob ends whatever still runs in the tree named name
// (StartNamedTree) and returns once none of it is left, within five seconds
// or the context's limit. isFound is false when no running process carries
// the name, so nothing of the tree runs that its mark finds.
func EndNamedJob(ctx context.Context, name string) (usage TreeUsage, isFound bool, err error) {
	tree := &Tree{mark: name}
	tree.mu.Lock()
	members, err := tree.members()
	if err == nil {
		usage, err = tree.usageOf(members)
	}
	tree.mu.Unlock()
	if err != nil {
		return TreeUsage{}, false, err
	}
	if len(members) == 0 {
		return usage, false, nil
	}
	usage, err = tree.TerminateAndWait(ctx)
	return usage, true, err
}
