package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// AFK mode is the Overlord's switch for running the fleet while he is away.
// The supervisor is the only writer of it: it turns the switch for a process
// it has proven is his own terminal, logs a decision for a process it has
// proven is the registered CFO, records each item that waits on him as held,
// and hands the board nothing to announce while the switch is on.
//
// The proof names its adversary: an agent that follows its contract and tries
// the command, the pipe or a wrapper around either. Every process of this
// Windows user can still write state/afk.json itself, which is the same
// boundary the board's other items have (docs/native-board.md).

// gateAgentVariable is set by no-mistakes on every agent it starts for a gate
// step.
const gateAgentVariable = "NO_MISTAKES_GATE"

// agentHarnesses are the programs an agent runs as, by executable name: each
// harness the fleet starts, and node, which runs the ones installed as npm
// scripts.
var agentHarnesses = []string{string(harness.Claude), string(harness.Codex), string(harness.Pi), string(harness.Kimi), "node"}

// agentVariables are environment variables a harness sets for every command
// it runs: Claude Code's own, and the one agents share to say a command is
// theirs.
var agentVariables = []string{"CLAUDECODE", "AI_AGENT"}

// SwitchAFK asks the supervisor to turn AFK mode on or off for the process
// that calls it. The supervisor makes the switch only once it has proven that
// process runs in a terminal of the Overlord's own.
func SwitchAFK(h home.Home, on bool) error {
	kind := "afk-off"
	if on {
		kind = "afk-on"
	}
	return sendPipeRequest(h.State, runPipeRequest{Kind: kind})
}

// LogAFKDecision records a decision the registered CFO made under AFK mode's
// authority, with its evidence. The supervisor writes the line only once it
// has proven the calling process runs under that CFO.
func LogAFKDecision(h home.Home, entry afk.Entry) error {
	return sendPipeRequest(h.State, runPipeRequest{Kind: "afk-log", AFK: &entry})
}

// overlordsTerminal proves the process pid, which was running at connected,
// runs in a terminal of the Overlord's own, and says which. Whatever marks the
// process as an agent's refuses it: a goblin's or a gate agent's environment,
// the registered CFO among its ancestors, a terminal the fleet runs an agent
// in, or an agent harness above it. A process it cannot read is refused too,
// never taken for his.
func (s *Service) overlordsTerminal(pid int, connected time.Time) (string, error) {
	const his = ": only he turns it on or off, from a terminal of his own"
	inspect := s.inspectCaller
	if inspect == nil {
		inspect = func(pid int) ([]proc.Entry, []string, error) {
			ancestry, err := proc.Ancestry(pid, 32)
			if err != nil {
				return nil, nil, err
			}
			env, err := proc.Environment(pid)
			return ancestry, env, err
		}
	}
	ancestry, env, err := inspect(pid)
	// A process that started after the request took the PID of the one that
	// sent it, and proves nothing.
	if err != nil || len(ancestry) == 0 || ancestry[0].Start.After(connected) {
		return "", errors.New("AFK mode is the Supreme Overlord's switch, and the supervisor could not read the process that asked for it, so nothing says it is his" + his)
	}
	refuse := func(where string) (string, error) {
		return "", errors.New("AFK mode is the Supreme Overlord's switch, and this command runs " + where + his)
	}
	switch {
	case environmentValue(env, harness.RoleVariable) == harness.RoleGoblin:
		return refuse("in a goblin's terminal")
	case environmentValue(env, gateAgentVariable) != "":
		return refuse("as a gate agent")
	}
	if primary, _, err := readPrimary(s.Store.Home.State); err == nil && descendsFrom(ancestry, primary.Process) {
		return refuse(fmt.Sprintf("under the registered CFO (%s pid %d)", primary.Agent, primary.Process.PID))
	}
	if id := environmentValue(env, host.IDVariable); id != "" {
		return refuse("in native terminal " + id + ", which runs the CFO or a goblin")
	}
	if environmentValue(env, "HERDR_PANE_ID") != "" {
		return refuse("in a Herdr pane, where the CFO or a goblin runs")
	}
	for _, entry := range ancestry {
		if slices.Contains(agentHarnesses, strings.TrimSuffix(strings.ToLower(entry.ExeBase), ".exe")) {
			return refuse(fmt.Sprintf("under an agent harness (%s pid %d)", entry.ExeBase, entry.PID))
		}
	}
	// A command whose parents are cut off, as Git Bash's timeout leaves one,
	// has no harness left among its ancestors; what the harness put in its
	// environment is still there.
	for _, name := range agentVariables {
		if environmentValue(env, name) != "" {
			return refuse("under an agent harness (its environment carries " + name + ")")
		}
	}
	// The command itself is the first entry; the shell it was typed in is
	// the next.
	shell := ancestry[min(1, len(ancestry)-1)]
	return fmt.Sprintf("his own terminal (%s pid %d)", shell.ExeBase, shell.PID), nil
}

// switchAFK turns AFK mode on or off for the process at the other end of the
// pipe, once that process is proven to be the Overlord's own terminal. Turning
// it on reads the allowance and tells the CFO through its wake queue; turning
// it off keeps the report of the stretch first, so a stretch never ends
// without one, and tells the CFO to write it into its terminal.
func (s *Service) switchAFK(ctx context.Context, pid int, connected time.Time, on bool) error {
	from, err := s.overlordsTerminal(pid, connected)
	if err != nil {
		return err
	}
	s.afkChange.Lock()
	defer s.afkChange.Unlock()
	stateDir := s.Store.Home.State
	current, err := afk.Read(stateDir)
	if err != nil {
		// His off always works: it puts a switch that cannot be read back to
		// off. His on does not, since it would guess at what the switch held.
		if on {
			return err
		}
		if err := afk.Reset(stateDir, from, err, time.Now()); err != nil {
			return err
		}
		return s.afkNotice("the Overlord reset AFK mode to off from " + from + ": its switch could not be read, so there is no report of the stretch it may have held, and state/afk.audit keeps what was logged. He is back and decides again, so the standing rules apply.")
	}
	if on == current.On {
		if on {
			return nil
		}
		return afk.ErrNotOn
	}
	allowance, unread := []afk.Allowance(nil), "this supervisor reads no allowance"
	if s.Options.Allowance != nil {
		allowance, unread = s.Options.Allowance(ctx)
	}
	now := time.Now().UTC()
	if on {
		if _, _, err := afk.TurnOn(stateDir, from, allowance, now); err != nil {
			return err
		}
		return s.afkNotice("the Overlord turned AFK mode on from " + from + ": he is away until he turns it off, and nothing prompts him meanwhile. Decide what its authority covers yourself and log each decision, and leave what stays his alone held for him. Its terms follow this queue.")
	}
	current.Ended, current.EndedFrom = now, from
	if err := afk.SaveReport(stateDir, s.afkReport(current, allowance, unread)); err != nil {
		return fmt.Errorf("AFK mode stays on: the report of its stretch could not be kept: %w", err)
	}
	if _, err := afk.TurnOff(stateDir, from, now); err != nil {
		return err
	}
	return s.afkNotice("the Overlord turned AFK mode off from " + from + ": he is back and decides again, so the standing rules apply. Write the report of the stretch into your terminal: cfo afk report")
}

// afkNotice tells the CFO of the Overlord's switch through its wake queue,
// asking it nothing.
func (s *Service) afkNotice(detail string) error {
	if _, err := wake.Append(s.Store.Home.State, "review", "afk", detail); err != nil {
		return fmt.Errorf("the switch was made, but the CFO's wake queue could not be told: %w", err)
	}
	if _, err := wake.PublishEpisode(s.Store.Home.State); err != nil {
		return fmt.Errorf("the switch was made, but the CFO's wake queue could not be told: %w", err)
	}
	return nil
}

// logAFKDecision writes a decision the registered CFO sent over the pipe.
func (s *Service) logAFKDecision(entry afk.Entry) error {
	s.afkChange.Lock()
	defer s.afkChange.Unlock()
	_, err := afk.Log(s.Store.Home.State, entry, time.Now())
	return err
}

// afkOn reports whether AFK mode is on. A switch that cannot be read is not
// taken for on: the recovery cycle reports it, and nothing that needs the
// Overlord is hidden by a guess.
func (s *Service) afkOn() bool {
	switched, err := afk.Read(s.Store.Home.State)
	return err == nil && switched.On
}

// heldItem is a Command Center item that waits on the Overlord.
type heldItem struct {
	kind, id, task, what string
}

func (item heldItem) key() string { return item.kind + ":" + item.id }

// waitingOnOverlord are the items that wait on the Overlord: what the board
// would announce to him, and a credential request, which only he can fill.
func waitingOnOverlord(d Database) []heldItem {
	var items []heldItem
	for _, q := range d.Questions {
		if q.Status == "pending" {
			items = append(items, heldItem{"question", q.ID, q.Task, q.Text})
		}
	}
	for _, r := range d.Reviews {
		if r.State == "open" {
			items = append(items, heldItem{"review", r.ID, r.Task, r.Title})
		}
	}
	for _, r := range d.Runs {
		if r.State == "ready" {
			items = append(items, heldItem{"run", r.ID, "", r.Title})
		}
	}
	for _, c := range d.Credentials {
		if c.State == "open" {
			items = append(items, heldItem{"credential", c.ID, c.Task, "Credentials for " + c.Project + " (" + strings.Join(c.Names, ", ") + "): " + c.Why})
		}
	}
	return items
}

// announceKeys are the names the board asks to announce an item under: its
// alert, and for a question the Command Center opening on it.
// TestTheBoardStillNamesItsItemsAsTheSupervisorExpects pins them to the
// board's own.
func announceKeys(kind, id string) []string {
	switch kind {
	case "question":
		return []string{"alert:question:" + id, "open:question:" + id}
	case "review", "run":
		return []string{"alert:" + kind + ":" + id}
	}
	return nil
}

// holdForOverlord records each item that waits on the Overlord while AFK mode
// is on as held for him, once in a stretch, and marks it announced, so the
// board never alerts for it, while he is away or once he is back: it is in
// the report instead.
func (s *Service) holdForOverlord(now time.Time) error {
	s.afkChange.Lock()
	defer s.afkChange.Unlock()
	stateDir := s.Store.Home.State
	switched, err := afk.Read(stateDir)
	if err != nil || !switched.On {
		return err
	}
	if s.heldSession != switched.Session {
		entries, _, err := afk.Entries(stateDir, switched.Session)
		if err != nil {
			return err
		}
		s.held, s.heldSession = map[string]bool{}, switched.Session
		for _, entry := range entries {
			if entry.Kind == afk.KindHeld {
				s.held[entry.Item] = true
			}
		}
	}
	var keys []string
	for _, item := range waitingOnOverlord(s.Store.Snapshot()) {
		keys = append(keys, announceKeys(item.kind, item.id)...)
		if s.held[item.key()] {
			continue
		}
		if err := afk.Hold(stateDir, afk.Entry{Item: item.key(), Task: item.task, What: bounded(item.what, 2000)}, now); err != nil {
			return err
		}
		s.held[item.key()] = true
	}
	// Every waiting item's keys, not only the new ones: a supervisor that
	// stopped between holding an item and marking it marks it now.
	if len(keys) == 0 {
		return nil
	}
	_, err = s.Store.claimAnnounced(keys, nil, now)
	return err
}

// afkReport is the report of the stretch ended holds: the log's decisions,
// what each goblin reported done, each held item with what became of it, and
// the allowance read when it turned on beside after, read now.
func (s *Service) afkReport(ended afk.State, after []afk.Allowance, unread string) afk.Report {
	stateDir := s.Store.Home.State
	report := afk.Report{Session: ended.Session, Since: ended.Since, Ended: ended.Ended, From: ended.From, EndedFrom: ended.EndedFrom, Before: ended.Allowance, After: after}
	entries, unreadable, err := afk.Entries(stateDir, ended.Session)
	switch {
	case err != nil:
		report.Notes = append(report.Notes, "the log of decisions could not be read: "+err.Error())
	case unreadable > 0:
		report.Notes = append(report.Notes, fmt.Sprintf("%d line(s) of the log could not be read, in this stretch or another", unreadable))
	}
	if len(ended.Allowance) == 0 {
		report.Notes = append(report.Notes, "the allowance was not read when AFK mode turned on")
	}
	if len(after) == 0 {
		report.Notes = append(report.Notes, "the allowance was not read when AFK mode turned off: "+unread)
	}
	report.Decisions = afk.Decisions(entries)
	finished, err := doneBetween(stateDir, ended.Since, ended.Ended)
	if err != nil {
		report.Notes = append(report.Notes, "what the goblins finished could not be read in full: "+err.Error())
	}
	report.Finished = finished
	d := s.Store.Snapshot()
	for _, entry := range entries {
		if entry.Kind != afk.KindHeld {
			continue
		}
		waiting, now, decided := heldNow(d, entry.Item)
		// What the CFO answered is a decision in the log, not held.
		if decided {
			continue
		}
		held := afk.Held{Item: entry.Item, Task: entry.Task, What: entry.What, At: entry.At, Waiting: waiting, Now: now}
		if entry.Task != "" {
			// A status line is stamped to the second.
			lines, _ := state.TailStatus(stateDir, entry.Task, 50)
			if at, said := latestReport(lines, time.Time{}); !at.Before(entry.At.Truncate(time.Second)) {
				held.Meanwhile = bounded(said, 500)
			}
		}
		report.Held = append(report.Held, held)
	}
	return report
}

// heldNow is what became of a held item: whether it still waits on the
// Overlord, in what words, and whether the CFO decided it.
func heldNow(d Database, item string) (waiting bool, now string, decided bool) {
	kind, id, _ := strings.Cut(item, ":")
	closed := func(how, reason string) (bool, string, bool) {
		if reason != "" {
			how += ": " + reason
		}
		return false, bounded(how, 500), false
	}
	switch kind {
	case "question":
		i := slices.IndexFunc(d.Questions, func(q Question) bool { return q.ID == id })
		if i < 0 {
			break
		}
		switch q := d.Questions[i]; {
		case q.Status == "pending":
			return true, "still waiting on you", false
		case q.AnsweredBy == "cfo":
			return false, "", true
		case q.AnsweredBy == "overlord":
			return closed("you answered it", q.Answer)
		default:
			return closed(q.Status, q.Message)
		}
	case "review":
		i := slices.IndexFunc(d.Reviews, func(r Review) bool { return r.ID == id })
		if i < 0 {
			break
		}
		if r := d.Reviews[i]; r.State != "open" {
			return closed(r.State, r.Reason)
		}
		return true, "still waiting on you", false
	case "run":
		i := slices.IndexFunc(d.Runs, func(r Run) bool { return r.ID == id })
		if i < 0 {
			break
		}
		if r := d.Runs[i]; r.State != "ready" {
			return closed(r.State, r.Reason)
		}
		return true, "still waiting for you to run it", false
	case "credential":
		i := slices.IndexFunc(d.Credentials, func(c CredentialRequest) bool { return c.ID == id })
		if i < 0 {
			break
		}
		if c := d.Credentials[i]; c.State != "open" {
			return closed(c.State, c.Reason)
		}
		return true, "still waiting for the values", false
	}
	return false, "no longer listed on the board", false
}

// doneBetween are the pull requests goblins reported done from since to
// ended, read from every status log the state directory and its archive
// still hold, oldest first.
func doneBetween(stateDir string, since, ended time.Time) ([]afk.Finish, error) {
	logs := map[string]string{}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if id, ok := strings.CutSuffix(entry.Name(), ".status"); ok && !entry.IsDir() && state.ValidTaskID(id) == nil {
			logs[filepath.Join(stateDir, entry.Name())] = id
		}
	}
	archive := filepath.Join(stateDir, state.ArchiveDirName)
	archived, err := os.ReadDir(archive)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range archived {
		if m := archivedStatusFile.FindStringSubmatch(entry.Name()); m != nil && !entry.IsDir() {
			logs[filepath.Join(archive, entry.Name())] = m[1]
		} else if m := archivedTaskDir.FindStringSubmatch(entry.Name()); m != nil && entry.IsDir() {
			logs[filepath.Join(archive, entry.Name(), m[1]+".status")] = m[1]
		}
	}
	var finished []afk.Finish
	var unread error
	for path, id := range logs {
		lines, err := fsx.ReadLines(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			unread = errors.Join(unread, err)
			continue
		}
		for _, line := range lines {
			// A status line is stamped to the second.
			at, event := state.SplitStatus(line)
			rest, done := strings.CutPrefix(strings.TrimSpace(event), "done: PR ")
			if !done || at.Before(since.Truncate(time.Second)) || at.After(ended) {
				continue
			}
			if fields := strings.Fields(rest); len(fields) > 0 && strings.HasPrefix(fields[0], "https://") {
				finished = append(finished, afk.Finish{Task: id, PR: fields[0], At: at})
			}
		}
	}
	slices.SortFunc(finished, func(a, b afk.Finish) int {
		if order := a.At.Compare(b.At); order != 0 {
			return order
		}
		return strings.Compare(a.Task+a.PR, b.Task+b.PR)
	})
	return finished, unread
}
