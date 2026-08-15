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
		return unknownObservation(observation, EndpointUnknown, detail, now)
	}

	observation.EndpointVerdict = ProbePresent
	digest := fmt.Sprintf("%x", sha256.Sum256(sample.Capture))
	if observation.Digest == "" || observation.Digest != digest {
		observation.Digest = digest
		observation.LastSeen = now
		observation.LastProgress = now
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
		if s.busyReference(meta.ID).Add(s.busyTurnMax()).After(now) {
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
	case herdr.BusyIdle:
		if s.declaredPaused(meta.ID) {
			return s.pauseObservation(observation, now)
		}
		return s.staleObservation(observation, UnchangedIdle, now)
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
		event := taskEvent(observation.TaskID, reason, "")
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
		detail := string(reason)
		if observation.DemandDeepInspection {
			detail += " demand-deep-inspection"
		}
		event := Event{Source: TaskEvent, TaskID: observation.TaskID, Kind: "stale", Key: observation.TaskID, Detail: detail}
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
	if sample.TabLabel != "fm-"+meta.ID || sample.Agent != herdr.AgentAlive {
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
		if observation.Health == HealthStale || observation.Health == HealthUnknown {
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
	return pending != nil && *pending == event
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
