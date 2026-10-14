package fleettree

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
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
	// Awaited is what the goblin's latest report says it waits on, which
	// may be a gate run's ID, empty when it reports no wait; nil reads none.
	Awaited func(state.TaskMeta) string
	// Processes, Listeners and CommandLine read the machine; nil reads it
	// through Windows.
	Processes   func() ([]Process, error)
	Listeners   func() (map[int][]int, error)
	CommandLine func(pid int) (string, error)
	// Owner names the task whose terminal's mark a process carries, empty
	// when it carries none of this home's. Nil reads none, and no detached
	// job is shown. It is asked once for each process that reaches no
	// harness, since a mark never changes.
	Owner func(pid int) string
	// Console reads the process id of the host of the console a process is
	// attached to; nil reads it through Windows.
	Console func(pid int) (int, error)
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	mu           sync.Mutex
	owners       map[processKey]string
	logs         map[string]*claudeLog
	records      map[string]childRecords
	rolloutMetas map[string]rolloutMeta
	commands     map[processKey]string
	readings     map[processKey]cpuReading
	gates        map[string]gateReading
	usual        map[string]time.Duration
	usualAt      time.Time
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

// cpuReading is a job's processor time when last judged, when it was last
// seen using the processor, and whether it was working then.
type cpuReading struct {
	at, busy  time.Time
	cpu       time.Duration
	isWorking bool
}

// busyShare is the share of one processor a job must use between readings
// to read as working: an idle dev server or a sleeping waiter stays far
// below it, a build or a test run far above. The monitor judges its own
// progress by the same share.
const busyShare = 0.05

// judgeEvery is the shortest span a job's processor use is judged over, the
// monitor's own: two reads can land a second apart, and a burst caught in
// that second would otherwise pass for work.
const judgeEvery = time.Minute

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

	processes, harness, isRunning, isReused, processErr := r.harnessOf(goblin)
	switch {
	case processErr != nil:
		tree.Unread = append(tree.Unread, "processes: "+processErr.Error())
	case isReused:
		tree.Unread = append(tree.Unread, "processes: the goblin's harness has ended; its process id now names another program")
	case isRunning:
		for _, process := range harness.all {
			tree.Memory += process.Memory
		}
		tree.OwnMemory = harness.harness.Memory
	}

	path, metas := r.conversation(ctx, goblin, harness.harness, isRunning)
	var commands map[string]string
	records := childRecords{harness: strings.ToLower(meta.Harness), children: map[string]childRecord{}}
	switch strings.ToLower(meta.Harness) {
	case "claude":
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
		commands = log.commands
		maps.Copy(records.children, log.records)
	case "codex":
		if path == "" {
			tree.Unread = append(tree.Unread, "conversation: no Codex rollout of this goblin's own")
			break
		}
		tree.ConversationAt = WrittenAt(path)
		if root := metas[path].id; root != "" {
			children, rollouts := codexChildren(metas, root)
			tree.Children = append(tree.Children, children...)
			for node, rollout := range rollouts {
				records.children[node] = childRecord{transcript: rollout}
			}
		}
	case "pi":
		// pi 0.85 records no sub-agents and no background jobs; only its
		// conversation's last write is read, when the terminal names it.
		tree.ConversationAt = WrittenAt(path)
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
		jobs, err := r.jobNodes(harness, commands, tree.Children, now)
		if err != nil {
			tree.Unread = append(tree.Unread, "listening ports: "+err.Error())
		}
		// countsIn is the child each process of the harness's jobs counts in.
		countsIn := map[int]string{}
		for i, job := range harness.jobs {
			for _, member := range job.members {
				countsIn[member.PID] = cmp.Or(jobs[i].Parent, jobs[i].ID)
			}
		}
		// A detached job is the goblin's work. A run or a script at work in
		// one is, as a test suite a script started in the background is. A
		// server or a browser left detached serves or draws whatever the
		// goblin does, so its processor use says nothing of the goblin's
		// work. Git Bash runs each program of a pipeline or a loop by
		// replacing its own process, which leaves the program no parent, so
		// a detached job counts in the child whose processes host the console
		// it is attached to.
		detached := r.detachedJobs(meta.ID, processes, harness)
		left, _ := r.jobNodes(harnessProcesses{jobs: detached}, commands, tree.Children, now)
		for i := range left {
			node := &left[i]
			node.Detached = true
			if node.Group == GroupDevServer || node.Group == GroupBrowser {
				node.cpu, node.process, node.outsideWork = 0, "", true
			}
			tree.Memory += node.Memory
			if node.Parent == "" {
				node.Parent = countsIn[r.console(detached[i].root.PID)]
			}
		}
		jobs = append(jobs, left...)
		runs := map[string]bool{}
		for _, job := range jobs {
			runs[job.Parent] = true
		}
		tree.Children, tree.own = drawn(mergeShells(append(tree.Children, jobs...)))
		launched := harness.top.Started.Add(HarnessLaunch)
		for i := range tree.Children {
			endUnseen(&tree.Children[i], commands, runs, launched, now)
		}
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
		if _, isKept := records.children[child.ID]; !isKept && child.lines != nil {
			records.children[child.ID] = childRecord{lines: child.lines}
		}
	}
	if r.records == nil {
		r.records = map[string]childRecords{}
	}
	r.records[meta.ID] = records
	if isRunning {
		tree.SourceUpdatedAt = now
	}
	sort.SliceStable(tree.Children, func(i, j int) bool {
		return kindOrder[tree.Children[i].Kind] < kindOrder[tree.Children[j].Kind]
	})
	return tree, processErr
}

var kindOrder = map[Kind]int{KindSubagent: 0, KindShell: 1, KindMonitor: 2, KindProcess: 3, KindGate: 4}

// harnessOf reads every running process and, among them, the goblin's
// harness and everything under it. isReused is set when the harness's id
// now names another program: the terminal's program ended and Windows gave
// its id to a process that is no part of this goblin.
func (r *Reader) harnessOf(goblin Goblin) (processes []Process, harness harnessProcesses, isRunning, isReused bool, err error) {
	if goblin.HarnessPID == 0 {
		return nil, harnessProcesses{}, false, false, nil
	}
	read := r.Processes
	if read == nil {
		read = Processes
	}
	if processes, err = read(); err != nil {
		return nil, harnessProcesses{}, false, false, err
	}
	harness, isRunning = readHarness(goblin.HarnessPID, processes, HarnessLaunch)
	if isRunning && !goblin.HarnessStarted.IsZero() && harness.top.Started.Sub(goblin.HarnessStarted).Abs() > startSlack {
		return processes, harnessProcesses{}, false, true, nil
	}
	return processes, harness, isRunning, false, nil
}

// conversation is the goblin's own conversation file: the session its
// terminal names, else the one the board recorded for its generation, else
// for Claude Code the one its running process records for itself, and for
// Codex its worktree's rollout written last; for Claude Code never merely the
// newest in its folder. metas is every Codex rollout read, among which a
// Codex goblin's children are.
func (r *Reader) conversation(ctx context.Context, goblin Goblin, harness Process, isRunning bool) (string, map[string]rolloutMeta) {
	session := goblin.Session
	if session == "" && r.Recorded != nil {
		session = r.Recorded(goblin.Meta)
	}
	switch strings.ToLower(goblin.Meta.Harness) {
	case "claude":
		if session == "" && isRunning {
			session = claudeProcessSession(r.Home, harness)
		}
		return SessionTranscript(r.Home, "claude", session), nil
	case "codex":
		metas := r.rollouts(ctx)
		return r.codexConversation(ctx, goblin.Meta.Worktree, session, metas), metas
	case "pi":
		return SessionTranscript(r.Home, "pi", session), nil
	}
	return "", nil
}

// Conversation is the goblin's own conversation file, chosen as Read chooses
// it, for a reader of what the goblin last said.
func (r *Reader) Conversation(ctx context.Context, goblin Goblin) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, harness, isRunning, _, _ := r.harnessOf(goblin)
	path, _ := r.conversation(ctx, goblin, harness.harness, isRunning)
	return path
}

// RunningSession is the id of the conversation the goblin's running harness
// is in, as the harness records it itself, for a switch that resumes it when
// the board recorded none: Claude Code's record of its own process, or the
// Codex rollout of the goblin's worktree written last, never a child
// agent's nor a codex exec's. It is empty while the harness does not run, and for pi, whose
// conversation no switch resumes.
func (r *Reader) RunningSession(ctx context.Context, goblin Goblin) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, harness, isRunning, _, _ := r.harnessOf(goblin)
	if !isRunning {
		return ""
	}
	switch strings.ToLower(goblin.Meta.Harness) {
	case "claude":
		return claudeProcessSession(r.Home, harness.harness)
	case "codex":
		metas := r.rollouts(ctx)
		return metas[r.codexConversation(ctx, goblin.Meta.Worktree, "", metas)].id
	}
	return ""
}

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
	for task := range r.records {
		if !live[task] {
			delete(r.records, task)
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
func (r *Reader) jobNodes(harness harnessProcesses, commands map[string]string, children []Node, now time.Time) ([]Node, error) {
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
	var nodes []Node
	for _, job := range harness.jobs {
		for _, member := range job.members {
			facts.commands[member.PID] = r.command(member)
		}
		group, label, detail := classify(job, facts)
		key := processKey{job.root.PID, job.root.Created}
		cpu := job.cpu()
		// A job is judged when first read, over its life so far, and again
		// once judgeEvery has passed since; between, it keeps its judgment.
		reading, seen := r.readings[key]
		if elapsed := now.Sub(reading.at); !seen || elapsed >= judgeEvery {
			share := 0.0
			if seen {
				share = float64(cpu-reading.cpu) / float64(elapsed)
			} else if age := now.Sub(job.root.Started); age > 0 {
				share = float64(cpu) / float64(age)
			}
			if reading.busy.IsZero() {
				reading.busy = job.root.Started
			}
			reading.isWorking = share >= busyShare
			if reading.isWorking {
				reading.busy = now
			}
			reading.at, reading.cpu = now, cpu
			r.readings[key] = reading
		}
		state := Waiting
		if reading.isWorking {
			state = Working
		}
		node := Node{ID: fmt.Sprintf("process:%d:%d", job.root.PID, job.root.Created), Kind: KindProcess, Group: group, Label: label, Detail: detail, Task: commandTask(facts.commands[job.root.PID]), State: state, Started: job.root.Started, LastActivity: reading.busy, Memory: job.memory(), SourceUpdatedAt: now, cpu: cpu, process: job.name()}
		// A machine service a goblin started works for every goblin, as the
		// no-mistakes daemon runs every gate's agents: it is shown at work,
		// and its processor use is no goblin's work.
		if job.isService(facts) {
			node.cpu, node.process, node.outsideWork = 0, "", true
		}
		if shell := shellOf(job, facts.commands[job.root.PID], commands, children); shell != "" {
			node.Parent = shell
		}
		node.lines = jobLines(job, facts.commands)
		nodes = append(nodes, node)
	}
	return nodes, err
}

// command is a process's command line, read once in its life.
func (r *Reader) command(process Process) string {
	if r.commands == nil {
		r.commands, r.readings = map[processKey]string{}, map[processKey]cpuReading{}
	}
	key := processKey{process.PID, process.Created}
	command, isRead := r.commands[key]
	if !isRead {
		commandLine := r.CommandLine
		if commandLine == nil {
			commandLine = proc.CommandLine
		}
		command, _ = commandLine(process.PID)
		r.commands[key] = command
	}
	return command
}

// ownerReads bounds how many processes of one detached tree are asked whose
// they are at one read; the tree's other processes are asked at the next.
const ownerReads = 8

// detachedJobs are the jobs of task that reach no harness: each a process
// whose parent has exited, that started after the harness, with everything
// under it, that is task's by one of its processes. A tree is the task's by the first answer its processes
// give, top down. A Git Bash tool carries no mark of its own, and a script
// can unset the mark for what it starts, so a tree whose processes have
// said nothing yet is asked again as processes join it, each process once
// in its life, since what makes it a task's never changes.
func (r *Reader) detachedJobs(task string, processes []Process, harness harnessProcesses) []job {
	if r.Owner == nil {
		return nil
	}
	if r.owners == nil {
		r.owners = map[processKey]string{}
	}
	under := make(map[int]bool, len(harness.all))
	for _, process := range harness.all {
		under[process.PID] = true
	}
	byPID := make(map[int]Process, len(processes))
	children := make(map[int][]Process)
	for _, process := range processes {
		byPID[process.PID] = process
		children[process.ParentPID] = append(children[process.ParentPID], process)
	}
	// The processes above the harness are its terminal: the host that runs
	// it works in the task's folders and has no parent once the spawn that
	// started it has returned, and it is never a job of the goblin's.
	for current := harness.top; current.PID != 0; {
		parent, isRunning := byPID[current.ParentPID]
		if !isRunning || parent.PID == current.PID || parent.Started.After(current.Started) {
			break
		}
		under[parent.PID] = true
		current = parent
	}
	var jobs []job
	for _, process := range processes {
		if process.PID == 0 || under[process.PID] {
			continue
		}
		if parent, isRunning := byPID[process.ParentPID]; isRunning && parent.PID != process.PID && !process.Started.Before(parent.Started) {
			continue
		}
		// A tree that began before the harness was not started by it,
		// whatever its newest processes do, so nothing of it is asked.
		if process.Started.Before(harness.top.Started) {
			continue
		}
		members := []Process{process}
		seen := map[int]bool{process.PID: true}
		for index := 0; index < len(members); index++ {
			for _, child := range children[members[index].PID] {
				if !seen[child.PID] && !under[child.PID] && !child.Started.Before(members[index].Started) {
					seen[child.PID] = true
					members = append(members, child)
				}
			}
		}
		owner, asks := "", 0
		for _, member := range members {
			key := processKey{member.PID, member.Created}
			answer, isAsked := r.owners[key]
			if !isAsked {
				if asks == ownerReads {
					break
				}
				asks++
				answer = r.Owner(member.PID)
				r.owners[key] = answer
			}
			if answer != "" {
				owner = answer
				break
			}
		}
		if owner == task {
			jobs = append(jobs, job{root: process, members: members})
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].root.PID < jobs[j].root.PID })
	return jobs
}

// forgetEnded drops what the reader keeps of processes no longer running,
// whichever goblin they were under: processes is every process running now.
func (r *Reader) forgetEnded(processes []Process) {
	running := make(map[processKey]bool, len(processes))
	for _, process := range processes {
		running[processKey{process.PID, process.Created}] = true
	}
	for key := range r.owners {
		if !running[key] {
			delete(r.owners, key)
		}
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

// shellOf is the background shell or monitor a job runs for, when the job's
// first process started with it and evaluates its command, or, as Claude
// Code runs a PowerShell command, writes to its output file.
func shellOf(job job, commandLine string, commands map[string]string, children []Node) string {
	command := strings.TrimSpace(shellCommand(commandLine))
	for _, child := range children {
		if child.Kind != KindShell && child.Kind != KindMonitor || child.State != Working && child.State != Silent {
			continue
		}
		if started := job.root.Started.Sub(child.Started); started <= -shellStartWindow || started >= shellStartWindow {
			continue
		}
		_, taskID, _ := strings.Cut(child.ID, ":")
		if command != "" && strings.TrimSpace(commands[child.ID]) == command || strings.Contains(commandLine, `\`+taskID+".output") || strings.Contains(commandLine, "/"+taskID+".output") {
			return child.ID
		}
	}
	return ""
}

// console is the process id of the host of the console process pid is
// attached to, 0 when it has none or it cannot be read.
func (r *Reader) console(pid int) int {
	read := r.Console
	if read == nil {
		read = proc.ConsoleHost
	}
	host, err := read(pid)
	if err != nil {
		return 0
	}
	return host
}

// drawn splits children into those the board draws, and the jobs of the
// goblin's own commands, which count in the goblin: a job no shell or
// monitor runs that is no server, test run, build or browser, and no machine
// service, such as a command the goblin runs in the foreground or a waiter
// a script left.
func drawn(children []Node) (shown, own []Node) {
	shown = []Node{}
	for _, child := range children {
		if child.Kind == KindProcess && child.Group == GroupOther && !child.outsideWork {
			own = append(own, child)
		} else {
			shown = append(shown, child)
		}
	}
	return shown, own
}

// endUnseen ends a background shell or monitor whose processes have all
// ended once it has been quiet for SilentAfter: Claude Code records no end
// of one stopped from its own screen or ended with its conversation, and
// the mark leaves time for the record of one that did end. runs names the
// children a process still runs for. Only a child whose command is known,
// and that started after its harness launched, when its processes were read
// as jobs, is judged.
func endUnseen(child *Node, commands map[string]string, runs map[string]bool, launched, now time.Time) {
	if child.Kind != KindShell && child.Kind != KindMonitor || child.State != Working && child.State != Silent {
		return
	}
	quiet := later(child.LastActivity, child.Started)
	if strings.TrimSpace(commands[child.ID]) == "" || !child.Started.After(launched) || runs[child.ID] || now.Sub(quiet) < SilentAfter {
		return
	}
	finish(child, Done, quiet)
}

// mergeShells folds each job a background shell or monitor runs into that
// child: the child carries the job's memory, what it is doing and its
// processor use, is named by its first job, its own shell, and the job is
// not listed again.
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
			if child.process == "" {
				child.process = job.process
			}
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

// gateNode reads the goblin's gate at most once per GateEvery, or again once
// it waits on something else: the run it names as its wait, or else its
// branch's newest run.
func (r *Reader) gateNode(ctx context.Context, meta state.TaskMeta, processes []Process, now time.Time) (Node, bool) {
	if r.Gate == nil {
		return Node{}, false
	}
	if r.gates == nil {
		r.gates = map[string]gateReading{}
	}
	awaited := ""
	if r.Awaited != nil {
		awaited = r.Awaited(meta)
	}
	reading, ok := r.gates[meta.ID]
	if !ok || now.Sub(reading.at) >= GateEvery || reading.awaited != awaited {
		reading = gateReading{at: now, awaited: awaited}
		// With no wait, or a wait on anything but a run, such as another
		// task, no run is named, and the goblin's gate is its branch's.
		err := pipeline.ErrNoProgress
		if awaited != "" {
			reading.progress, err = r.Gate.Run(ctx, awaited)
		}
		if branch := worktreeBranch(meta.Worktree); errors.Is(err, pipeline.ErrNoProgress) && branch != "" {
			reading.progress, err = r.Gate.Progress(ctx, meta.Project, branch)
		}
		switch {
		case errors.Is(err, pipeline.ErrNoProgress) || errors.Is(err, fs.ErrNotExist):
			// No run, or no no-mistakes state on this machine at all, is no
			// gate run rather than one that could not be read.
			reading.progress = pipeline.Progress{}
		case err != nil:
			reading.err = err
		default:
			reading.steps, reading.err = r.Gate.StepDetails(ctx, reading.progress.RunID)
		}
		if reading.err == nil && len(reading.steps) > 0 && now.Sub(r.usualAt) >= UsualEvery {
			if usual, err := r.Gate.UsualStepTimes(ctx); err != nil {
				reading.err = err
			} else {
				r.usual, r.usualAt = usual, now
			}
		}
		r.gates[meta.ID] = reading
	}
	if reading.err != nil {
		return Node{}, false
	}
	return gateNode(reading, generationStart(meta.SpawnGen), processes, r.usual)
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
