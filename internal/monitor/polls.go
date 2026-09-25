package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Poll is a lavish-axi poll a goblin runs itself. The poll takes the
// Overlord's feedback on the page as it arrives, so his answer there reaches
// that goblin and nobody else: no status line, no Waiting on you card and no
// wake. A goblin waiting on a page registers the wait with cfo notify
// --waiting-on overlord --lavish instead, and the supervisor polls it for the
// CFO.
type Poll struct {
	PID   int       `json:"pid"`
	Start time.Time `json:"start"`
	Task  string    `json:"task"`
	Page  string    `json:"page,omitempty"`
}

// PollProber lists the lavish-axi polls goblins are running themselves.
type PollProber interface {
	Polls(ctx context.Context) ([]Poll, error)
}

// ProcessPolls reads those polls from this machine's process table.
type ProcessPolls struct{}

// pollAncestry bounds the walk from a poll up to the goblin that started it:
// the poll, the shell it runs in, the harness and the pane's shell, with room
// for a shim between them.
const pollAncestry = 8

// Polls lists every lavish-axi poll that runs in a goblin's worktree, or
// under a process that does. Only node and lavish-axi itself are read, so the
// check opens a handful of processes rather than every one on the machine.
func (ProcessPolls) Polls(context.Context) ([]Poll, error) {
	processes, err := proc.Processes()
	if err != nil {
		return nil, err
	}
	var polls []Poll
	for _, process := range processes {
		if name := executableName(process.ExeBase); name != "node" && name != "lavish-axi" {
			continue
		}
		args, err := proc.Arguments(process.PID)
		if err != nil {
			continue
		}
		page, ok := pollPage(args)
		if !ok {
			continue
		}
		chain, err := proc.Ancestry(process.PID, pollAncestry)
		if err != nil || len(chain) == 0 || chain[0].PID != process.PID {
			continue
		}
		task, ok := goblinOf(chain)
		if !ok {
			continue
		}
		if page != "" && !filepath.IsAbs(page) {
			if dir, err := proc.WorkingDirectory(process.PID); err == nil {
				page = filepath.Join(dir, page)
			}
		}
		polls = append(polls, Poll{PID: process.PID, Start: chain[0].Start, Task: task, Page: page})
	}
	return polls, nil
}

// goblinOf places a poll with the first process in its chain, the poll itself
// first, that runs in a goblin's worktree. A poll cfo started is the
// supervisor polling a page wait for the CFO, which is exactly what a goblin
// should have asked for, so it is never a goblin's poll.
func goblinOf(chain []proc.Entry) (string, bool) {
	if startedByCFO(chain) {
		return "", false
	}
	for _, entry := range chain {
		dir, err := proc.WorkingDirectory(entry.PID)
		if err != nil {
			continue
		}
		if task := worktreeTask(dir); task != "" {
			return task, true
		}
	}
	return "", false
}

// startedByCFO reports whether the nearest program above a poll, past the
// shells and node shims that run it, is cfo. Only that one decides: a
// launcher further up, above Herdr, is above every goblin too.
func startedByCFO(chain []proc.Entry) bool {
	for _, entry := range chain[1:] {
		switch executableName(entry.ExeBase) {
		case "cmd", "powershell", "pwsh", "bash", "sh", "node":
			continue
		case "cfo", "goblins":
			return true
		}
		return false
	}
	return false
}

// worktreeTask names the goblin whose worktree holds dir, from the
// <project>\.worktrees\gb-<task> layout spawn creates, or returns "" when dir
// is in no goblin's worktree.
func worktreeTask(dir string) string {
	parts := strings.FieldsFunc(filepath.Clean(dir), isPathSeparator)
	task := ""
	for index := 0; index+1 < len(parts); index++ {
		name := parts[index+1]
		if !strings.EqualFold(parts[index], ".worktrees") || len(name) <= len("gb-") || !strings.EqualFold(name[:len("gb-")], "gb-") {
			continue
		}
		if id := name[len("gb-"):]; state.ValidTaskID(id) == nil {
			task = id
		}
	}
	return task
}

// pollPage reports whether args run lavish-axi's poll, and the page it waits
// on. lavish-axi is the program itself or, under node, the script node runs
// from its package. The first operand after it is the subcommand, and the page
// is the first operand after poll that names an HTML file, else the first.
func pollPage(args []string) (string, bool) {
	for index, arg := range args {
		if !namesLavish(arg) {
			continue
		}
		var operands []string
		for _, operand := range args[index+1:] {
			if !strings.HasPrefix(operand, "-") {
				operands = append(operands, operand)
			}
		}
		if len(operands) == 0 || operands[0] != "poll" {
			return "", false
		}
		for _, operand := range operands[1:] {
			if extension := strings.ToLower(filepath.Ext(operand)); extension == ".html" || extension == ".htm" {
				return operand, true
			}
		}
		if len(operands) > 1 {
			return operands[1], true
		}
		return "", true
	}
	return "", false
}

// namesLavish reports whether an argument is lavish-axi: the program under any
// of its names, or a path through its package.
func namesLavish(arg string) bool {
	for _, part := range strings.FieldsFunc(arg, isPathSeparator) {
		if strings.EqualFold(strings.TrimSuffix(part, filepath.Ext(part)), "lavish-axi") {
			return true
		}
	}
	return false
}

func isPathSeparator(r rune) bool {
	return r == '\\' || r == '/'
}

func executableName(exe string) string {
	return strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe")
}

// flagPrivatePoll raises a review event for the oldest private poll not
// flagged before, and forgets the flags of polls that have ended. The event
// waits in the heartbeat's own slot until it is published, as every event
// does, so a crash cannot lose it, and a poll is flagged once for as long as
// it runs. Only a goblin this home knows is flagged: live with a task record,
// or retired with only its status log left.
func (s Service) flagPrivatePoll(ctx context.Context, heartbeat *Heartbeat, result *ScanResult, entries []os.DirEntry) {
	if s.Polls == nil {
		return
	}
	polls, err := s.Polls.Polls(ctx)
	if err != nil {
		return
	}
	running := make(map[string]bool, len(polls))
	for _, poll := range polls {
		running[poll.key()] = true
	}
	flagged := make(map[string]bool, len(heartbeat.FlaggedPolls))
	var kept []Poll
	for _, poll := range heartbeat.FlaggedPolls {
		if running[poll.key()] {
			kept = append(kept, poll)
			flagged[poll.key()] = true
		}
	}
	heartbeat.FlaggedPolls = kept
	if result.Event != nil {
		return
	}
	goblins := knownGoblins(entries)
	sort.Slice(polls, func(i, j int) bool {
		if !polls[i].Start.Equal(polls[j].Start) {
			return polls[i].Start.Before(polls[j].Start)
		}
		return polls[i].PID < polls[j].PID
	})
	for _, poll := range polls {
		goblin, known := goblins[strings.ToLower(poll.Task)]
		if flagged[poll.key()] || !known {
			continue
		}
		event := Event{Source: TaskEvent, TaskID: goblin.id, Kind: "review", Key: goblin.id, Detail: privatePollDetail(poll, goblin.id, goblin.live)}
		heartbeat.PendingEvent = &event
		heartbeat.FlaggedPolls = append(heartbeat.FlaggedPolls, poll)
		result.Event = cloneEvent(&event)
		return
	}
}

func (p Poll) key() string {
	return fmt.Sprintf("%d@%d", p.PID, p.Start.UnixNano())
}

type goblinRecord struct {
	id   string
	live bool
}

// knownGoblins reads every goblin this home knows from its state listing:
// live with a task record, retired with only its status log left, which cfo
// cleanup keeps when it retires the record.
func knownGoblins(entries []os.DirEntry) map[string]goblinRecord {
	goblins := make(map[string]goblinRecord)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		extension := filepath.Ext(name)
		id := strings.TrimSuffix(name, extension)
		switch {
		case strings.EqualFold(extension, ".meta"):
			goblins[strings.ToLower(id)] = goblinRecord{id: id, live: true}
		case strings.EqualFold(extension, ".status"):
			if _, seen := goblins[strings.ToLower(id)]; !seen {
				goblins[strings.ToLower(id)] = goblinRecord{id: id}
			}
		}
	}
	return goblins
}

// privatePollDetail is what the CFO reads when a poll is flagged. A live
// goblin is told how to wait instead; a retired goblin's poll holds answers
// nobody will read, so the CFO collects them itself.
func privatePollDetail(poll Poll, task string, live bool) string {
	page := poll.Page
	if page == "" {
		page = "its page"
	}
	if live {
		return fmt.Sprintf("goblin %s is polling %s itself with lavish-axi (pid %d), so the Overlord's answer there reaches only that goblin; tell it to stop the poll and wait with cfo notify %s --waiting-on overlord <why> --lavish <page>", task, page, poll.PID, task)
	}
	return fmt.Sprintf("a lavish-axi poll (pid %d) still waits on %s for goblin %s, which is retired, so the Overlord's answer there reaches nobody; read the page's open questions, put them to him, and stop the poll", poll.PID, page, task)
}
