package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// ProgressSample is what a goblin's own work looks like from outside its
// pane. agent_status says whether the harness is in a turn; this says whether
// anything underneath it is moving.
type ProgressSample struct {
	// TranscriptAt is when the harness last wrote its session transcript,
	// zero when no transcript was found.
	TranscriptAt time.Time
	// Jobs names each process the harness started after launching that is
	// still running - a tool command, a background job, a monitor loop - as
	// "name (pid N)".
	Jobs []string
	// JobCPU is the processor time those processes and everything under them
	// have used.
	JobCPU time.Duration
}

// ProgressProber reads a goblin's progress evidence. The monitor consults it
// only for a goblin a stale wake is otherwise due for, so its cost is paid
// rarely and never on a healthy, short turn. An error means some evidence
// could not be read; the sample still carries whatever was.
type ProgressProber interface {
	InspectProgress(ctx context.Context, meta state.TaskMeta, sample EndpointSample) (ProgressSample, error)
}

// PaneProcesses reads a pane's operating-system identity.
type PaneProcesses interface {
	PaneProcessInfo(ctx context.Context, target herdr.Target) (herdr.PaneProcessInfo, error)
}

// HostProgress reads progress evidence on this machine: the harness's
// transcript under the user's home, and the processes under the harness.
type HostProgress struct {
	Panes PaneProcesses
	// StateDir is where native terminals' hosts record the program each
	// runs, the harness of a native task.
	StateDir string
	// Home is the user's home directory, where every harness keeps its
	// transcripts. Empty skips the transcript.
	Home string
}

// harnessLaunch is how long after a harness starts the processes it starts
// still belong to launching it: its MCP servers, or the real binary a shim
// runs. A process it starts later is work it was asked to do.
const harnessLaunch = 2 * time.Minute

// InspectProgress reads the transcript and the harness's own processes. The
// harness of a native task is the program its terminal runs; otherwise it is
// whatever Herdr reports in the pane's foreground, and a pane back at its
// shell has no harness and so no processes of its own.
func (h HostProgress) InspectProgress(ctx context.Context, meta state.TaskMeta, sample EndpointSample) (ProgressSample, error) {
	progress := ProgressSample{TranscriptAt: transcriptAt(h.Home, sample.Harness, sample.Session)}
	if meta.Backend == "native" && strings.EqualFold(sample.Harness, "codex") && sample.Session == "" {
		progress.TranscriptAt = nativeCodexTranscriptAt(ctx, h.Home, meta.Worktree)
	}
	harnessPID, err := h.harnessPID(ctx, meta, sample)
	if err != nil || harnessPID == 0 {
		return progress, err
	}
	processes, err := proc.Processes()
	if err != nil {
		return progress, err
	}
	progress.Jobs, progress.JobCPU = harnessJobs(harnessPID, processes, harnessLaunch, proc.StartTime, proc.CPUTime)
	return progress, nil
}

// harnessPID returns the process id of the task's harness, and 0 for a pane
// back at its shell.
func (h HostProgress) harnessPID(ctx context.Context, meta state.TaskMeta, sample EndpointSample) (int, error) {
	if meta.Backend == "native" {
		record, err := host.ReadRecord(h.StateDir, meta.ID)
		if err != nil {
			return 0, err
		}
		return record.ChildPID, nil
	}
	info, err := h.Panes.PaneProcessInfo(ctx, sample.Endpoint.Target)
	if err != nil {
		return 0, err
	}
	if info.ForegroundProcessGroupID == info.ShellPID {
		return 0, nil
	}
	return info.ForegroundProcessGroupID, nil
}

// launchShims are the programs a harness is commonly started through. One
// that launched a single process as it started is walked through to the
// harness it runs, so a harness installed behind node or a .cmd wrapper is
// read where it actually runs its tools.
var launchShims = map[string]bool{"cmd": true, "node": true}

// harnessJobs finds the processes a harness started after launching, and the
// processor time they and their descendants have used. A process started
// within launch of the harness is part of the harness - its MCP
// servers are the common case, and they idle for its whole life - so it is
// never counted as work. A child created before its parent is a reused
// process id, not a child, and is skipped.
func harnessJobs(root int, processes []proc.Entry, launch time.Duration, start func(int) (time.Time, bool), cpu func(int) (time.Duration, bool)) ([]string, time.Duration) {
	byPID := make(map[int]proc.Entry, len(processes))
	children := make(map[int][]proc.Entry)
	for _, process := range processes {
		byPID[process.PID] = process
		children[process.ParentPID] = append(children[process.ParentPID], process)
	}
	harness, found := byPID[root]
	if !found {
		return nil, 0
	}
	launched, ok := start(root)
	if !ok {
		return nil, 0
	}
	launchEnds := launched.Add(launch)
	childrenOf := func(pid int, after time.Time) []proc.Entry {
		var kept []proc.Entry
		for _, child := range children[pid] {
			if child.PID == pid {
				continue
			}
			if started, ok := start(child.PID); ok && !started.Before(after) {
				child.Start = started
				kept = append(kept, child)
			}
		}
		return kept
	}

	for launchShims[executableName(harness.ExeBase)] {
		kids := childrenOf(harness.PID, launched)
		if len(kids) != 1 || kids[0].Start.After(launchEnds) {
			break
		}
		harness = kids[0]
	}

	var jobs []proc.Entry
	for _, child := range childrenOf(harness.PID, launched) {
		if child.Start.After(launchEnds) {
			jobs = append(jobs, child)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].PID < jobs[j].PID })

	var names []string
	var used time.Duration
	seen := map[int]bool{harness.PID: true}
	var walk func(process proc.Entry)
	walk = func(process proc.Entry) {
		if seen[process.PID] {
			return
		}
		seen[process.PID] = true
		if spent, ok := cpu(process.PID); ok {
			used += spent
		}
		for _, child := range childrenOf(process.PID, process.Start) {
			walk(child)
		}
	}
	for _, job := range jobs {
		names = append(names, fmt.Sprintf("%s (pid %d)", job.ExeBase, job.PID))
		walk(job)
	}
	return names, used
}

// transcriptPatterns are where each harness writes its session transcript,
// as globs under the user's home with {session} standing for the session id.
// A Claude subagent writes its own transcript beside its parent's, and a
// parent waiting on one writes nothing, so those count too.
var transcriptPatterns = map[string][]string{
	"claude": {
		filepath.Join(".claude", "projects", "*", "{session}.jsonl"),
		filepath.Join(".claude", "projects", "*", "{session}", "subagents", "*.jsonl"),
	},
	"codex": {filepath.Join(".codex", "sessions", "*", "*", "*", "rollout-*-{session}.jsonl")},
	"pi":    {filepath.Join(".pi", "agent", "sessions", "*", "*_{session}.jsonl")},
}

// sessionID is the shape of a harness session id. Anything else could reach
// outside the transcript directories once substituted into a glob.
var sessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// transcriptAt returns when the harness last wrote its session transcript,
// or zero when the harness keeps none this reads or none was found. That is
// the later of the file's write time and its last complete entry's own
// timestamp: Codex keeps its rollout open and appends to it, and on
// 2026-09-29 a rollout's write time stayed ninety seconds after its creation
// for hours while its entries ran on, read alike with os.Stat and from an
// open handle.
func transcriptAt(home, harness, session string) time.Time {
	var latest time.Time
	if home == "" || !sessionID.MatchString(session) {
		return latest
	}
	for _, pattern := range transcriptPatterns[strings.ToLower(harness)] {
		matches, err := filepath.Glob(filepath.Join(home, strings.ReplaceAll(pattern, "{session}", session)))
		if err != nil {
			continue
		}
		for _, match := range matches {
			file, err := fsx.Open(match)
			if err != nil {
				continue
			}
			written := transcriptFileAt(file)
			file.Close()
			if written.After(latest) {
				latest = written
			}
		}
	}
	return latest
}

// Native Codex screens carry no session ID. The rollout's own metadata binds
// it to the task's worktree; directory timestamps cannot establish progress
// while Codex keeps the writer open.
func nativeCodexTranscriptAt(ctx context.Context, home, worktree string) time.Time {
	var latest time.Time
	if home == "" || !filepath.IsAbs(worktree) {
		return latest
	}
	pattern := strings.ReplaceAll(transcriptPatterns["codex"][0], "{session}", "*")
	matches, _ := filepath.Glob(filepath.Join(home, pattern))
	for _, match := range matches {
		if ctx.Err() != nil {
			break
		}
		file, err := fsx.Open(match)
		if err != nil {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.NewDecoder(io.LimitReader(file, transcriptEntryReach)).Decode(&entry) == nil && entry.Type == "session_meta" && filepath.IsAbs(entry.Payload.Cwd) && fsx.SamePath(entry.Payload.Cwd, worktree) {
			if written := transcriptFileAt(file); written.After(latest) {
				latest = written
			}
		}
		file.Close()
	}
	return latest
}

// transcriptEntryReach bounds how much of a transcript's end is read for its
// last entry, since one entry holding a large tool result can run to
// megabytes.
const transcriptEntryReach = 4 << 20

// transcriptFileAt reads the write time and length through the open handle,
// and uses the last complete entry's timestamp when it is newer. An entry
// still being written does not parse and is passed over.
func transcriptFileAt(file *os.File) time.Time {
	info, err := file.Stat()
	if err != nil {
		return time.Time{}
	}
	start := max(0, info.Size()-transcriptEntryReach)
	tail := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(tail, start); err != nil && !errors.Is(err, io.EOF) {
		return info.ModTime()
	}
	lines := bytes.Split(tail, []byte("\n"))
	if start > 0 {
		// The first piece may begin part way through an entry.
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		var entry struct {
			Timestamp time.Time `json:"timestamp"`
		}
		if json.Unmarshal(lines[i], &entry) == nil && !entry.Timestamp.IsZero() {
			if entry.Timestamp.After(info.ModTime()) {
				return entry.Timestamp
			}
			break
		}
	}
	return info.ModTime()
}
