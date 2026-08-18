package monitor

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/routing"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

type Prober interface {
	Inspect(ctx context.Context, meta state.TaskMeta) (EndpointSample, error)
}

// CycleProber is the optional Prober extension for batch evidence: Scan marks
// one cycle boundary before inspecting any task so a structural prober can
// supply every task from one coherent snapshot.
type CycleProber interface {
	BeginScan(ctx context.Context)
}

// Service scans read-only endpoint samples and persists classification state.
// It deliberately has no lifecycle, send, treehouse, or delete dependency.
type Service struct {
	StateDir              string
	Probe                 Prober
	Now                   func() time.Time
	StaleEscalateAfter    time.Duration
	StallAfter            time.Duration
	LaunchGrace           time.Duration
	BusyTurnMax           time.Duration
	PauseResurfaceAfter   time.Duration
	DemandInspectionAfter int
	Heartbeat             time.Duration
	HeartbeatMax          time.Duration
}

type ScanResult struct {
	Observations []Observation
	Heartbeat    Heartbeat
	Event        *Event
}

// Scan reloads durable observations and the heartbeat before each
// classification. It stores a pending event before returning it so a caller
// can retry Publish after a crash without losing the wake reason.
func (s Service) Scan(ctx context.Context) (ScanResult, error) {
	if s.StateDir == "" {
		return ScanResult{}, errors.New("monitor: state directory is required")
	}
	now := s.now().UTC()
	heartbeat, err := ReadHeartbeat(s.StateDir)
	heartbeatCorrupt := err != nil && !errors.Is(err, os.ErrNotExist)
	if errors.Is(err, os.ErrNotExist) {
		heartbeat = s.freshHeartbeat(now)
	} else if heartbeatCorrupt {
		heartbeat = s.freshHeartbeat(now)
	} else if heartbeat.LastHeartbeat.IsZero() && heartbeat.NextDue.IsZero() {
		heartbeat.LastHeartbeat = now
		heartbeat.NextDue = now.Add(s.heartbeat())
		heartbeat.NoChangeStreak = 0
	}
	heartbeat.LastCycle = now

	result := ScanResult{Heartbeat: heartbeat}
	if heartbeat.PendingEvent != nil {
		result.Event = cloneEvent(heartbeat.PendingEvent)
	}

	entries, err := os.ReadDir(s.StateDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return ScanResult{}, err
		}
		entries = nil
	}
	if cycler, ok := s.Probe.(CycleProber); ok {
		cycler.BeginScan(ctx)
	}
	for _, entry := range entries {
		extension := filepath.Ext(entry.Name())
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !strings.EqualFold(extension, ".meta") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), extension)
		if state.ValidTaskID(id) != nil {
			continue
		}
		meta, err := state.ReadTaskMeta(s.StateDir, id)
		if err != nil || meta.Backend != "herdr" {
			continue
		}

		prior, priorErr := ReadObservation(s.StateDir, id)
		if priorErr != nil && !errors.Is(priorErr, os.ErrNotExist) {
			if heartbeatCorrupt {
				return ScanResult{}, fmt.Errorf("monitor: cannot persist invalid-record event for %s while heartbeat and observation records are corrupt", id)
			}
			observation := Observation{
				Schema:          Schema,
				TaskID:          id,
				Endpoint:        endpointString(meta),
				EndpointVerdict: ProbeUnknown,
				LastObserved:    now,
				Health:          HealthUnknown,
				Reason:          InvalidRecord,
			}
			result.Observations = append(result.Observations, observation)
			if result.Event == nil {
				event := taskEvent(id, InvalidRecord, "")
				heartbeat.PendingEvent = &event
				result.Event = cloneEvent(&event)
			}
			continue
		}
		if errors.Is(priorErr, os.ErrNotExist) {
			prior = Observation{TaskID: id}
		}
		if heartbeatCorrupt {
			observation := invalidRecordObservation(meta, prior, now)
			if priorErr == nil {
				if err := WriteObservation(s.StateDir, observation); err != nil {
					return ScanResult{}, err
				}
			} else if errors.Is(priorErr, os.ErrNotExist) {
				if err := WriteObservation(s.StateDir, observation); err != nil {
					return ScanResult{}, err
				}
			}
			result.Observations = append(result.Observations, observation)
			if result.Event == nil {
				result.Event = cloneEvent(observation.PendingEvent)
			}
			continue
		}

		observation := s.classify(ctx, meta, prior, now)
		if err := WriteObservation(s.StateDir, observation); err != nil {
			return ScanResult{}, err
		}
		result.Observations = append(result.Observations, observation)
		if result.Event == nil && observation.PendingEvent != nil {
			result.Event = cloneEvent(observation.PendingEvent)
		}
	}

	if !heartbeatCorrupt && s.heartbeatDue(heartbeat, now) {
		heartbeat.LastHeartbeat = now
		if result.Event == nil && hasUnsurfacedActionable(result.Observations) {
			event := Event{Source: HeartbeatEvent, Kind: "heartbeat", Key: "heartbeat", Detail: "actionable fleet observation"}
			heartbeat.PendingEvent = &event
			heartbeat.NoChangeStreak = 0
			result.Event = cloneEvent(&event)
		} else {
			heartbeat.NoChangeStreak++
		}
		heartbeat.NextDue = now.Add(s.backoff(heartbeat.NoChangeStreak))
	}
	if !heartbeatCorrupt {
		if err := WriteHeartbeat(s.StateDir, heartbeat); err != nil {
			return ScanResult{}, err
		}
	}
	result.Heartbeat = heartbeat
	return result, nil
}

// Publish hands a previously persisted event to the existing durable wake
// queue, publishes its recovery episode, then clears only the matching pending
// event. Any failure before that clear leaves retry evidence intact.
func (s Service) Publish(event Event) (wake.Record, error) {
	record, err := wake.Append(s.StateDir, event.Kind, event.Key, event.Detail)
	if err != nil {
		return wake.Record{}, err
	}
	if _, err := wake.PublishEpisode(s.StateDir); err != nil {
		return record, err
	}
	if err := s.clearPending(event); err != nil {
		return record, err
	}
	return record, nil
}

func (s Service) classify(ctx context.Context, meta state.TaskMeta, prior Observation, now time.Time) Observation {
	observation := prior
	observation.Schema = Schema
	observation.TaskID = meta.ID
	observation.Endpoint = endpointString(meta)
	observation.LastObserved = now

	if s.Probe == nil {
		return unknownObservation(observation, EndpointUnknown, "monitor probe is unavailable", now)
	}
	sample, err := s.Probe.Inspect(ctx, meta)
	if err != nil {
		return unknownObservation(observation, EndpointUnknown, err.Error(), now)
	}
	if sample.Verdict == ProbeMissing {
		return unknownObservation(observation, EndpointMissing, sample.Detail, now)
	}
	if sample.Verdict != ProbePresent || !validSample(meta, sample) {
		detail := sample.Detail
		if detail == "" {
			detail = "endpoint identity did not validate"
		}
		// A freshly created task publishes its metadata before the harness can
		// register, so a present pane with no agent yet is still launching,
		// not harness death. Stay quiet only within the launch budget: a task
		// whose agent registers and dies before it is ever observed alive must
		// not stay "launching" forever, so past the budget it wakes as death.
		if sample.Verdict == ProbePresent && sample.Agent == herdr.AgentDead && prior.LastSeen.IsZero() && now.Before(s.launchDeadline(meta)) {
			return launchingObservation(observation, now)
		}
		return unknownObservation(observation, EndpointUnknown, detail, now)
	}

	observation.EndpointVerdict = ProbePresent
	digest := fmt.Sprintf("%x", sha256.Sum256(sample.Capture))
	stamp := s.statusStamp(meta.ID)

	// A harness being refused by its provider is checked before anything
	// else. An erroring pane often keeps changing - the error scrolls, the
	// harness retries - so treating a changed digest as progress would report
	// a rate-limited goblin as healthy and busy.
	if fault, detail, found := routing.Detect(string(sample.Capture)); found {
		return erroringObservation(observation, digest, fault, detail, now)
	}
	if observation.Health == HealthErroring {
		// The error cleared on its own; fall through so the ordinary rules
		// classify the pane afresh rather than leaving it stuck erroring.
		observation.Digest = ""
	}
	if observation.Digest == "" || observation.Digest != digest {
		observation.Digest = digest
		observation.StatusStamp = stamp
		observation.LastSeen = now
		observation.LastProgress = now
		observation.IdleSince = nil
		observation.StaleSince = nil
		observation.NextEscalation = nil
		observation.NextPauseResurface = nil
		observation.Health = HealthActive
		observation.Reason = None
		observation.Escalation = 0
		observation.DemandDeepInspection = false
		return observation
	}

	switch sample.Busy {
	case herdr.BusyWorking:
		return s.busyOrStale(observation, meta.ID, now)
	case herdr.BusyIdle:
		if s.declaredPaused(meta.ID) {
			return s.pauseObservation(observation, now)
		}
		// A status line written since the last observation is liveness even
		// when both the pane and herdr report quiet: the goblin is still
		// working. Reset the idle clock rather than walking toward a stall.
		if stamp != "" && stamp != observation.StatusStamp {
			observation.StatusStamp = stamp
			observation.LastSeen = now
			observation.LastProgress = now
			observation.IdleSince = nil
			observation.StaleSince = nil
			observation.NextEscalation = nil
			observation.NextPauseResurface = nil
			observation.Health = HealthActive
			observation.Reason = None
			observation.Escalation = 0
			observation.DemandDeepInspection = false
			return observation
		}
		// A pane already classified stale stays stale while its digest is
		// unchanged, whatever the stale reason was: the goblin is still stuck.
		if observation.Health == HealthStale {
			return s.staleObservation(observation, observation.Reason, now)
		}
		// A parked goblin awaiting an answer prints a question and sits at a
		// prompt; pi has no dialog widget to report it as "blocked", so the
		// pane text is the only signal. Detect it once the pane has been idle
		// (unchanged, no spinner) for a cycle, and wake immediately.
		if observation.IdleSince != nil {
			if question, ok := awaitingAnswer(sample.Capture); ok {
				return awaitingAnswerObservation(observation, question, now)
			}
		}
		return s.idleObservation(observation, now)
	default:
		return unknownObservation(observation, EndpointUnknown, "endpoint activity is unknown", now)
	}
}

func (s Service) staleObservation(observation Observation, reason Reason, now time.Time) Observation {
	observation.Health = HealthStale
	observation.Reason = reason
	observation.NextPauseResurface = nil
	if observation.PendingEvent != nil {
		return observation
	}
	if observation.StaleSince == nil {
		observation.StaleSince = timePointer(now)
		next := now.Add(s.staleEscalateAfter())
		observation.NextEscalation = &next
		observation.Escalation = 0
		observation.DemandDeepInspection = false
		// A genuine stall: no liveness signal (pane, status log, or busy)
		// moved for the stall window and no notify arrived. This is
		// the uncooperative case a dead or wedged goblin cannot notify about,
		// so it wakes once with the stall reason.
		event := taskEvent(observation.TaskID, reason, "no liveness signal for "+s.stallAfter().String())
		observation.PendingEvent = &event
		return observation
	}
	if observation.NextEscalation != nil && !now.Before(*observation.NextEscalation) {
		observation.Escalation++
		next := observation.NextEscalation.Add(s.staleEscalateAfter())
		observation.NextEscalation = &next
		if observation.Escalation >= s.demandInspectionAfter() {
			observation.DemandDeepInspection = true
		}
		// Dedupe: escalation does not re-wake. The CFO was already woken for
		// this exact stale state; the only thing that advances is the
		// escalation column, which fleet-view reads without a new wake. A
		// genuinely new state (digest change, recovery, a fault) clears this
		// and wakes on its own.
	}
	return observation
}

// busyOrStale classifies a pane that is working (or shows live activity) as
// busy until its busy reference ages past the turn budget, at which point it
// is stale by busy-turn-over-age.
func (s Service) busyOrStale(observation Observation, id string, now time.Time) Observation {
	observation.IdleSince = nil
	if s.busyReference(id).Add(s.busyTurnMax()).After(now) {
		observation.Health = HealthBusy
		observation.Reason = None
		observation.StaleSince = nil
		observation.NextEscalation = nil
		observation.NextPauseResurface = nil
		observation.Escalation = 0
		observation.DemandDeepInspection = false
		return observation
	}
	return s.staleObservation(observation, BusyTurnOverAge, now)
}

// idleObservation classifies an idle-and-unchanged pane behind a substantial
// grace period. A goblin legitimately thinking or running a quiet subprocess
// can sit at an unchanged pane for minutes; it is only stale once it has been
// genuinely idle (no digest change, no spinner) for the idle threshold.
func (s Service) idleObservation(observation Observation, now time.Time) Observation {
	if observation.IdleSince == nil {
		observation.IdleSince = timePointer(now)
	}
	if now.Sub(*observation.IdleSince) < s.stallAfter() {
		observation.Health = HealthIdle
		observation.Reason = None
		observation.StaleSince = nil
		observation.NextEscalation = nil
		observation.NextPauseResurface = nil
		observation.Escalation = 0
		observation.DemandDeepInspection = false
		return observation
	}
	observation.IdleSince = nil
	return s.staleObservation(observation, UnchangedIdle, now)
}

// awaitingAnswer reports whether a pane tail is parked on a substantive
// question the goblin asked and is waiting to have answered. Pi has no
// interactive dialog widget, so a parked question reads as merely "idle" to
// herdr; the pane text is the only signal. The question must be the final
// substantive line - any later output means the goblin moved on - and must not
// be an interactive confirmation prompt (a script's "[y/N]", not a question to
// the CFO). The goblin's own cfo notify confirmation is never a question: it
// is the goblin reporting one.
func awaitingAnswer(capture []byte) (string, bool) {
	lines := strings.Split(strings.TrimRight(string(capture), "\r\n"), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || isNotifyEcho(line) {
			continue
		}
		if !strings.HasSuffix(line, "?") || len(line) < 6 || isConfirmationPrompt(line) {
			return "", false
		}
		return line, true
	}
	return "", false
}

// isConfirmationPrompt reports whether a line is a machine confirmation prompt
// (a script offering a y/n or yes/no choice) rather than a question a goblin
// is asking the CFO.
func isConfirmationPrompt(line string) bool {
	lower := strings.ToLower(line)
	for _, marker := range []string{"[y/n]", "(y/n)", "[yes/no]", "(yes/no)"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// isNotifyEcho reports whether a pane line is the goblin's own cfo notify
// confirmation ("notified <id> blocked: ...", "notified <id> done: ...", or
// "notified <id> failed: ..."), which the goblin printed while reporting.
func isNotifyEcho(line string) bool {
	rest, ok := strings.CutPrefix(line, "notified ")
	if !ok {
		return false
	}
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return false
	}
	switch fields[1] {
	case "done:", "blocked:", "failed:":
		return true
	}
	return false
}

// awaitingAnswerObservation records a goblin parked on a printed question,
// waiting for a decision. It wakes once per parked episode with the question
// text, and demands deep inspection because the fix is a CFO decision, not
// another poll.
func awaitingAnswerObservation(observation Observation, question string, now time.Time) Observation {
	first := observation.Reason != AwaitingAnswer
	observation.IdleSince = nil
	observation.Health = HealthStale
	observation.Reason = AwaitingAnswer
	observation.NextPauseResurface = nil
	if observation.StaleSince == nil {
		observation.StaleSince = timePointer(now)
		observation.NextEscalation = timePointer(now.Add(time.Hour))
	}
	observation.Escalation = 0
	observation.DemandDeepInspection = true
	if first && observation.PendingEvent == nil {
		event := taskEvent(observation.TaskID, AwaitingAnswer, question)
		observation.PendingEvent = &event
	}
	return observation
}

func (s Service) pauseObservation(observation Observation, now time.Time) Observation {
	observation.Health = HealthPaused
	observation.Reason = DeclaredPause
	observation.StaleSince = nil
	observation.NextEscalation = nil
	observation.Escalation = 0
	observation.DemandDeepInspection = false
	if observation.PendingEvent != nil {
		return observation
	}
	if observation.NextPauseResurface == nil || !now.Before(*observation.NextPauseResurface) {
		next := now.Add(s.pauseResurfaceAfter())
		if observation.NextPauseResurface != nil {
			next = observation.NextPauseResurface.Add(s.pauseResurfaceAfter())
		}
		observation.NextPauseResurface = &next
		event := taskEvent(observation.TaskID, DeclaredPause, "")
		observation.PendingEvent = &event
	}
	return observation
}

// erroringObservation records a pane whose harness is being refused by its
// provider. It raises a wake event once per episode - the fault does not
// resolve itself, so repeating it every cycle would be noise - and demands
// deep inspection, because the fix is a decision (switch, wait, or top up)
// rather than another poll.
func erroringObservation(observation Observation, digest string, fault routing.Fault, detail string, now time.Time) Observation {
	first := observation.Health != HealthErroring
	observation.Digest = digest
	observation.LastObserved = now
	observation.LastSeen = now
	if first {
		observation.LastProgress = now
	}
	observation.Health = HealthErroring
	observation.Reason = HarnessError
	observation.StaleSince = nil
	observation.NextEscalation = nil
	observation.NextPauseResurface = nil
	observation.Escalation = 0
	observation.DemandDeepInspection = true
	if first && observation.PendingEvent == nil {
		event := taskEvent(observation.TaskID, HarnessError, string(fault)+": "+detail)
		event.Fault = fault
		observation.PendingEvent = &event
	}
	return observation
}

func unknownObservation(observation Observation, reason Reason, detail string, now time.Time) Observation {
	observation.LastObserved = now
	observation.EndpointVerdict = ProbeUnknown
	if reason == EndpointMissing {
		observation.EndpointVerdict = ProbeMissing
	}
	observation.Health = HealthUnknown
	observation.Reason = reason
	observation.StaleSince = nil
	observation.NextEscalation = nil
	observation.NextPauseResurface = nil
	observation.Escalation = 0
	observation.DemandDeepInspection = false
	if observation.PendingEvent == nil {
		event := taskEvent(observation.TaskID, reason, detail)
		observation.PendingEvent = &event
	}
	return observation
}

// launchingObservation records a present pane whose agent has not registered
// yet on a task that has never been observed alive. It carries no pending
// event: launch-in-progress is not a decision and must not wake the CFO.
func launchingObservation(observation Observation, now time.Time) Observation {
	observation.LastObserved = now
	observation.EndpointVerdict = ProbePresent
	observation.Health = HealthLaunching
	observation.Reason = None
	observation.StaleSince = nil
	observation.NextEscalation = nil
	observation.NextPauseResurface = nil
	observation.Escalation = 0
	observation.DemandDeepInspection = false
	observation.PendingEvent = nil
	return observation
}

func invalidRecordObservation(meta state.TaskMeta, prior Observation, now time.Time) Observation {
	observation := prior
	observation.Schema = Schema
	observation.TaskID = meta.ID
	observation.Endpoint = endpointString(meta)
	observation.EndpointVerdict = ProbeUnknown
	observation.LastObserved = now
	observation.Health = HealthUnknown
	observation.Reason = InvalidRecord
	observation.StaleSince = nil
	observation.NextEscalation = nil
	observation.NextPauseResurface = nil
	observation.Escalation = 0
	observation.DemandDeepInspection = false
	if observation.PendingEvent == nil {
		event := taskEvent(meta.ID, InvalidRecord, "")
		observation.PendingEvent = &event
	}
	return observation
}

func validSample(meta state.TaskMeta, sample EndpointSample) bool {
	if sample.Endpoint.Target.Session != meta.HerdrSession || sample.Endpoint.Target.Pane != meta.HerdrPaneID {
		return false
	}
	if sample.Endpoint.WorkspaceID != meta.HerdrWorkspaceID || sample.Endpoint.TabID != meta.HerdrTabID || sample.Endpoint.PaneID != meta.HerdrPaneID {
		return false
	}
	if sample.TabLabel != "gb-"+meta.ID || sample.Agent != herdr.AgentAlive {
		return false
	}
	return len(sample.Capture) > 0
}

func (s Service) declaredPaused(id string) bool {
	lines, err := state.TailStatus(s.StateDir, id, 200)
	if err != nil {
		return false
	}
	for i := len(lines) - 1; i >= 0; i-- {
		verb, _, ok := crewstate.ParseStatusLine(lines[i])
		if !ok {
			continue
		}
		return verb == "paused"
	}
	return false
}

// launchDeadline bounds the launch-in-progress grace to the task's spawn time
// plus a launch budget. A task whose agent registers and dies before the
// monitor ever observes it alive would otherwise be "launching" forever; after
// the budget it is classified as a dead endpoint and wakes normally.
func (s Service) launchDeadline(meta state.TaskMeta) time.Time {
	info, err := os.Stat(filepath.Join(s.StateDir, meta.ID+".meta"))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime().Add(s.launchGrace())
}

func (s Service) busyReference(id string) time.Time {
	var latest time.Time
	for _, path := range []string{filepath.Join(s.StateDir, id+".meta"), filepath.Join(s.StateDir, id+".turn-ended")} {
		if info, err := os.Stat(path); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}

func (s Service) clearPending(event Event) error {
	if event.TaskID != "" {
		observation, err := ReadObservation(s.StateDir, event.TaskID)
		if err == nil && sameEvent(observation.PendingEvent, event) {
			observation.PendingEvent = nil
			return WriteObservation(s.StateDir, observation)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			// Corrupt task records preserve their bytes. Their pending event is
			// stored in the heartbeat record below.
		}
	}
	heartbeat, err := ReadHeartbeat(s.StateDir)
	if err != nil {
		return err
	}
	if !sameEvent(heartbeat.PendingEvent, event) {
		return fmt.Errorf("monitor: event %s/%s is not pending", event.Kind, event.Key)
	}
	heartbeat.PendingEvent = nil
	return WriteHeartbeat(s.StateDir, heartbeat)
}

func hasUnsurfacedActionable(observations []Observation) bool {
	for _, observation := range observations {
		if observation.PendingEvent != nil {
			continue
		}
		// Only a genuinely unknown endpoint (a goblin whose pane vanished) is
		// worth a heartbeat re-surface. Stale goblins are doing their job, and
		// an erroring or awaiting-answer goblin already woke once with its
		// decision; re-surfacing it every cycle would be noise.
		if observation.Health == HealthUnknown {
			return true
		}
	}
	return false
}

func taskEvent(id string, reason Reason, detail string) Event {
	text := string(reason)
	if detail != "" {
		text += ": " + detail
	}
	return Event{Source: TaskEvent, TaskID: id, Kind: "stale", Key: id, Detail: text}
}

func sameEvent(pending *Event, event Event) bool {
	return pending != nil &&
		pending.Source == event.Source &&
		pending.TaskID == event.TaskID &&
		pending.Kind == event.Kind &&
		pending.Key == event.Key
}

func cloneEvent(event *Event) *Event {
	if event == nil {
		return nil
	}
	copy := *event
	return &copy
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}

func endpointString(meta state.TaskMeta) string {
	return herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}.String()
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Service) stallAfter() time.Duration {
	if s.StallAfter > 0 {
		return s.StallAfter
	}
	return 10 * time.Minute
}

func (s Service) launchGrace() time.Duration {
	if s.LaunchGrace > 0 {
		return s.LaunchGrace
	}
	return 5 * time.Minute
}

// statusStamp is the status log's size and mtime as a cheap liveness signal.
// A goblin writing its status (no-mistakes steps, cfo notify, or a plain
// progress line) advances this stamp, which is evidence it is working even
// when the pane text is quiet.
func (s Service) statusStamp(id string) string {
	info, err := os.Stat(filepath.Join(s.StateDir, id+".status"))
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

func (s Service) staleEscalateAfter() time.Duration {
	if s.StaleEscalateAfter > 0 {
		return s.StaleEscalateAfter
	}
	return 4 * time.Minute
}

func (s Service) busyTurnMax() time.Duration {
	if s.BusyTurnMax > 0 {
		return s.BusyTurnMax
	}
	return time.Hour
}

func (s Service) pauseResurfaceAfter() time.Duration {
	if s.PauseResurfaceAfter > 0 {
		return s.PauseResurfaceAfter
	}
	return time.Hour
}

func (s Service) demandInspectionAfter() int {
	if s.DemandInspectionAfter > 0 {
		return s.DemandInspectionAfter
	}
	return 3
}

func (s Service) heartbeat() time.Duration {
	if s.Heartbeat > 0 {
		return s.Heartbeat
	}
	return 10 * time.Minute
}

func (s Service) heartbeatMax() time.Duration {
	if s.HeartbeatMax >= s.heartbeat() {
		return s.HeartbeatMax
	}
	return s.heartbeat()
}

func (s Service) backoff(streak int) time.Duration {
	interval := s.heartbeat()
	for range streak {
		if interval >= s.heartbeatMax()/2 {
			return s.heartbeatMax()
		}
		interval *= 2
	}
	return interval
}

func (s Service) heartbeatDue(heartbeat Heartbeat, now time.Time) bool {
	return !heartbeat.NextDue.IsZero() && !now.Before(heartbeat.NextDue)
}

func (s Service) freshHeartbeat(now time.Time) Heartbeat {
	return Heartbeat{LastHeartbeat: now, NextDue: now.Add(s.heartbeat())}
}
