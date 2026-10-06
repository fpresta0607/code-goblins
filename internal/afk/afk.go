// Package afk holds AFK mode, the Supreme Overlord's switch for running the
// fleet while he is away: the switch itself, the log of every decision the CFO
// makes under it and of every item held for him, and the report the switch
// ends with. It only reads and writes its own files in a home's state
// directory; the supervisor decides who may switch and who may log.
package afk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const (
	stateFile = "afk.json"
	auditFile = "afk.audit"
	// maxWhat and maxEvidence bound one line of the log.
	maxWhat     = 2000
	maxEvidence = 8000
	// maxAsked bounds the Overlord's words that a switch made at his ask
	// carries.
	maxAsked = 500
)

// State is the switch. A home where it was never turned on reads as the zero
// State, which is off.
type State struct {
	On bool `json:"on"`
	// Session names one stretch of AFK mode, from on to off: every line the
	// log holds of it carries it.
	Session string    `json:"session,omitempty"`
	Since   time.Time `json:"since,omitzero"`
	// From is where the Overlord turned it on, and EndedFrom where he turned
	// it off. A switch the CFO made at his ask names the CFO there, and Asked
	// or EndedAsked holds his words as the CFO quoted them; both are empty
	// for a switch he made himself.
	From       string    `json:"from,omitempty"`
	Asked      string    `json:"asked,omitempty"`
	Ended      time.Time `json:"ended,omitzero"`
	EndedFrom  string    `json:"ended_from,omitempty"`
	EndedAsked string    `json:"ended_asked,omitempty"`
	// Allowance is what quota-axi read when it turned on, which the report
	// sets beside the reading when it turned off.
	Allowance []Allowance `json:"allowance,omitempty"`
}

// Allowance is one reading of a provider's allowance: a window's use, or a
// credit balance.
type Allowance struct {
	Provider string `json:"provider"`
	// Window is the window's label, such as week, or credits.
	Window string `json:"window"`
	// PercentUsed is how much of a window is used, and ResetsAt when it resets.
	PercentUsed float64   `json:"percent_used"`
	ResetsAt    time.Time `json:"resets_at,omitzero"`
	// Credits says the reading is a credit balance: Remaining of Unit, or
	// Unlimited.
	Credits   bool    `json:"credits,omitempty"`
	Remaining float64 `json:"remaining,omitempty"`
	Unit      string  `json:"unit,omitempty"`
	Unlimited bool    `json:"unlimited,omitempty"`
}

// The kinds of line the log holds: the switch, an item held for the Overlord,
// a goblin the supervisor paused at a floor, and the decisions the CFO makes
// under the authority.
const (
	KindOn        = "on"
	KindOff       = "off"
	KindHeld      = "held"
	KindPause     = "pause"
	KindMerge     = "merge"
	KindAnswer    = "answer"
	KindDeploy    = "deploy"
	KindMigration = "migration"
	KindInstall   = "install"
	KindOther     = "other"
)

// DecisionKinds are the kinds Log takes, in the order the report lists them.
var DecisionKinds = []string{KindMerge, KindDeploy, KindMigration, KindInstall, KindAnswer, KindOther}

// Entry is one line of the log.
type Entry struct {
	At      time.Time `json:"at"`
	Session string    `json:"session"`
	Kind    string    `json:"kind"`
	// What is what was decided or held: a pull request, a question, a deploy.
	What string `json:"what,omitempty"`
	Link string `json:"link,omitempty"`
	// Evidence is what the decision stands on, in the CFO's words and the
	// facts the deciding command read.
	Evidence string `json:"evidence,omitempty"`
	// Outcome is how a decision that had to be logged before it was carried
	// out ended, on a second line for the same What.
	Outcome string `json:"outcome,omitempty"`
	// Task is the goblin an answer or a held item belongs to, and Item the
	// held item's key in the Command Center.
	Task string `json:"task,omitempty"`
	Item string `json:"item,omitempty"`
	// Recommendation is the choice recommended for a held question by whoever
	// asked it: the CFO for its own, or the goblin whose question it is.
	Recommendation string `json:"recommendation,omitempty"`
}

// ErrNotOn refuses what only happens while AFK mode is on.
var ErrNotOn = errors.New("AFK mode is not on")

// Read returns the switch as the home's state directory holds it. A switch
// that cannot be read is an error, never off, and the error names the way
// back: Reset, which is the Overlord's off.
func Read(stateDir string) (State, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	var state State
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&state)
	}
	if err != nil {
		return State{}, fmt.Errorf("AFK mode's switch (state/%s) cannot be read: %w; only the Overlord resets it, with cfo afk off from a terminal of his own", stateFile, err)
	}
	return state, nil
}

func write(stateDir string, state State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, stateFile), append(data, '\n'))
}

// HisWords are the Overlord's words asking for a switch, as the CFO quotes
// them, on one line. A switch made at his ask carries them, so none, or more
// than their bound, is refused.
func HisWords(asked string) (string, error) {
	asked = strings.Join(strings.Fields(asked), " ")
	if asked == "" || utf8.RuneCountInString(asked) > maxAsked {
		return "", fmt.Errorf("a switch the CFO makes at the Overlord's ask carries his words, in at most %d characters", maxAsked)
	}
	return asked, nil
}

// SwitchedBy says who made a switch, to follow "turned on" or "off": the
// Overlord, from where he did it, or the CFO at his ask, with the words of
// his the CFO quoted.
func SwitchedBy(from, asked string) string {
	if asked == "" {
		return "from " + from
	}
	return "by " + from + ", in his words " + strconv.Quote(asked)
}

// asEvidence is what a switch's line in the log stands on: his words when the
// CFO made it at his ask, and nothing when he made it himself.
func asEvidence(asked string) string {
	if asked == "" {
		return ""
	}
	return "his words: " + asked
}

// TurnOn turns AFK mode on from where the Overlord did it, with the allowance
// read then, and reports whether it changed anything: one already on stays as
// it is. The log line is written first, so a switch the log does not hold
// never happened.
func TurnOn(stateDir, from string, allowance []Allowance, now time.Time) (State, bool, error) {
	return turnOn(stateDir, from, "", allowance, now)
}

// TurnOnAtHisAsk is TurnOn for a switch the CFO made at the Overlord's ask:
// by names the CFO, and asked is his words as the CFO quoted them, which the
// switch and the log keep.
func TurnOnAtHisAsk(stateDir, by, asked string, allowance []Allowance, now time.Time) (State, bool, error) {
	asked, err := HisWords(asked)
	if err != nil {
		return State{}, false, err
	}
	return turnOn(stateDir, by, asked, allowance, now)
}

func turnOn(stateDir, from, asked string, allowance []Allowance, now time.Time) (State, bool, error) {
	state, err := Read(stateDir)
	if err != nil || state.On {
		return state, false, err
	}
	now = now.UTC()
	state = State{On: true, Session: "afk-" + now.Format("20060102T150405.000Z"), Since: now, From: from, Asked: asked, Allowance: allowance}
	if err := appendEntry(stateDir, Entry{At: now, Session: state.Session, Kind: KindOn, What: from, Evidence: asEvidence(asked)}); err != nil {
		return State{}, false, err
	}
	if err := write(stateDir, state); err != nil {
		return State{}, false, err
	}
	return state, true, nil
}

// TurnOff turns AFK mode off from where the Overlord did it. The state keeps
// the stretch it ended, which its report is built from.
func TurnOff(stateDir, from string, now time.Time) (State, error) {
	return turnOff(stateDir, from, "", now)
}

// TurnOffAtHisAsk is TurnOff for a switch the CFO made at the Overlord's ask,
// with his words as the CFO quoted them.
func TurnOffAtHisAsk(stateDir, by, asked string, now time.Time) (State, error) {
	asked, err := HisWords(asked)
	if err != nil {
		return State{}, err
	}
	return turnOff(stateDir, by, asked, now)
}

func turnOff(stateDir, from, asked string, now time.Time) (State, error) {
	state, err := Read(stateDir)
	if err != nil {
		return State{}, err
	}
	if !state.On {
		return state, ErrNotOn
	}
	state.On, state.Ended, state.EndedFrom, state.EndedAsked = false, now.UTC(), from, asked
	if err := appendEntry(stateDir, Entry{At: state.Ended, Session: state.Session, Kind: KindOff, What: from, Evidence: asEvidence(asked)}); err != nil {
		return State{}, err
	}
	if err := write(stateDir, state); err != nil {
		return State{}, err
	}
	return state, nil
}

// Reset puts a switch that cannot be read back to off from where the Overlord
// did it, so his off always works. The log says so and why first, as for any
// switch. What the switch held is not known, so no stretch ends here and no
// report is built: the log keeps what was decided.
func Reset(stateDir, from string, unread error, now time.Time) error {
	if err := appendEntry(stateDir, Entry{At: now.UTC(), Kind: KindOff, What: from, Evidence: "the switch could not be read and was reset to off: " + unread.Error()}); err != nil {
		return err
	}
	return write(stateDir, State{})
}

// Log records a decision the CFO made under the authority, in the stretch
// that is on, and returns the line it wrote. A decision names what was
// decided and the evidence it stands on.
func Log(stateDir string, entry Entry, now time.Time) (Entry, error) {
	switch {
	case !slices.Contains(DecisionKinds, entry.Kind):
		return Entry{}, fmt.Errorf("a decision's kind is one of %s", strings.Join(DecisionKinds, ", "))
	case strings.TrimSpace(entry.What) == "" || len(entry.What) > maxWhat:
		return Entry{}, fmt.Errorf("a decision names what was decided, in at most %d characters", maxWhat)
	case strings.TrimSpace(entry.Evidence) == "" || len(entry.Evidence) > maxEvidence:
		return Entry{}, fmt.Errorf("a decision carries its evidence, in at most %d characters", maxEvidence)
	}
	return record(stateDir, entry, now)
}

// Hold records an item that waits on the Overlord, in the stretch that is on.
func Hold(stateDir string, entry Entry, now time.Time) error {
	if strings.TrimSpace(entry.Item) == "" {
		return errors.New("a held line names its item")
	}
	entry.Kind = KindHeld
	_, err := record(stateDir, entry, now)
	return err
}

// Pause records a goblin the supervisor paused at a floor, in the stretch
// that is on: first with the readings the pause stood on, then with how it
// went. It is the supervisor's safety rail, not a decision of the CFO's.
func Pause(stateDir string, entry Entry, now time.Time) error {
	if strings.TrimSpace(entry.Task) == "" {
		return errors.New("a paused line names its goblin")
	}
	entry.Kind = KindPause
	_, err := record(stateDir, entry, now)
	return err
}

// record stamps entry with the stretch that is on and when, and appends it.
func record(stateDir string, entry Entry, now time.Time) (Entry, error) {
	state, err := Read(stateDir)
	if err != nil {
		return Entry{}, err
	}
	if !state.On {
		return Entry{}, ErrNotOn
	}
	entry.At, entry.Session = now.UTC(), state.Session
	return entry, appendEntry(stateDir, entry)
}

func appendEntry(stateDir string, entry Entry) error {
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := fsx.OpenAppend(filepath.Join(stateDir, auditFile), 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	return errors.Join(werr, f.Close())
}

// Entries returns the log's lines for one stretch, or every line when session
// is empty, and how many lines it could not read: a line cut short is
// counted, never dropped unseen.
func Entries(stateDir, session string) ([]Entry, int, error) {
	lines, err := fsx.ReadLines(filepath.Join(stateDir, auditFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var entries []Entry
	unreadable := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry Entry
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Kind == "" {
			unreadable++
			continue
		}
		if session == "" || entry.Session == session {
			entries = append(entries, entry)
		}
	}
	return entries, unreadable, nil
}
