package reap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// ProcessLister reads the operating system process table. It is an interface
// because it is the one source with no test double anywhere else in the tree:
// a classification test cannot conjure real processes.
type ProcessLister interface {
	List(ctx context.Context) ([]Process, error)
}

// PaneReader is the read-only slice of the Herdr client the sweep needs: the
// structural snapshot (which panes and agents exist) plus each pane's
// operating-system identity.
type PaneReader interface {
	Snapshot(ctx context.Context) (herdr.SessionSnapshot, error)
	PaneProcessInfo(ctx context.Context, target herdr.Target) (herdr.PaneProcessInfo, error)
}

// Collector assembles one Inventory from every source.
//
// The two sources that decide what is alive - the pane table and the process
// table - are hard requirements: with either missing, every live goblin reads
// as an orphan, so the sweep refuses rather than reporting a fleet it cannot
// see. Everything else degrades and says so in the returned notes.
type Collector struct {
	Home      home.Home
	Session   string
	Panes     PaneReader
	Processes ProcessLister
	// StatusTail bounds how much of each status log is read to find the
	// latest verb, matching crewstate.Resolve's own window.
	StatusTail int
}

// Collect reads state, panes, processes and worktree directories once each.
func (c Collector) Collect(ctx context.Context) (Inventory, []string, error) {
	if c.Home.State == "" {
		return Inventory{}, nil, errors.New("reap: home state directory is required")
	}
	var notes []string
	inv := Inventory{SelfPIDs: selfAncestry()}

	scan, err := state.ScanIDs(c.Home.State)
	if err != nil {
		return Inventory{}, nil, fmt.Errorf("reap: read state directory: %w", err)
	}
	inv.OrphanStatusIDs = scan.OrphanStatusIDs
	for _, id := range scan.MetaIDs {
		if state.ValidTaskID(id) != nil {
			continue
		}
		meta, err := state.ReadTaskMeta(c.Home.State, id)
		if err != nil {
			notes = append(notes, fmt.Sprintf("state/%s.meta: UNREADABLE (%s)", id, err))
			continue
		}
		verb := c.latestVerb(id)
		inv.Tasks = append(inv.Tasks, Task{ID: id, Meta: meta, Verb: verb, Terminal: IsTerminal(verb)})
	}

	inv.Worktrees = c.worktrees(inv.Tasks, &notes)

	if c.Panes != nil {
		panes, unresolved, err := c.readPanes(ctx)
		if err != nil {
			// Every pane reads as gone when Herdr is unreadable, which would
			// classify the whole live fleet as orphaned. Refuse instead: a
			// sweep that cannot see panes has no business reporting orphans.
			return Inventory{}, notes, fmt.Errorf("reap: read Herdr panes: %w", err)
		}
		inv.Panes = panes
		inv.UnresolvedPanes = unresolved
		for _, pane := range unresolved {
			notes = append(notes, "pane "+pane+" could not report its process identity; process findings are held")
		}
	} else {
		notes = append(notes, "no pane reader configured; pane evidence is missing")
	}

	if c.Processes != nil {
		processes, err := c.Processes.List(ctx)
		if err != nil {
			return Inventory{}, notes, fmt.Errorf("reap: read process table: %w", err)
		}
		inv.Processes = processes
		inv.FleetRootPIDs = herdrRoots(processes)
	} else {
		notes = append(notes, "no process lister configured; process evidence is missing")
	}

	return inv, notes, nil
}

func (c Collector) latestVerb(id string) string {
	tail := c.StatusTail
	if tail <= 0 {
		tail = 200
	}
	lines, err := state.TailStatus(c.Home.State, id, tail)
	if err != nil {
		return ""
	}
	verb, ok := crewstate.LatestVerb(lines)
	if !ok {
		return ""
	}
	return verb
}

// readPanes pairs each pane in the structural snapshot with its
// operating-system identity and whether an agent is registered on it, and
// returns the panes whose identity could not be read. Such a pane still counts
// as a live pane, because it exists, which is what keeps its worktree off the
// list; but its shell pid is missing from the supervised set, so it is named
// separately and every process finding is held while any pane is unresolved.
func (c Collector) readPanes(ctx context.Context) (panes []Pane, unresolved []string, err error) {
	snapshot, err := c.Panes.Snapshot(ctx)
	if err != nil {
		return nil, nil, err
	}
	agents := make(map[string]bool, len(snapshot.Agents))
	for _, agent := range snapshot.Agents {
		agents[agent.PaneID] = true
	}
	panes = make([]Pane, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		entry := Pane{ID: pane.ID, HasAgent: agents[pane.ID]}
		info, err := c.Panes.PaneProcessInfo(ctx, herdr.Target{Session: c.Session, Pane: pane.ID})
		if err == nil {
			entry.ShellPID = info.ShellPID
			entry.ForegroundPID = info.ForegroundProcessGroupID
		} else {
			unresolved = append(unresolved, pane.ID)
		}
		panes = append(panes, entry)
	}
	return panes, unresolved, nil
}

// worktrees enumerates every .worktrees/ directory the fleet could own: the
// CFO home's own, each clone under projects/, and the project of every task
// that has a record. The last is what reaches a project cloned outside the
// home, which is most of them.
func (c Collector) worktrees(tasks []Task, notes *[]string) []WorktreeDir {
	roots := map[string]bool{c.Home.Root: true}
	for _, entry := range readDirNames(filepath.Join(c.Home.Root, "projects")) {
		roots[filepath.Join(c.Home.Root, "projects", entry)] = true
	}
	for _, task := range tasks {
		if task.Meta.Project != "" {
			roots[filepath.Clean(task.Meta.Project)] = true
		}
	}

	seen := make(map[string]bool)
	var found []WorktreeDir
	for _, root := range sortedKeys(roots) {
		dir := filepath.Join(root, ".worktrees")
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				*notes = append(*notes, fmt.Sprintf("%s: UNREADABLE (%s)", dir, err))
			}
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if seen[normalizePath(path)] {
				continue
			}
			seen[normalizePath(path)] = true
			found = append(found, WorktreeDir{
				Path:    path,
				Project: root,
				TaskID:  strings.TrimPrefix(entry.Name(), "gb-"),
			})
		}
	}
	return found
}

// herdrRoots finds the Herdr server processes. Every pane shell CFO ever
// created descends from one, which is what makes an unsupervised harness
// attributable to the fleet rather than to the operator's own editor.
func herdrRoots(processes []Process) []int {
	var roots []int
	for _, process := range processes {
		if executableName(process.Name) == "herdr" {
			roots = append(roots, process.PID)
		}
	}
	return roots
}

// selfAncestry is this process and its parents. The sweep runs inside the
// CFO's own Claude session (directly, or hosted by the watcher), so its own
// harness would otherwise look exactly like an orphan.
func selfAncestry() []int {
	entries, err := proc.Ancestry(proc.Self(), 12)
	if err != nil {
		return []int{proc.Self()}
	}
	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		pids = append(pids, entry.PID)
	}
	return pids
}

func readDirNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// CIMProcesses reads the Windows process table through one CIM query. The
// Toolhelp32 snapshot internal/proc already takes carries no command line, and
// the command line is what tells a goblin's dev server apart from the
// operator's, so this pays one subprocess per sweep to get it.
type CIMProcesses struct {
	Commands execx.Runner
}

// utf8OutputPrelude forces the child's console output encoding to UTF-8.
// Go spawns powershell with a pipe rather than a console, and PowerShell then
// encodes stdout with the OEM code page - IBM437 on this machine. A command
// line holding any character outside that page comes back mangled, and some
// mangle into raw control bytes: U+00A7 SECTION SIGN encodes to byte 0x15,
// which encoding/json rejects with "invalid character '' in string
// literal", failing the entire process listing and with it the whole orphan
// sweep. Escaping the byte instead would decode, but would report a command
// line that is not the process's real one, and reap classifies processes by
// their command line to decide what may be killed.
const utf8OutputPrelude = `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; `

const cimProcessScript = utf8OutputPrelude + `Get-CimInstance Win32_Process | ForEach-Object { [pscustomobject]@{ pid = [int]$_.ProcessId; ppid = [int]$_.ParentProcessId; name = $_.Name; cmd = $_.CommandLine; start = $(if ($_.CreationDate) { $_.CreationDate.ToUniversalTime().ToString('o') } else { '' }) } } | ConvertTo-Json -Compress -Depth 3`

// List runs the CIM query and decodes it. ConvertTo-Json emits a bare object
// rather than an array when the pipeline yields exactly one item, so a single
// row is wrapped before decoding; a real process table never has one row, but
// a wrapper that only works on the common case is not worth the bug report.
func (c CIMProcesses) List(ctx context.Context) ([]Process, error) {
	if c.Commands == nil {
		return nil, errors.New("reap: command runner is required")
	}
	result, err := c.Commands.Run(ctx, execx.Request{
		Name: "powershell",
		Args: []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", cimProcessScript},
	})
	if err != nil {
		return nil, fmt.Errorf("reap: list processes: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("reap: list processes exited with code %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return decodeProcesses(result.Stdout)
}

func decodeProcesses(stdout []byte) ([]Process, error) {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return nil, errors.New("reap: process listing returned no output")
	}
	if strings.HasPrefix(trimmed, "{") {
		trimmed = "[" + trimmed + "]"
	}
	var rows []struct {
		PID   int    `json:"pid"`
		PPID  int    `json:"ppid"`
		Name  string `json:"name"`
		Cmd   string `json:"cmd"`
		Start string `json:"start"`
	}
	if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
		return nil, fmt.Errorf("reap: decode process listing: %w", err)
	}
	processes := make([]Process, 0, len(rows))
	for _, row := range rows {
		process := Process{PID: row.PID, ParentPID: row.PPID, Name: row.Name, CommandLine: row.Cmd}
		if row.Start != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, row.Start); err == nil {
				process.Start = parsed.UTC()
			}
		}
		processes = append(processes, process)
	}
	return processes, nil
}
