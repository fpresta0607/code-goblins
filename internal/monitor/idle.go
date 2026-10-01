package monitor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// idleAtPrompt wakes the CFO once for a goblin that has sat at its prompt for
// the idle window with nothing running, nothing asked and nothing reported:
// a goblin with work left that stopped. Every harness is read the same way,
// from what the supervisor sees itself - the screen, the status log, the wake
// queue, and the transcript and processes of the harness - because a Codex
// goblin runs without its hooks, and on 2026-09-30 one sat idle for about an
// hour while a process it had left running held the other stale wakes back.
//
// It runs after classification. A goblin already woken this stretch, as
// awaiting its answer or as idle, is left alone, and so is one the scan could
// not see: the clock neither runs out nor restarts on a scan that saw nothing.
// The same goblin wakes as idle at most once per idle wake gap; a second idle
// stretch inside it waits for the gap rather than being dropped.
func (s Service) idleAtPrompt(ctx context.Context, meta state.TaskMeta, sample EndpointSample, observation Observation, led ledger, now time.Time) Observation {
	if sample.Verdict != ProbePresent || observation.EndpointVerdict != ProbePresent {
		return observation
	}
	if observation.Health == HealthStale && observation.Reason == GoblinIdle {
		return observation
	}
	atPrompt := sample.Status == herdr.AgentDone || sample.Status == herdr.AgentIdle || sample.InteractiveReady && sample.Status != herdr.AgentWorking && sample.Status != herdr.AgentBlocked
	_, running := paneRunning(sample)
	quiet := observation.Health == HealthIdle || observation.Health == HealthBusy
	if !atPrompt || running || !quiet || observation.Reason != None || s.heldByVerb(meta.ID, observation) || led.unanswered(meta.ID) {
		observation.PromptSince, observation.PromptStatus = nil, ""
		return observation
	}

	// A report restarts the clock: the goblin said something since.
	since, stamp := now, s.statusStamp(meta.ID)
	if observation.PromptSince != nil && observation.PromptStatus == stamp {
		since = *observation.PromptSince
	}
	observation.PromptStatus = stamp
	var jobs []string
	judged := true
	if s.Progress != nil && now.Sub(since) >= s.idleAfter()-jobSampleInterval {
		var err error
		if observation.ProgressReadAt != nil && observation.ProgressReadAt.Equal(now) {
			jobs = observation.Jobs
		} else {
			jobs, _, err = s.sampleProgress(ctx, meta, sample, &observation, since, now)
		}
		if observation.EvidenceAt != nil && observation.EvidenceAt.After(since) {
			since = *observation.EvidenceAt
		}
		// Its own processes' processor use is judged over a reading
		// interval, so an idle goblin with processes running waits one.
		judged = err == nil && (len(jobs) == 0 || observation.JobSampledSince != nil && now.Sub(*observation.JobSampledSince) >= jobSampleInterval)
	}
	observation.PromptSince = timePointer(since)
	if now.Sub(since) < s.idleAfter() || !judged || observation.PendingEvent != nil {
		return observation
	}
	if observation.IdleWokeAt != nil && now.Sub(*observation.IdleWokeAt) < s.idleWakeGap() {
		return observation
	}

	observation.Health = HealthStale
	observation.Reason = GoblinIdle
	observation.StaleSince = timePointer(now)
	observation.NextEscalation = timePointer(now.Add(time.Hour))
	observation.NextPauseResurface = nil
	observation.Escalation = 0
	observation.DemandDeepInspection = true
	observation.IdleWokeAt = timePointer(now)
	event := taskEvent(meta.ID, GoblinIdle, idleDetail(meta.ID, sample, jobs, now.Sub(since), since))
	observation.PendingEvent = &event
	return observation
}

// heldByVerb reports whether the goblin's latest report, one it has not been
// seen working past, parks, pauses or ends it: it waits on something, asked
// the CFO, or reported its work finished, so it is not idle with work left.
func (s Service) heldByVerb(id string, observation Observation) bool {
	verb, line, ok := s.latestStatusVerb(id)
	return ok && line > observation.ConsumedVerbLine && (verb == "paused" || parkedDecisionVerb(verb) || terminalVerb(verb))
}

// idleDetail is the goblin_idle wake: how long it has sat, what to do next,
// its own processes left running, and the end of its screen.
func idleDetail(id string, sample EndpointSample, jobs []string, idle time.Duration, since time.Time) string {
	detail := fmt.Sprintf("at its prompt for %s with nothing running, no question open and no report since %s UTC; next: steer it with cfo send %s \"<what to do next>\", or retire it if its work is done",
		idle.Round(time.Minute), since.UTC().Format("15:04"), id)
	if len(jobs) > 0 {
		detail += "; its own processes sit idle: " + strings.Join(jobs, ", ")
	}
	if tail := screenTail(sample.Capture, idleScreenRows); tail != "" {
		detail += "; its screen ends: " + tail
	}
	return detail
}

// idleScreenRows is how many of the screen's last rows a goblin_idle wake
// carries.
const idleScreenRows = 6

// screenTail joins the last rows of a capture that say something on one line,
// since a wake is one line, and bounds it. A blank row and a rule drawn across
// the screen say nothing.
func screenTail(capture []byte, rows int) string {
	var tail []string
	lines := strings.Split(strings.ReplaceAll(string(capture), "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0 && len(tail) < rows; i-- {
		if row := strings.TrimSpace(lines[i]); strings.Trim(row, "─━═ ") != "" {
			tail = append([]string{row}, tail...)
		}
	}
	return bounded(strings.Join(tail, " | "), 700)
}
