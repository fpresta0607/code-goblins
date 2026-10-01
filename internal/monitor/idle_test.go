package monitor

import (
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// idleService is a monitor over one goblin whose turn has ended while a
// process it started sits idle, the shape that held a Codex goblin quiet for
// the whole busy budget on 2026-09-30.
func idleService(t *testing.T, now *time.Time) (Service, *fakeProber, *fakeProgress, state.TaskMeta) {
	t.Helper()
	service, probe, progress, meta := progressService(t, now)
	service.StallAfter = 10 * time.Minute
	service.BusyTurnMax = 10 * time.Minute
	progress.sample = ProgressSample{Jobs: []string{"pwsh.exe (pid 51)"}, JobCPU: time.Second}
	return service, probe, progress, meta
}

// scanIdle scans once a minute for minutes, publishing each wake and acking
// it as the CFO's drain does, and returns every wake raised.
func scanIdle(t *testing.T, service Service, probe *fakeProber, status, pane string, now *time.Time, minutes int) []Event {
	t.Helper()
	return scanIdleAcking(t, service, probe, status, pane, now, minutes, true)
}

func scanIdleAcking(t *testing.T, service Service, probe *fakeProber, status, pane string, now *time.Time, minutes int, ack bool) []Event {
	t.Helper()
	var wakes []Event
	for range minutes {
		r := scanPane(t, service, probe, status, pane, now, time.Minute)
		if r.Event == nil {
			continue
		}
		wakes = append(wakes, *r.Event)
		record, err := service.Publish(*r.Event)
		if err != nil {
			t.Fatal(err)
		}
		if ack {
			if err := wake.AckThrough(service.StateDir, record.Seq); err != nil {
				t.Fatal(err)
			}
		}
	}
	return wakes
}

// A goblin sitting at its prompt with nothing running, nothing asked and
// nothing reported wakes the CFO once after three minutes, with the end of its
// screen and what to do next, whatever held its awaiting_answer wake back.
func TestAGoblinIdleAtItsPromptWakesOnceAfterThreeMinutes(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, _ := idleService(t, &now)
	pane := "● Tests pass. Next I will open the pull request."

	early := scanIdle(t, service, probe, herdr.AgentDone, pane, &now, 3)
	if len(early) != 0 {
		t.Fatalf("woke within three minutes: %+v", early)
	}
	wakes := scanIdle(t, service, probe, herdr.AgentDone, pane, &now, 17)
	if len(wakes) != 1 {
		t.Fatalf("an idle goblin raised %d wakes over the next 17 minutes, past the busy budget, want exactly one: %+v", len(wakes), wakes)
	}
	for _, want := range []string{string(GoblinIdle) + ":", "next:", "cfo send g1", "Tests pass. Next I will open the pull request.", "pwsh.exe (pid 51)"} {
		if !strings.Contains(wakes[0].Detail, want) {
			t.Errorf("wake detail %q lacks %q", wakes[0].Detail, want)
		}
	}
	if wakes[0].Kind != "stale" || wakes[0].Key != "g1" {
		t.Errorf("wake = %+v, want a stale wake keyed by the goblin", wakes[0])
	}
}

// A scan reads a goblin's progress once: the idle clock reuses the reading
// the ended turn's own-work check just made rather than reading the
// transcript and every process again in the loop that stamps the heartbeat.
func TestAScanReadsAnIdleGoblinsProgressOnce(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, progress, _ := idleService(t, &now)

	for scan := 1; scan <= 12; scan++ {
		scanPane(t, service, probe, herdr.AgentDone, "❯", &now, 15*time.Second)
		if progress.calls != scan {
			t.Fatalf("after %d scans progress was read %d times, want once a scan", scan, progress.calls)
		}
	}
}

// Between turns reads the same: an idle Herdr agent at its prompt wakes as
// goblin_idle before the ten-minute stall would.
func TestAnIdleAgentBetweenTurnsWakesAsGoblinIdleFirst(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, _ := idleService(t, &now)

	wakes := scanIdle(t, service, probe, herdr.AgentIdle, "❯", &now, 12)
	if len(wakes) != 1 || !strings.HasPrefix(wakes[0].Detail, string(GoblinIdle)+":") {
		t.Fatalf("wakes = %+v, want one goblin_idle wake", wakes)
	}
}

// Each thing that says the goblin is not idle with work left keeps it quiet:
// its pane shows work running, its own processes use the processor, it is
// parked on a status it reported, or it asked a question still unanswered.
func TestAGoblinThatIsNotIdleWithWorkLeftNeverWakesAsGoblinIdle(t *testing.T) {
	for name, arrange := range map[string]func(t *testing.T, service Service, progress *fakeProgress) string{
		"pane shows a running tool": func(*testing.T, Service, *fakeProgress) string { return runningToolPane },
		"its own job uses the processor": func(_ *testing.T, _ Service, progress *fakeProgress) string {
			progress.cpuStep = 20 * time.Second
			return "❯"
		},
		"it waits on ci": func(t *testing.T, service Service, _ *fakeProgress) string {
			appendStatus(t, service.StateDir, "g1", "waiting on ci: PR 12 checks running")
			return "❯"
		},
		"it reported done": func(t *testing.T, service Service, _ *fakeProgress) string {
			appendStatus(t, service.StateDir, "g1", "done: PR https://github.com/o/r/pull/12")
			return "❯"
		},
		"its question is unanswered": func(t *testing.T, service Service, _ *fakeProgress) string {
			if _, err := wake.Append(service.StateDir, "notify", "g1", "blocked: which layout? options: Grid | List"); err != nil {
				t.Fatal(err)
			}
			return "❯"
		},
	} {
		t.Run(name, func(t *testing.T) {
			now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
			service, probe, progress, _ := idleService(t, &now)
			pane := arrange(t, service, progress)

			for _, event := range scanIdle(t, service, probe, herdr.AgentDone, pane, &now, 10) {
				if strings.HasPrefix(event.Detail, string(GoblinIdle)+":") {
					t.Fatalf("a goblin that is not idle with work left woke as idle: %+v", event)
				}
			}
		})
	}
}

// A notify restarts the clock: three minutes are counted from the goblin's
// last report, not from when its turn ended.
func TestANotifyRestartsTheIdleClock(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, _ := idleService(t, &now)

	scanIdle(t, service, probe, herdr.AgentDone, "❯", &now, 2)
	appendStatus(t, service.StateDir, "g1", "working: opening the pull request")
	if wakes := scanIdle(t, service, probe, herdr.AgentDone, "❯", &now, 2); len(wakes) != 0 {
		t.Fatalf("woke %+v two minutes after the goblin reported", wakes)
	}
	if wakes := scanIdle(t, service, probe, herdr.AgentDone, "❯", &now, 3); len(wakes) != 1 {
		t.Fatalf("wakes = %+v, want one once three minutes passed since the report", wakes)
	}
}

// A goblin woken as idle that works and then sits idle again is a new
// episode, but the same goblin wakes as idle at most once per fifteen
// minutes: the second wake waits for the gap instead of being dropped.
func TestASecondIdleEpisodeWaitsForTheGap(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, _ := idleService(t, &now)
	service.BusyTurnMax = time.Hour

	first := scanIdle(t, service, probe, herdr.AgentDone, "❯", &now, 4)
	if len(first) != 1 {
		t.Fatalf("first episode wakes = %+v, want one", first)
	}
	wokeAt := now
	scanIdle(t, service, probe, herdr.AgentWorking, "✽ Reticulating… (3s · esc to interrupt)", &now, 1)
	var second []Event
	for range 20 {
		if wakes := scanIdle(t, service, probe, herdr.AgentDone, "❯", &now, 1); len(wakes) > 0 {
			second = wakes
			break
		}
	}
	if len(second) != 1 || !strings.HasPrefix(second[0].Detail, string(GoblinIdle)+":") {
		t.Fatalf("second episode wakes = %+v, want one goblin_idle wake", second)
	}
	if gap := now.Sub(wokeAt); gap < 15*time.Minute {
		t.Fatalf("second idle wake came %s after the first, want at least 15m", gap)
	}
}

// An idle wake is a question the CFO owes an answer to until it is acked, as
// a turn that ended at its prompt is: the ledger re-asks it.
func TestAnUnackedIdleWakeIsReAsked(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, _ := idleService(t, &now)

	wakes := scanIdleAcking(t, service, probe, herdr.AgentDone, "❯", &now, 15, false)
	if len(wakes) != 2 || !strings.HasPrefix(wakes[0].Detail, string(GoblinIdle)+":") || !strings.HasPrefix(wakes[1].Detail, string(AwaitingDecision)+":") {
		t.Fatalf("wakes = %+v, want the idle wake then one re-ask", wakes)
	}
}

func appendStatus(t *testing.T, stateDir, id, line string) {
	t.Helper()
	if err := state.AppendStatus(stateDir, id, line); err != nil {
		t.Fatal(err)
	}
}

// Whichever wake reports a goblin stopped at its prompt, the CFO reads what it
// stopped on: the wake for a turn that ended carries the end of its screen too.
func TestAnEndedTurnWakeCarriesTheEndOfItsScreen(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, _ := progressService(t, &now)

	rule := strings.Repeat("─", 40)
	r := scanPane(t, service, probe, herdr.AgentDone, "● Tests pass; opening the pull request next.\n"+rule+"\n>\n"+rule, &now, time.Minute)

	if r.Event == nil || !strings.HasPrefix(r.Event.Detail, string(AwaitingAnswer)+":") || !strings.Contains(r.Event.Detail, "its screen ends: ● Tests pass; opening the pull request next. | > | ● Tests pass") {
		t.Fatalf("event = %+v, want an awaiting_answer wake carrying the end of the screen without its rules", r.Event)
	}
	if strings.Contains(r.Event.Detail, "──") {
		t.Errorf("wake %q carries the screen's rules, which say nothing", r.Event.Detail)
	}
}
