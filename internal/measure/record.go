// Package measure records what each role of a goblin's run spent, read from
// what the run already recorded: the lead's and its workers' own
// transcripts, and the gate's database of its agents' calls. It runs no
// probe and makes no call that bills, and a role whose spend cannot be read
// is named unmeasured, never estimated.
package measure

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// FileName is the file in the home's state folder that keeps every run's
// record, one JSON object to a line, oldest first. It outlives the tasks it
// measures, so a later routing table or decision has every run to stand on.
const FileName = "role-measurements.jsonl"

// The roles a record names: the role table's own, a session's other
// helpers, and the gate's steps that are no role's.
const (
	RoleLead      = "lead"
	RoleImplement = "implement"
	RoleReview    = "review"
	RoleVerify    = "verify"
	RoleFix       = "fix"
	RoleHelper    = "helper"
	RoleGate      = "gate"
)

// roleOrder is the order a table lists roles in.
var roleOrder = []string{RoleLead, RoleImplement, RoleReview, RoleVerify, RoleFix, RoleHelper, RoleGate}

// Usage is the tokens a role spent: every input token, the cache reads
// among them, and the output.
type Usage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
}

func (u *Usage) add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.CacheReadTokens += other.CacheReadTokens
}

// Role is what one role spent on one harness, model and effort in one run.
// Calls counts a session each for a role read from transcripts and a call
// each for a gate role, and its tokens and wall time are summed over them.
// Agent names a session helper's agent type or a gate step's purpose.
// Untallied counts the gate calls that recorded no tokens, so a total that
// lacks them never passes for a whole one. A review role counts the
// findings its calls reported, and a verify role the gate runs whose Test
// step passed and failed on its model.
type Role struct {
	Role      string `json:"role"`
	Harness   string `json:"harness"`
	Model     string `json:"model"`
	Effort    string `json:"effort,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Calls     int    `json:"calls"`
	Untallied int    `json:"untallied,omitempty"`
	Usage
	WallSeconds float64 `json:"wall_seconds"`
	Findings    int     `json:"review_findings,omitempty"`
	Passed      int     `json:"verification_passed,omitempty"`
	Failed      int     `json:"verification_failed,omitempty"`
}

// key is what makes two rows the same role's.
func (r Role) key() string {
	return r.Role + "\x00" + r.Harness + "\x00" + r.Model + "\x00" + r.Effort + "\x00" + r.Agent
}

func (r *Role) add(other Role) {
	r.Calls += other.Calls
	r.Untallied += other.Untallied
	r.Usage.add(other.Usage)
	r.WallSeconds += other.WallSeconds
	r.Findings += other.Findings
	r.Passed += other.Passed
	r.Failed += other.Failed
}

// Record is one run of a task: its work from its previous done or failed
// report, or from its spawn, to this report. Runs are the gate runs of its
// branch in that time, Escalations the roles it moved up a tier, and
// Unmeasured what could not be read and why.
type Record struct {
	Task        string    `json:"task"`
	Project     string    `json:"project"`
	Outcome     string    `json:"outcome"`
	PR          string    `json:"pr,omitempty"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	Runs        []string  `json:"gate_runs,omitempty"`
	Escalations []string  `json:"escalations,omitempty"`
	Roles       []Role    `json:"roles"`
	Unmeasured  []string  `json:"unmeasured,omitempty"`
}

// Append adds record to the home's measurements in stateDir.
func Append(stateDir string, record Record) error {
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := fsx.OpenAppend(filepath.Join(stateDir, FileName), 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		return errors.Join(err, file.Close())
	}
	return file.Close()
}

// Read returns the home's measurements in stateDir, oldest first: none
// before the first run is recorded.
func Read(stateDir string) ([]Record, error) {
	lines, err := fsx.ReadLines(filepath.Join(stateDir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(lines))
	for index, line := range lines {
		var record Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("measure: line %d of %s: %w", index+1, FileName, err)
		}
		records = append(records, record)
	}
	return records, nil
}

// Since is when task's last recorded run ended, and false when none is.
func Since(records []Record, task string) (time.Time, bool) {
	for _, record := range slices.Backward(records) {
		if record.Task == task {
			return record.To, true
		}
	}
	return time.Time{}, false
}

// Total is one role's rows summed over the runs that recorded it.
type Total struct {
	Role
	Runs int
}

// Totals sums records' rows by role, harness, model, effort and agent, in
// the role table's order and then by harness, model, effort and agent.
func Totals(records []Record) []Total {
	byKey := map[string]*Total{}
	for _, record := range records {
		for _, role := range record.Roles {
			total, isKnown := byKey[role.key()]
			if !isKnown {
				total = &Total{Role: Role{Role: role.Role, Harness: role.Harness, Model: role.Model, Effort: role.Effort, Agent: role.Agent}}
				byKey[role.key()] = total
			}
			total.add(role)
			total.Runs++
		}
	}
	totals := make([]Total, 0, len(byKey))
	for _, total := range byKey {
		totals = append(totals, *total)
	}
	slices.SortFunc(totals, func(a, b Total) int {
		return cmp.Or(cmp.Compare(slices.Index(roleOrder, a.Role.Role), slices.Index(roleOrder, b.Role.Role)), cmp.Compare(a.key(), b.key()))
	})
	return totals
}
