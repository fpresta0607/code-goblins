package monitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// GateSample is what a goblin's no-mistakes run looks like from the outside:
// whether a run is active, which run and step, how long that step and its
// current round have been active, and the evidence that the step moves.
// NoCI records that the worktree has no GitHub Actions workflows at all,
// which is the one shape in which a ci step can never complete on its own.
type GateSample struct {
	Active    bool
	RunID     string
	Step      string
	ActiveFor time.Duration
	// RoundFor is how long the step's current round has run, since a fix
	// round runs the step again; zero where the gate keeps no round timer.
	RoundFor     time.Duration
	LastActivity string
	// MovedAt is when the step last showed progress of its own: its last
	// activity, such as its agent producing output, or the machine's heavy
	// line moving for its run. It is zero when neither could be read.
	MovedAt time.Time
	// Allowance is how long nine in ten of the step's rounds on the task's
	// harness took on this machine, and IsMeasured says any is on record.
	Allowance  time.Duration
	IsMeasured bool
	// Line says what the heavy line shows of the step's run, empty when the
	// run stands nowhere in it, and IsLineRead that the line was read.
	Line       string
	IsLineRead bool
	// Unread names the evidence that could not be read.
	Unread []string
	NoCI   bool
}

// GateProber reads a goblin's gate state. The monitor consults it only for a
// goblin that has read `working` past the busy-turn budget, so a probe cost
// of one subprocess is paid rarely and never on a healthy fleet.
type GateProber interface {
	InspectGate(ctx context.Context, meta state.TaskMeta) (GateSample, error)
}

// ExecGateProber shells out to `no-mistakes axi status` in the task worktree,
// then reads how long the step's rounds take on this machine from the gate's
// database through Commands, and the machine's heavy line through Line. A
// nil Commands or Line reads none of that, and the sample says so.
type ExecGateProber struct {
	Commands execx.Runner
	Line     func() (holding, waiting []verify.Standing, err error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// gateStatusBudget bounds one `no-mistakes axi status`, which answers in well
// under a second. The monitor's whole scan waits on it, and a supervisor whose
// scan never returns keeps its lock while its heartbeat stops, so nothing can
// take supervision over.
const gateStatusBudget = 30 * time.Second

func (p ExecGateProber) InspectGate(ctx context.Context, meta state.TaskMeta) (GateSample, error) {
	sample := GateSample{}
	if meta.Worktree == "" {
		return sample, nil
	}
	if _, err := os.Stat(filepath.Join(meta.Worktree, ".github", "workflows")); err != nil {
		sample.NoCI = true
	}
	statusCtx, cancel := context.WithTimeout(ctx, gateStatusBudget)
	defer cancel()
	root, err := pipeline.DefaultRoot()
	if err != nil {
		return sample, err
	}
	cmd := execx.CommandContext(statusCtx, "no-mistakes", "axi", "status")
	cmd.Dir = meta.Worktree
	cmd.Env = pipeline.NativeEnv(root)
	// A process the status call leaves running, such as a daemon it starts,
	// inherits the output pipe; without a delay Wait holds until that process
	// exits, which for a daemon is never.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return sample, err
	}
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	sample = parseGateStatus(string(out), sample)
	if sample.Active {
		sample = p.stepEvidence(ctx, root, meta.Harness, sample, now)
	}
	return sample, nil
}

// stepEvidence adds to an active step's sample what tells a long step from a
// stalled one: when its last activity was, read at now; what the machine's
// heavy line shows of its run, which a Test step holds or waits in while it
// shows nothing of its own; and how long its rounds take on harness on this
// machine (#2165).
func (p ExecGateProber) stepEvidence(ctx context.Context, root, harness string, sample GateSample, now time.Time) GateSample {
	if quiet, ok := activityAge(sample.LastActivity); ok {
		sample.MovedAt = now.Add(-quiet)
	} else {
		sample.Unread = append(sample.Unread, "when the step last showed activity")
	}
	switch {
	case p.Line == nil:
		sample.Unread = append(sample.Unread, "the heavy line, which no reader was given")
	case sample.RunID == "":
		sample.Unread = append(sample.Unread, "the heavy line, since the gate status named no run")
	default:
		holding, waiting, err := p.Line()
		if err != nil {
			sample.Unread = append(sample.Unread, "the heavy line ("+err.Error()+")")
			break
		}
		moved, line := lineMoved(holding, waiting, sample.RunID, now)
		if moved.After(sample.MovedAt) {
			sample.MovedAt = moved
		}
		sample.Line, sample.IsLineRead = line, true
	}
	if p.Commands == nil {
		sample.Unread = append(sample.Unread, "how long its rounds take on this machine, which no reader was given")
		return sample
	}
	usual, err := (pipeline.Reader{Root: root, Commands: p.Commands}).UsualRoundTimes(ctx)
	if err != nil {
		sample.Unread = append(sample.Unread, "how long its rounds take on this machine ("+err.Error()+")")
		return sample
	}
	sample.Allowance, sample.IsMeasured = usual[pipeline.StepHarness{Step: sample.Step, Harness: harness}]
	return sample
}

// lineMoved is when the machine's heavy line last moved for the gate run
// runID, and what it shows of it, or zero and nothing while none of its runs
// stands there. A run of it that holds a turn within the budget its turn
// names is at work, since its caller ends it at that budget, so the line
// moved for it now; past that budget, or with none named, it last moved when
// the run took its turn. A run of it that waits moved when the turn last
// passed to a run that holds one, or when it joined the line, whichever is
// later, as fleettree reads a goblin's own wait. A gate's run is named by
// its worktree, which no-mistakes names by the run.
func lineMoved(holding, waiting []verify.Standing, runID string, now time.Time) (time.Time, string) {
	for _, run := range holding {
		if !strings.Contains(run.Who, runID) {
			continue
		}
		line := "its run has held a turn in the heavy line for " + now.Sub(run.Since).Round(time.Minute).String()
		if run.Now != "" {
			line += " (" + run.Now + ")"
		}
		if run.Budget > 0 && now.Before(run.Since.Add(run.Budget)) {
			return now, line + ", within its " + run.Budget.String() + " budget"
		}
		return run.Since, line + ", with no budget left to it"
	}
	for _, run := range waiting {
		if !strings.Contains(run.Who, runID) {
			continue
		}
		moved := run.Since
		for _, holder := range holding {
			if holder.Since.After(moved) {
				moved = holder.Since
			}
		}
		return moved, "its run waits in the heavy line, which last moved " + now.Sub(moved).Round(time.Minute).String() + " ago"
	}
	return time.Time{}, ""
}

// activityAge reads how long ago a step's last activity was from the
// last_activity column of axi status, as "1s ago: claude producing output",
// "quiet 22m30s ago: log: ..." or "unknown". Its durations are no-mistakes'
// compact ones, whose longest form counts days as "2d3h".
func activityAge(activity string) (time.Duration, bool) {
	age, _, ok := strings.Cut(strings.TrimPrefix(activity, "quiet "), " ago")
	if !ok {
		return 0, false
	}
	var days time.Duration
	if count, rest, isDays := strings.Cut(age, "d"); isDays {
		parsed, err := strconv.Atoi(count)
		if err != nil {
			return 0, false
		}
		days, age = time.Duration(parsed)*24*time.Hour, rest
	}
	quiet, err := time.ParseDuration(age)
	if err != nil {
		return 0, false
	}
	return days + quiet, true
}

// gateProbeEvery is how long one reading of a worktree's gate stands for. The
// monitor asks a goblin's gate once a minute once it has been busy past
// busyTurnMax, and each `no-mistakes axi status` starts about ten git
// processes, so a reading stands for five minutes: a gate step that stalls is
// named at the first fresh reading, at most five minutes later, and a stall
// that was real when read can stand in the observation up to five minutes
// after the step moves on.
const gateProbeEvery = 5 * time.Minute

// RecentGateProber reads each worktree's gate through Probe at most once per
// gateProbeEvery. Within the period it answers the last reading exactly as it
// was read, so a kept reading can delay a wake by at most gateProbeEvery and
// never invent one.
type RecentGateProber struct {
	Probe GateProber
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu       sync.Mutex
	readings map[string]gateReading
}

type gateReading struct {
	at     time.Time
	sample GateSample
	err    error
}

func (p *RecentGateProber) InspectGate(ctx context.Context, meta state.TaskMeta) (GateSample, error) {
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	p.mu.Lock()
	reading, ok := p.readings[meta.Worktree]
	p.mu.Unlock()
	if !ok || now.Sub(reading.at) >= gateProbeEvery {
		sample, err := p.Probe.InspectGate(ctx, meta)
		reading = gateReading{at: now, sample: sample, err: err}
		p.mu.Lock()
		if p.readings == nil {
			p.readings = map[string]gateReading{}
		}
		p.readings[meta.Worktree] = reading
		p.mu.Unlock()
	}
	return reading.sample, reading.err
}

// parseGateStatus reads the run's id and the active_steps row out of `axi
// status`. Only the
// active row matters: a completed or pending step is never what a goblin is
// wedged on. A step is active while it runs and while an agent fixes what it
// found. The row is read by the column names in the block's header, since
// no-mistakes has added columns before.
func parseGateStatus(out string, sample GateSample) GateSample {
	var columns []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if id, ok := strings.CutPrefix(trimmed, "id: "); ok && columns == nil && sample.RunID == "" {
			sample.RunID = strings.Trim(id, `"`)
			continue
		}
		if header, ok := strings.CutPrefix(trimmed, "active_steps["); ok {
			_, names, _ := strings.Cut(header, "{")
			names, _, _ = strings.Cut(names, "}")
			columns = strings.Split(names, ",")
			continue
		}
		if columns == nil || trimmed == "" {
			continue
		}
		row := splitGateRow(trimmed)
		if len(row) != len(columns) {
			break
		}
		field := func(name string) string {
			if index := slices.Index(columns, name); index >= 0 {
				return row[index]
			}
			return ""
		}
		if status := field("status"); status != "running" && status != "fixing" {
			break
		}
		sample.Active = true
		sample.Step = field("step")
		if d, err := time.ParseDuration(field("active_for")); err == nil {
			sample.ActiveFor = d
		}
		if d, err := time.ParseDuration(field("round_active_for")); err == nil {
			sample.RoundFor = d
		}
		sample.LastActivity = strings.TrimSpace(field("last_activity"))
		break
	}
	return sample
}

// splitGateRow splits a TOON row at each comma outside a quoted value. TOON
// quotes a value with JSON string escapes, so inside quotes a backslash
// escapes the next character, and a quoted value is decoded as a JSON string.
func splitGateRow(row string) []string {
	var fields []string
	start := 0
	isQuoted := false
	for index := 0; index < len(row); index++ {
		switch {
		case isQuoted && row[index] == '\\':
			index++
		case row[index] == '"':
			isQuoted = !isQuoted
		case !isQuoted && row[index] == ',':
			fields = append(fields, row[start:index])
			start = index + 1
		}
	}
	fields = append(fields, row[start:])
	for index, field := range fields {
		if len(field) < 2 || field[0] != '"' || field[len(field)-1] != '"' {
			continue
		}
		var decoded string
		if err := json.Unmarshal([]byte(field), &decoded); err != nil {
			decoded = field[1 : len(field)-1]
		}
		fields[index] = decoded
	}
	return fields
}
