package fleettree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Reader reads goblins' trees. It keeps what it has read of each goblin's
// conversation and processes between reads, so one reader serves the board
// and the monitor alike, and is safe to share.
type Reader struct {
	// Home is the user's home, where every harness keeps its records.
	Home string
	// Recorded is the conversation the board recorded for the task's goblin
	// of its current generation, empty when it recorded none; nil records
	// none.
	Recorded func(state.TaskMeta) string
	// Gate reads a goblin's gate run; nil reads none.
	Gate GateProgress
	// Processes, Listeners and CommandLine read the machine; nil reads it
	// through Windows.
	Processes   func() ([]Process, error)
	Listeners   func() (map[int][]int, error)
	CommandLine func(pid int) (string, error)
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	mu           sync.Mutex
	logs         map[string]*claudeLog
	rolloutMetas map[string]rolloutMeta
	commands     map[processKey]string
	readings     map[processKey]cpuReading
	gates        map[string]gateReading
}

// Goblin is one goblin to read: its task record, the process its terminal
// runs (0 when none is known) and when that process started (zero when
// unknown), and the harness session its terminal names (empty when it names
// none).
type Goblin struct {
	Meta           state.TaskMeta
	HarnessPID     int
	HarnessStarted time.Time
	Session        string
}

// startSlack is how far a process's start may read from the start its
// terminal's host recorded for it.
const startSlack = time.Second

type processKey struct {
	pid     int
	created int64
}

// cpuReading is a job's processor time when last read, and when it was last
// seen using the processor.
type cpuReading struct {
	at, busy time.Time
	cpu      time.Duration
}

// busyShare is the share of one processor a job must use between readings
// to read as working: an idle dev server or a sleeping waiter stays far
// below it, a build or a test run far above. The monitor judges its own
// progress by the same share.
const busyShare = 0.05

// shellStartWindow is how long after its call a background shell's process
// may start and still be matched to it.
const shellStartWindow = 2 * time.Minute

// Read reads one goblin's tree. Evidence that could not be read is named in
// the tree's Unread; err is set when the goblin's processes could not be
// read, which the monitor reports as unreadable progress.
func (r *Reader) Read(ctx context.Context, goblin Goblin) (Tree, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	meta := goblin.Meta
	tree := Tree{TaskID: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Children: []Node{}, FetchedAt: now}

	var processes []Process
	var harness harnessProcesses
	var processErr error
	isRunning := false
	if goblin.HarnessPID != 0 {
		read := r.Processes
		if read == nil {
			read = Processes
		}
		processes, processErr = read()
		if processErr != nil {
			tree.Unread = append(tree.Unread, "processes: "+processErr.Error())
		} else if harness, isRunning = readHarness(goblin.HarnessPID, processes, HarnessLaunch); isRunning && !goblin.HarnessStarted.IsZero() && harness.top.Started.Sub(goblin.HarnessStarted).Abs() > startSlack {
			// The terminal's program ended and Windows gave its id to
			// another process, which is no part of this goblin.
			isRunning = false
			tree.Unread = append(tree.Unread, "processes: the goblin's harness has ended; its process id now names another program")
		} else if isRunning {
			for _, process := range harness.all {
				tree.Memory += process.Memory
			}
			tree.OwnMemory = harness.harness.Memory
		}
	}

	session := goblin.Session
	if session == "" && r.Recorded != nil {
		session = r.Recorded(meta)
	}
	var shellCommands map[string]string
	switch strings.ToLower(meta.Harness) {
	case "claude":
		if session == "" && isRunning {
			session = claudeProcessSession(r.Home, harness.harness)
		}
		path := SessionTranscript(r.Home, "claude", session)
		if path == "" {
			tree.Unread = append(tree.Unread, "conversation: Claude Code names no conversation of this goblin's own")
			break
		}
		log := r.claudeLog(meta.ID, path)
		if err := log.update(); err != nil {
			tree.Unread = append(tree.Unread, "conversation: "+err.Error())
		}
		tree.ConversationAt = log.writtenAt()
		tree.Children = append(tree.Children, log.nodes(now)...)
		shellCommands = log.commands
	case "codex":
		metas := r.rollouts(ctx)
		path := r.codexConversation(ctx, meta.Worktree, session, metas)
		if path == "" {
			tree.Unread = append(tree.Unread, "conversation: no Codex rollout of this goblin's own")
			break
		}
		tree.ConversationAt = WrittenAt(path)
		if root := metas[path].id; root != "" {
			tree.Children = append(tree.Children, codexChildren(metas, root)...)
		}
	case "pi":
		// pi 0.85 records no sub-agents and no background jobs; only its
		// conversation's last write is read, when the terminal names it.
		tree.ConversationAt = WrittenAt(SessionTranscript(r.Home, "pi", session))
	}
	if isRunning {
		// A child of an earlier run of the harness ended with it, however
		// its record left it.
		for i := range tree.Children {
			child := &tree.Children[i]
			if (child.State == Working || child.State == Silent) && child.Started.Before(harness.harness.Started) {
				child.State, child.Finished = Failed, harness.harness.Started
				child.LastLine = "ended with an earlier run of its harness"
			}
		}
		jobs, err := r.jobNodes(harness, shellCommands, tree.Children, now)
		if err != nil {
			tree.Unread = append(tree.Unread, "listening ports: "+err.Error())
		}
		tree.Children = mergeShells(append(tree.Children, jobs...))
		r.forgetEnded(processes)
	}
	if node, ok := r.gateNode(ctx, meta, processes, now); ok {
		tree.Children = append(tree.Children, node)
	} else if reading := r.gates[meta.ID]; reading.err != nil {
		tree.Unread = append(tree.Unread, "gate: "+reading.err.Error())
	}

	tree.SourceUpdatedAt = tree.ConversationAt
	for i := range tree.Children {
		child := &tree.Children[i]
		settle(child, now)
		child.FetchedAt = now
		tree.SourceUpdatedAt = later(tree.SourceUpdatedAt, child.SourceUpdatedAt)
	}
	if isRunning {
		tree.SourceUpdatedAt = now
	}
	sort.SliceStable(tree.Children, func(i, j int) bool {
		return kindOrder[tree.Children[i].Kind] < kindOrder[tree.Children[j].Kind]
	})
	return tree, processErr
}

var kindOrder = map[Kind]int{KindSubagent: 0, KindShell: 1, KindMonitor: 2, KindProcess: 3, KindGate: 4}

// claudeLog keeps one log per goblin, read on from where it stopped while
// the goblin's conversation stays the same.
func (r *Reader) claudeLog(task, path string) *claudeLog {
	if r.logs == nil {
		r.logs = map[string]*claudeLog{}
	}
	log := r.logs[task]
	if log == nil || log.path != path {
		log = newClaudeLog(path)
		r.logs[task] = log
	}
	return log
}

// Forget drops what the reader keeps of tasks no longer live.
func (r *Reader) Forget(live map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for task := range r.logs {
		if !live[task] {
			delete(r.logs, task)
		}
	}
	for task := range r.gates {
		if !live[task] {
			delete(r.gates, task)
		}
	}
}

// claudeProcessSession is the conversation the running Claude Code process
// itself records it is in: Claude Code 2.1 writes ~/.claude/sessions/<pid>.json
// naming its session and its process's creation time, which must be this
// process's, or the record is an earlier process's that had the same id.
func claudeProcessSession(home string, harness Process) string {
	data, err := fsx.ReadFile(filepath.Join(home, ".claude", "sessions", strconv.Itoa(harness.PID)+".json"))
	if err != nil {
		return ""
	}
	var record struct {
		PID       int    `json:"pid"`
		SessionID string `json:"sessionId"`
		ProcStart string `json:"procStart"`
	}
	if json.Unmarshal(data, &record) != nil || record.PID != harness.PID || record.ProcStart != strconv.FormatInt(harness.Created, 10) || !sessionID.MatchString(record.SessionID) {
		return ""
	}
	return record.SessionID
}

// jobNodes are the jobs the harness started, each with what it is doing,
// its memory and whether it is using the processor. err says the listening
// ports could not be read, so no job reads as a dev server.
func (r *Reader) jobNodes(harness harnessProcesses, shellCommands map[string]string, children []Node, now time.Time) ([]Node, error) {
	if len(harness.jobs) == 0 {
		return nil, nil
	}
	listeners := r.Listeners
	if listeners == nil {
		listeners = Listeners
	}
	facts := jobFacts{commands: map[int]string{}}
	listens, err := listeners()
	facts.listens = listens
	commandLine := r.CommandLine
	if commandLine == nil {
		commandLine = proc.CommandLine
	}
	if r.commands == nil {
		r.commands, r.readings = map[processKey]string{}, map[processKey]cpuReading{}
	}
	var nodes []Node
	for _, job := range harness.jobs {
		for _, member := range job.members {
			key := processKey{member.PID, member.Created}
			command, ok := r.commands[key]
			if !ok {
				command, _ = commandLine(member.PID)
				r.commands[key] = command
			}
			facts.commands[member.PID] = command
		}
		group, label, detail := classify(job, facts)
		key := processKey{job.root.PID, job.root.Created}
		cpu := job.cpu()
		reading, seen := r.readings[key]
		share := 0.0
		if elapsed := now.Sub(reading.at); seen && elapsed > 0 {
			share = float64(cpu-reading.cpu) / float64(elapsed)
		} else if age := now.Sub(job.root.Started); age > 0 {
			share = float64(cpu) / float64(age)
		}
		busy := reading.busy
		if busy.IsZero() {
			busy = job.root.Started
		}
		state := Waiting
		if share >= busyShare {
			state, busy = Working, now
		}
		r.readings[key] = cpuReading{at: now, busy: busy, cpu: cpu}
		node := Node{ID: fmt.Sprintf("process:%d:%d", job.root.PID, job.root.Created), Kind: KindProcess, Group: group, Label: label, Detail: detail, State: state, Started: job.root.Started, LastActivity: busy, Memory: job.memory(), SourceUpdatedAt: now, cpu: cpu, process: job.name()}
		if shell := shellOf(job, facts.commands[job.root.PID], shellCommands, children); shell != "" {
			node.Parent = shell
		}
		nodes = append(nodes, node)
	}
	return nodes, err
}

// forgetEnded drops what the reader keeps of processes no longer running,
// whichever goblin they were under: processes is every process running now.
func (r *Reader) forgetEnded(processes []Process) {
	running := make(map[processKey]bool, len(processes))
	for _, process := range processes {
		running[processKey{process.PID, process.Created}] = true
	}
	for key := range r.commands {
		if !running[key] {
			delete(r.commands, key)
		}
	}
	for key := range r.readings {
		if !running[key] {
			delete(r.readings, key)
		}
	}
}

// shellOf is the background shell a job runs for, when the job's first
// process evaluates that shell's command and started with it.
func shellOf(job job, commandLine string, shellCommands map[string]string, children []Node) string {
	command := strings.TrimSpace(shellCommand(commandLine))
	if command == "" {
		return ""
	}
	for _, child := range children {
		if child.Kind != KindShell || child.State != Working && child.State != Silent {
			continue
		}
		started := job.root.Started.Sub(child.Started)
		if strings.TrimSpace(shellCommands[child.ID]) == command && started > -shellStartWindow && started < shellStartWindow {
			return child.ID
		}
	}
	return ""
}

// mergeShells folds each job a background shell runs into that shell: the
// shell carries the job's memory, what it is doing and its processor use,
// and the job is not listed again.
func mergeShells(children []Node) []Node {
	jobs := map[string][]Node{}
	for _, child := range children {
		if child.Kind == KindProcess && child.Parent != "" {
			jobs[child.Parent] = append(jobs[child.Parent], child)
		}
	}
	kept := []Node{}
	for _, child := range children {
		if child.Kind == KindProcess && child.Parent != "" {
			continue
		}
		for _, job := range jobs[child.ID] {
			child.Memory += job.Memory
			child.cpu += job.cpu
			child.process = job.process
			if job.Group != GroupOther {
				child.Group = job.Group
			}
			if job.State == Working {
				child.LastActivity = later(child.LastActivity, job.LastActivity)
			}
		}
		kept = append(kept, child)
	}
	return kept
}

// gateNode reads the goblin's gate at most once per GateEvery.
func (r *Reader) gateNode(ctx context.Context, meta state.TaskMeta, processes []Process, now time.Time) (Node, bool) {
	if r.Gate == nil {
		return Node{}, false
	}
	if r.gates == nil {
		r.gates = map[string]gateReading{}
	}
	reading, ok := r.gates[meta.ID]
	if !ok || now.Sub(reading.at) >= GateEvery {
		reading = gateReading{at: now}
		if branch := worktreeBranch(meta.Worktree); branch != "" {
			reading.progress, reading.err = r.Gate.Progress(ctx, meta.Project, branch)
			// No run of the branch, or no no-mistakes state on this machine
			// at all, is no gate run rather than one that could not be read.
			if errors.Is(reading.err, pipeline.ErrNoProgress) || errors.Is(reading.err, fs.ErrNotExist) {
				reading.err = nil
			}
			if reading.err == nil && reading.progress.RunID != "" {
				reading.steps, reading.err = r.Gate.StepDetails(ctx, reading.progress.RunID)
			}
		}
		r.gates[meta.ID] = reading
	}
	if reading.err != nil {
		return Node{}, false
	}
	return gateNode(reading, generationStart(meta.SpawnGen), processes)
}

// generationStart is when a spawn generation began: cfo spawn names each
// "s" and its Unix time in nanoseconds.
func generationStart(generation string) time.Time {
	digits, ok := strings.CutPrefix(generation, "s")
	nanos, err := strconv.ParseInt(digits, 10, 64)
	if !ok || err != nil {
		return time.Time{}
	}
	return time.Unix(0, nanos).UTC()
}

// RecordedSession is the board's record of one harness session.
type RecordedSession struct {
	NativeID   string `json:"native_id"`
	Harness    string `json:"harness"`
	Role       string `json:"role"`
	TaskID     string `json:"task_id"`
	Generation string `json:"generation"`
}

// Owned is the conversation id the board recorded for the task's goblin of
// its current generation, in a harness that resumes one by its id, or empty
// when session proves no such thing, so nothing reads another task's
// conversation as this one's.
func Owned(meta state.TaskMeta, session RecordedSession) string {
	if meta.SpawnGen == "" || (meta.Harness != "codex" && meta.Harness != "claude") {
		return ""
	}
	if session.TaskID != meta.ID || session.Generation != meta.SpawnGen || session.Harness != meta.Harness || session.Role != "goblin" {
		return ""
	}
	return session.NativeID
}

// OwnedSession is Owned read from the board's record in stateDir. A record
// that cannot be read is an error, never taken as none.
func OwnedSession(stateDir string, meta state.TaskMeta) (string, error) {
	if meta.SpawnGen == "" || (meta.Harness != "codex" && meta.Harness != "claude") {
		return "", nil
	}
	data, err := fsx.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read session ownership: %w", err)
	}
	var database struct {
		Sessions     map[string]RecordedSession `json:"sessions"`
		TaskSessions map[string]string          `json:"task_sessions"`
	}
	if err := json.Unmarshal(data, &database); err != nil {
		return "", fmt.Errorf("read session ownership: %w", err)
	}
	return Owned(meta, database.Sessions[database.TaskSessions[meta.ID]]), nil
}
