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
// it has proven is his own terminal, and for one it has proven is the
// registered CFO when the request carries the words he asked for it with,
// logs a decision for a process it has proven is the registered CFO, records
// each item that waits on him as held, and hands the board nothing to
// announce while the switch is on (announce.go).
//
// The proof names its adversary: an agent that follows its contract and tries
// the command, the pipe or a wrapper around either. Every process of this
// Windows user can still write state/afk.json itself, which is the same
// boundary the board's other items have (docs/native-board.md).
//
// A switch at his ask proves who asks, never whose words those are: the CFO
// quotes them, and the supervisor cannot tell an ask of his from one the CFO
// misread or was handed by a goblin, a page or a tool's output. The CFO's
// contract stands there, and the switch, the log, the board and the report
// keep the words, so he sees what it was switched for.

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

// desktopPrograms are the programs a terminal of the Overlord's own is opened
// from, by executable name: the Windows desktop and Windows Terminal. A command
// whose parents reach neither had them cut off on the way.
var desktopPrograms = []string{"explorer", "windowsterminal"}

// onlyHis ends every refusal of the switch with who makes it.
const onlyHis = ": he turns it on or off from a terminal or a board of his own, and the registered CFO only at his ask, with his words"

// SwitchAFK asks the supervisor to turn AFK mode on or off for the process
// that calls it. The supervisor makes the switch only once it has proven that
// process runs in a terminal of the Overlord's own.
func SwitchAFK(h home.Home, on bool) error {
	return sendPipeRequest(h.State, runPipeRequest{Kind: afkSwitchKind(on)})
}

// SwitchAFKAtHisAsk asks the supervisor to turn AFK mode on or off for the
// registered CFO, which makes the switch at the Overlord's ask: asked is his
// words as the CFO quotes them. The supervisor makes the switch only once it
// has proven the calling process runs under that CFO.
func SwitchAFKAtHisAsk(h home.Home, on bool, asked string) error {
	asked, err := afk.HisWords(asked)
	if err != nil {
		return err
	}
	return sendPipeRequest(h.State, runPipeRequest{Kind: afkSwitchKind(on), Asked: asked})
}

func afkSwitchKind(on bool) string {
	if on {
		return "afk-on"
	}
	return "afk-off"
}

// LogAFKDecision records a decision the registered CFO made under AFK mode's
// authority, with its evidence. The supervisor writes the line only once it
// has proven the calling process runs under that CFO.
func LogAFKDecision(h home.Home, entry afk.Entry) error {
	return sendPipeRequest(h.State, runPipeRequest{Kind: "afk-log", AFK: &entry})
}

// asker is what asked the supervisor for the Overlord's switch, as a refusal
// words it: the command at the other end of the pipe, or the program that
// shows the board.
type asker struct {
	// runs opens a refusal that says where it runs.
	runs string
	// cut says whose parents stop short of the desktop, and the way out.
	cut string
}

var (
	askingCommand = asker{
		runs: "this command runs",
		cut:  "as it cannot those of a command run in Git Bash or under a program that replaces its own process, so nothing says it is his: run it in PowerShell or cmd",
	}
	askingBoard = asker{
		runs: "the program that shows this board runs",
		cut:  "as it cannot those of a browser whose opener has since exited, so nothing says it is his: open the board in the Code Goblins window or a browser he starts from the desktop, or run cfo afk on in PowerShell or cmd",
	}
)

// overlordsTerminal proves the process pid, which was running at connected,
// runs in a terminal of the Overlord's own, and says which.
func (s *Service) overlordsTerminal(pid int, connected time.Time) (string, error) {
	ancestry, err := s.overlordsOwn(pid, connected, askingCommand)
	if err != nil {
		return "", err
	}
	// The command itself is the first entry; the shell it was typed in is
	// the next.
	shell := ancestry[min(1, len(ancestry)-1)]
	return fmt.Sprintf("his own terminal (%s pid %d)", shell.ExeBase, shell.PID), nil
}

// overlordsOwn proves the process pid, which was running at asked, is one the
// Overlord started himself, and returns it with its parents. Whatever marks
// the process as an agent's refuses it: a goblin's or a gate agent's
// environment, the registered CFO among its ancestors, a terminal the fleet
// runs an agent in, or an agent harness above it. A process it cannot read, or
// whose parents it cannot follow to the desktop, is refused too, never taken
// for his.
func (s *Service) overlordsOwn(pid int, asked time.Time, who asker) ([]proc.Entry, error) {
	ancestry, env, err := s.inspect(pid)
	// A process that started after the request took the PID of the one that
	// sent it, and proves nothing. A terminal run as administrator is one the
	// supervisor cannot read, and it is the Overlord who meets that, so the
	// refusal says so.
	if err != nil || len(ancestry) == 0 || ancestry[0].Start.After(asked) {
		return nil, errors.New("AFK mode is the Supreme Overlord's switch, and the supervisor could not read the process that asked for it, as it cannot one run as administrator, so nothing says it is his" + onlyHis)
	}
	refuse := func(where string) ([]proc.Entry, error) {
		return nil, errors.New("AFK mode is the Supreme Overlord's switch, and " + who.runs + " " + where + onlyHis)
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
	// The same cut with those variables removed too, as Git Bash's env leaves
	// a command, has nothing left that marks an agent. Parents that stop short
	// of the desktop are parents the supervisor could not read, and Git Bash
	// cuts the Overlord's own the same way, so the refusal names the way out.
	if fromDesktop(ancestry) < 0 {
		return nil, errors.New("AFK mode is the Supreme Overlord's switch, and the supervisor could not follow its parents to the desktop, " + who.cut + onlyHis)
	}
	return ancestry, nil
}

// fromDesktop is where a process's parents reach the Windows desktop or
// Windows Terminal, or -1 when they stop short of both.
func fromDesktop(ancestry []proc.Entry) int {
	return slices.IndexFunc(ancestry, func(entry proc.Entry) bool {
		return slices.Contains(desktopPrograms, strings.TrimSuffix(strings.ToLower(entry.ExeBase), ".exe"))
	})
}

// inspect reads the process pid: its parents, itself first, and its
// environment.
func (s *Service) inspect(pid int) ([]proc.Entry, []string, error) {
	if s.inspectCaller != nil {
		return s.inspectCaller(pid)
	}
	ancestry, err := proc.Ancestry(pid, 32)
	if err != nil {
		return nil, nil, err
	}
	env, err := proc.Environment(pid)
	return ancestry, env, err
}

// cfoAtHisAsk proves the process pid, which was running at connected, runs
// under the registered primary CFO and is neither a goblin's nor a gate
// agent's, and says who it is. The registration stays open, so it cannot
// change, until release is called. It is the proof of a switch whose request
// carries the words the Overlord asked for it with: who asks is proven here,
// and whose words those are is not.
func (s *Service) cfoAtHisAsk(ctx context.Context, pid int, connected time.Time) (string, func(), error) {
	refuse := func(why string) (string, func(), error) {
		return "", nil, errors.New("AFK mode is the Supreme Overlord's switch, and " + why + onlyHis)
	}
	_, env, err := s.inspect(pid)
	switch {
	case err != nil:
		return refuse("the supervisor could not read the process that asked for it with his words")
	case environmentValue(env, harness.RoleVariable) == harness.RoleGoblin:
		return refuse(askingCommand.runs + " in a goblin's terminal")
	case environmentValue(env, gateAgentVariable) != "":
		return refuse(askingCommand.runs + " as a gate agent")
	case s.Options.CFO == nil:
		return refuse("this supervisor cannot verify the CFO")
	}
	_, release, err := s.Options.CFO.identityOf(ctx, pid, connected)
	if err != nil {
		return refuse("the supervisor could not prove the process that asked for it with his words is the registered CFO's (" + err.Error() + ")")
	}
	primary, _, err := readPrimary(s.Store.Home.State)
	if err != nil {
		release()
		return refuse("the supervisor could not read who the registered CFO is (" + err.Error() + ")")
	}
	return fmt.Sprintf("the CFO at his ask (%s pid %d)", primary.Agent, primary.Process.PID), release, nil
}

// switchAFK turns AFK mode on or off for the process at the other end of the
// pipe: once that process is proven to be the Overlord's own terminal, or,
// when the request carries the words he asked for the switch with, once it is
// proven to be the registered CFO's.
func (s *Service) switchAFK(ctx context.Context, pid int, connected time.Time, on bool, asked string) error {
	if asked == "" {
		from, err := s.overlordsTerminal(pid, connected)
		if err != nil {
			return err
		}
		return s.switchAFKAs(ctx, from, "", on)
	}
	asked, err := afk.HisWords(asked)
	if err != nil {
		return err
	}
	by, release, err := s.cfoAtHisAsk(ctx, pid, connected)
	if err != nil {
		return err
	}
	defer release()
	return s.switchAFKAs(ctx, by, asked, on)
}

// switchedSays opens the CFO's notice of a switch with who made it: the
// Overlord from where he did, or the CFO at his ask with his words.
func switchedSays(from, asked, to string) string {
	if asked == "" {
		return "the Overlord turned AFK mode " + to + " from " + from
	}
	return "AFK mode was turned " + to + " " + afk.SwitchedBy(from, asked)
}

// switchAFKAs turns AFK mode on or off for who its caller has proven asks: the
// Overlord from a terminal or a board of his own, which from names, or the
// registered CFO at his ask, which from names with his words in asked. Turning
// it on reads the allowance and tells the CFO through its wake queue; turning
// it off keeps the report of the stretch first, so a stretch never ends
// without one, and tells the CFO to write it into its terminal. Its caller
// holds runRequests.
func (s *Service) switchAFKAs(ctx context.Context, from, asked string, on bool) error {
	stateDir := s.Store.Home.State
	// quota-axi is read with afkChange free, so the supervisor's cycle never
	// waits behind it, and only for a switch that will be made. Requests for
	// the switch are taken one at a time, so it reads the same again below.
	s.afkChange.Lock()
	before, err := afk.Read(stateDir)
	s.afkChange.Unlock()
	allowance, unread := []afk.Allowance(nil), "this supervisor reads no allowance"
	if err == nil && on != before.On && s.Options.Allowance != nil {
		allowance, unread = s.Options.Allowance(ctx)
	}
	s.afkChange.Lock()
	defer s.afkChange.Unlock()
	current, err := afk.Read(stateDir)
	if err != nil {
		// His off always works: it puts a switch that cannot be read back to
		// off. His on does not, since it would guess at what the switch held,
		// and the CFO's switch does neither: the reset is his alone.
		if on || asked != "" {
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
	now := time.Now().UTC()
	if on {
		if asked == "" {
			_, _, err = afk.TurnOn(stateDir, from, allowance, now)
		} else {
			_, _, err = afk.TurnOnAtHisAsk(stateDir, from, asked, allowance, now)
		}
		if err != nil {
			return err
		}
		return s.afkNotice(switchedSays(from, asked, "on") + ": he is away until he turns it off, and nothing prompts him meanwhile. Decide what its authority covers yourself and log each decision, and leave what stays his alone held for him. Its terms stand above this queue.")
	}
	current.Ended, current.EndedFrom, current.EndedAsked = now, from, asked
	if err := afk.SaveReport(stateDir, s.afkReport(current, allowance, unread)); err != nil {
		return fmt.Errorf("AFK mode stays on: the report of its stretch could not be kept: %w", err)
	}
	if asked == "" {
		_, err = afk.TurnOff(stateDir, from, now)
	} else {
		_, err = afk.TurnOffAtHisAsk(stateDir, from, asked, now)
	}
	if err != nil {
		return err
	}
	return s.afkNotice(switchedSays(from, asked, "off") + ": he is back and decides again, so the standing rules apply. Write the report of the stretch into your terminal: cfo afk report")
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
		// A run the board made itself, for a credential card's terminal or a
		// connection's repair, is ready only for the moment after his own
		// click, and was never something that waited on him.
		if r.State == "ready" && r.CredentialRequest == "" && r.ConnectionTask == "" {
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

// holdForOverlord records each item that waits on the Overlord while AFK mode
// is on as held for him, once in a stretch: the record his report is built
// from. Nothing here keeps the board quiet; the announce endpoint does that
// by handing it nothing while the switch is on.
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
	for _, item := range waitingOnOverlord(s.Store.Snapshot()) {
		if s.held[item.key()] {
			continue
		}
		if err := afk.Hold(stateDir, afk.Entry{Item: item.key(), Task: item.task, What: bounded(item.what, 2000)}, now); err != nil {
			return err
		}
		s.held[item.key()] = true
	}
	return nil
}

// afkReport is the report of the stretch ended holds: the log's decisions,
// what each goblin reported done, each held item with what became of it, and
// the allowance read when it turned on beside after, read now.
func (s *Service) afkReport(ended afk.State, after []afk.Allowance, unread string) afk.Report {
	stateDir := s.Store.Home.State
	report := afk.Report{Session: ended.Session, Since: ended.Since, Ended: ended.Ended, From: ended.From, Asked: ended.Asked, EndedFrom: ended.EndedFrom, EndedAsked: ended.EndedAsked, Before: ended.Allowance, After: after}
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
	report.Held = s.heldOf(s.Store.Snapshot(), entries, report.Decisions)
	return report
}

// heldOf is each item a stretch's log holds for the Overlord, with what became
// of it by now and what its goblin did meanwhile. What the CFO answered is a
// decision in the log, not held. Only the log says so: a question the board
// closed as the CFO's with no decision logged stays held.
func (s *Service) heldOf(d Database, entries, decisions []afk.Entry) []afk.Held {
	answered := map[string]bool{}
	for _, decision := range decisions {
		if decision.Kind == afk.KindAnswer {
			answered["question:"+decision.What] = true
		}
	}
	held := []afk.Held{}
	for _, entry := range entries {
		if entry.Kind != afk.KindHeld || answered[entry.Item] {
			continue
		}
		waiting, now := heldNow(d, entry.Item)
		one := afk.Held{Item: entry.Item, Task: entry.Task, What: entry.What, At: entry.At, Waiting: waiting, Now: now}
		if entry.Task != "" {
			// A status line is stamped to the second.
			lines, _ := state.TailStatus(s.Store.Home.State, entry.Task, 50)
			if at, said := latestReport(lines, time.Time{}); !at.Before(entry.At.Truncate(time.Second)) {
				one.Meanwhile = bounded(said, 500)
			}
		}
		held = append(held, one)
	}
	return held
}

// AFKView is AFK mode as the board shows it: the switch, and while it is on
// how much the CFO decided and what is held for the Overlord.
type AFKView struct {
	// State is off, on, or unreadable for a switch that cannot be read, which
	// is never taken for on.
	State string `json:"state"`
	// Since and From say when it turned on and who turned it on: the Overlord
	// from where he did, or the CFO at his ask, with his words in Asked.
	Since *time.Time `json:"since,omitempty"`
	From  string     `json:"from,omitempty"`
	Asked string     `json:"asked,omitempty"`
	// Decided counts the decisions the CFO logged in the stretch that is on,
	// and Held is what the log holds for the Overlord in it, each with what
	// became of it.
	Decided int        `json:"decided"`
	Held    []afk.Held `json:"held"`
	// Report names the last stretch that ended with its report kept, and
	// Ended says when it ended.
	Report string     `json:"report,omitempty"`
	Ended  *time.Time `json:"ended,omitempty"`
	// Problem says why the switch cannot be read.
	Problem string `json:"problem,omitempty"`
}

// afkView reads AFK mode for a snapshot of d. A log that cannot be read leaves
// the view without what the log holds, and is the error returned.
func (s *Service) afkView(d Database) (AFKView, error) {
	stateDir := s.Store.Home.State
	view := AFKView{State: "off", Held: []afk.Held{}}
	switched, err := afk.Read(stateDir)
	switch {
	case err != nil:
		view.State, view.Problem = "unreadable", bounded(err.Error(), 1000)
	case switched.On:
		view.State, view.Since, view.From, view.Asked = "on", &switched.Since, switched.From, switched.Asked
		entries, _, err := s.afkLog.Entries(stateDir, switched.Session)
		if err != nil {
			return view, fmt.Errorf("AFK mode's log could not be read, so the board cannot say what was decided or held: %w", err)
		}
		decisions := afk.Decisions(entries)
		view.Decided, view.Held = len(decisions), s.heldOf(d, entries, decisions)
	case switched.Session != "" && !switched.Ended.IsZero():
		// A stretch never ends without its report kept, so the stretch the
		// switch last ended is the one whose report there is.
		view.Report, view.Ended = switched.Session, &switched.Ended
	}
	return view, nil
}

// heldNow is what became of a held item: whether it still waits on the
// Overlord, and in what words. It is asked only about an item the log holds
// no answer decision for.
func heldNow(d Database, item string) (waiting bool, now string) {
	kind, id, _ := strings.Cut(item, ":")
	closed := func(how, reason string) (bool, string) {
		if reason != "" {
			how += ": " + reason
		}
		return false, bounded(how, 500)
	}
	switch kind {
	case "question":
		i := slices.IndexFunc(d.Questions, func(q Question) bool { return q.ID == id })
		if i < 0 {
			break
		}
		switch q := d.Questions[i]; {
		case q.Status == "pending":
			return true, "still waiting on you"
		case q.AnsweredBy == "cfo":
			return closed("closed as answered by the CFO, and no decision was logged for it", "")
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
		return true, "still waiting on you"
	case "run":
		i := slices.IndexFunc(d.Runs, func(r Run) bool { return r.ID == id })
		if i < 0 {
			break
		}
		if r := d.Runs[i]; r.State != "ready" {
			return closed(r.State, r.Reason)
		}
		return true, "still waiting for you to run it"
	case "credential":
		i := slices.IndexFunc(d.Credentials, func(c CredentialRequest) bool { return c.ID == id })
		if i < 0 {
			break
		}
		if c := d.Credentials[i]; c.State != "open" {
			return closed(c.State, c.Reason)
		}
		return true, "still waiting for the values"
	}
	return false, "no longer listed on the board"
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
