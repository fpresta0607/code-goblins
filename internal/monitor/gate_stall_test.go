package monitor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/pipeline/pipelinetest"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// gateAt is a gate whose reading is taken at the monitor's clock, so the
// evidence it carries is as fresh at each scan as a live gate's would be.
type gateAt func() GateSample

func (g gateAt) InspectGate(context.Context, state.TaskMeta) (GateSample, error) {
	return g(), nil
}

// gateStallService is a monitor over one working pi goblin with the
// production busy budget of an hour, whose gate reads sample at its clock.
func gateStallService(t *testing.T, now *time.Time, sample func(now time.Time) GateSample) (Service, *fakeProber, state.TaskMeta) {
	t.Helper()
	stateDir := t.TempDir()
	meta := metaFor("g1")
	meta.Harness = "pi"
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{}}
	service := testService(stateDir, probe, now)
	service.BusyTurnMax = time.Hour
	service.Gate = gateAt(func() GateSample { return sample(*now) })
	return service, probe, meta
}

// scanGateFor scans a working goblin every five minutes for span and returns
// the first wake.
func scanGateFor(t *testing.T, service Service, probe *fakeProber, meta state.TaskMeta, now *time.Time, span time.Duration) *Event {
	t.Helper()
	scanWorking(t, service, probe, meta, now, 0)
	for elapsed := time.Duration(0); elapsed < span; elapsed += 5 * time.Minute {
		if r := scanWorking(t, service, probe, meta, now, 5*time.Minute); r.Event != nil {
			return r.Event
		}
	}
	return nil
}

// On 2026-11-07 the monitor raised busy_turn_over_age four times in one
// evening for gate steps that were working, each because the step had been
// active past an hour, while the goblin's own log stayed quiet as a gate
// leaves it until a step ends (#2165). A step that shows progress of its own,
// its agent's output or the heavy line moving for its run, is not stalled
// whatever its age.
func TestAGateStepPastTheHourIsNotStalledWhileItsOwnEvidenceMoves(t *testing.T) {
	for name, sample := range map[string]func(now time.Time) GateSample{
		"a review whose pi agent produced output a second before the reading": func(now time.Time) GateSample {
			return GateSample{Active: true, RunID: "01M7REVIEW", Step: "review", ActiveFor: 61 * time.Minute, RoundFor: 61 * time.Minute, LastActivity: "1s ago: pi producing output", MovedAt: now.Add(-time.Second)}
		},
		"a test step whose run holds the heavy line at package 52": func(now time.Time) GateSample {
			return GateSample{Active: true, RunID: "01M7TEST", Step: "test", ActiveFor: 65 * time.Minute, RoundFor: 65 * time.Minute, LastActivity: "quiet 1h4m ago: log: running tests: goblins gate test", MovedAt: now, Line: "its run has held a turn in the heavy line for 50m0s (go test: 52 package(s) done), within its 1h30m0s budget", IsLineRead: true}
		},
		"a test step quiet for 30 minutes while the heavy line moved": func(now time.Time) GateSample {
			return GateSample{Active: true, RunID: "01M7LINE", Step: "test", ActiveFor: 65 * time.Minute, RoundFor: 65 * time.Minute, LastActivity: "quiet 30m0s ago: log: running tests: goblins gate test", MovedAt: now.Add(-2 * time.Minute), Line: "its run waits in the heavy line, which last moved 2m0s ago", IsLineRead: true}
		},
		"a ci step producing output on its second fix round": func(now time.Time) GateSample {
			return GateSample{Active: true, RunID: "01M7CI", Step: "ci", ActiveFor: 69 * time.Minute, RoundFor: 69 * time.Minute, LastActivity: "1s ago: pi producing output", MovedAt: now.Add(-time.Second)}
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			now := time.Date(2026, 11, 7, 21, 0, 0, 0, time.UTC)
			service, probe, meta := gateStallService(t, &now, sample)

			// Act
			woke := scanGateFor(t, service, probe, meta, &now, 90*time.Minute)

			// Assert
			if woke != nil {
				t.Fatalf("a working gate step woke the CEO at %s: %+v", now.Format(time.Kitchen), woke)
			}
		})
	}
}

// A step's round is given the time nine in ten of its rounds on the task's
// harness took on this machine, a pi review up to 116 minutes, and only a
// step with none on record falls back to the busy budget (#2165).
func TestAGateStepIsGivenTheTimeItsRoundsOnTheHarnessTakeOnThisMachine(t *testing.T) {
	for name, test := range map[string]struct {
		isMeasured bool
		wantWake   bool
	}{
		"its rounds on pi are on record":  {isMeasured: true, wantWake: false},
		"no round of it is on the record": {isMeasured: false, wantWake: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange: a pi review 100 minutes into its round whose agent
			// has shown nothing for 70 minutes.
			now := time.Date(2026, 11, 7, 21, 0, 0, 0, time.UTC)
			started := now
			service, probe, meta := gateStallService(t, &now, func(now time.Time) GateSample {
				round := 40*time.Minute + now.Sub(started)
				sample := GateSample{Active: true, RunID: "01M7REVIEW", Step: "review", ActiveFor: round, RoundFor: round, LastActivity: "quiet 50m0s ago: pi thinking", MovedAt: started.Add(-10 * time.Minute), IsLineRead: true}
				if test.isMeasured {
					sample.Allowance, sample.IsMeasured = 116*time.Minute, true
				}
				return sample
			})

			// Act
			woke := scanGateFor(t, service, probe, meta, &now, time.Hour)

			// Assert
			if (woke != nil) != test.wantWake {
				t.Fatalf("wake = %+v, want a wake %v", woke, test.wantWake)
			}
			if test.wantWake && !strings.Contains(woke.Detail, "the default 1h0m0s") {
				t.Errorf("wake %q does not say it gave the step the default", woke.Detail)
			}
		})
	}
}

// A stall wake says what it read of the step and what it could not read, so
// the CEO does not have to look again to learn it (#2165).
func TestAStalledGateStepWakeNamesWhatItReadAndWhatItCouldNot(t *testing.T) {
	// Arrange
	now := time.Date(2026, 11, 7, 21, 0, 0, 0, time.UTC)
	service, probe, meta := gateStallService(t, &now, func(now time.Time) GateSample {
		return GateSample{Active: true, RunID: "01M7TEST", Step: "test", ActiveFor: 3 * time.Hour, RoundFor: 3 * time.Hour, LastActivity: "quiet 2h0m ago: log: running tests: goblins gate test", MovedAt: now.Add(-2 * time.Hour),
			Line: "its run has held a turn in the heavy line for 2h0m0s (go test: 52 package(s) done), with no budget left to it", IsLineRead: true,
			Unread: []string{"how long its rounds take on this machine (pipeline: state query timed out)"}}
	})

	// Act
	woke := scanGateFor(t, service, probe, meta, &now, time.Hour)

	// Assert
	if woke == nil || !strings.HasPrefix(woke.Detail, string(BusyTurnOverAge)+": ") {
		t.Fatalf("wake = %+v, want busy_turn_over_age for a stalled step", woke)
	}
	for _, want := range []string{"gate step test round active for 3h", "the default 1h0m0s", "nothing of it has moved for 2h", "last activity: quiet 2h0m ago", "go test: 52 package(s) done", "not read: how long its rounds take on this machine (pipeline: state query timed out)"} {
		if !strings.Contains(woke.Detail, want) {
			t.Errorf("wake %q lacks %q", woke.Detail, want)
		}
	}
}

func TestParseGateStatusReadsTheRunAndItsRoundTimer(t *testing.T) {
	// Arrange
	status := "run:\n  id: \"01M7NCG1RFG6GPHEQA7J40ZD06\"\n  status: running\n  active_steps[1]{step,status,active_for,round_active_for,last_activity,agent_pid,round}:\n    ci,fixing,1h9m,12m3s,\"1s ago: pi producing output\",\"48204\",fix 2\n"

	// Act
	got := parseGateStatus(status, GateSample{})

	// Assert
	if got.RunID != "01M7NCG1RFG6GPHEQA7J40ZD06" || got.ActiveFor != 69*time.Minute || got.RoundFor != 12*time.Minute+3*time.Second {
		t.Fatalf("parsed = %+v, want run 01M7NCG1RFG6GPHEQA7J40ZD06 active 1h9m in a round of 12m3s", got)
	}
}

func TestActivityAgeReadsTheGatesCompactDurations(t *testing.T) {
	for activity, want := range map[string]time.Duration{
		"1s ago: pi producing output":                    time.Second,
		"quiet 22m30s ago: log: running tests":           22*time.Minute + 30*time.Second,
		"quiet 1h4m ago: log: go test ./...":             time.Hour + 4*time.Minute,
		"quiet 2d3h ago: log: no CI checks reported yet": 51 * time.Hour,
		"unknown": -1,
		"":        -1,
		"quiet soon ago: a summary no-mistakes never wrote": -1,
	} {
		got, ok := activityAge(activity)
		if (want < 0 && ok) || (want >= 0 && (!ok || got != want)) {
			t.Errorf("activityAge(%q) = %v, %v; want %v", activity, got, ok, want)
		}
	}
}

func TestLineMovedIsTheHeavyLinesOwnProgressForTheRun(t *testing.T) {
	now := time.Date(2026, 11, 7, 21, 0, 0, 0, time.UTC)
	const who = `code-goblins at 514b8830, fast level, in C:\Users\me\.no-mistakes\worktrees\3946e592fa2c\01M7RUN`
	passed := now.Add(-4 * time.Minute)
	for name, test := range map[string]struct {
		holding, waiting []verify.Standing
		want             time.Time
		wantWords        string
	}{
		"its run holds a turn within its budget": {
			holding:   []verify.Standing{{Who: who, Since: now.Add(-50 * time.Minute), Budget: 90 * time.Minute, Now: "go test: 52 package(s) done"}},
			want:      now,
			wantWords: "go test: 52 package(s) done",
		},
		"its run holds a turn past its budget": {
			holding:   []verify.Standing{{Who: who, Since: now.Add(-2 * time.Hour), Budget: 90 * time.Minute}},
			want:      now.Add(-2 * time.Hour),
			wantWords: "no budget left",
		},
		"its run waits while the turn passes": {
			holding:   []verify.Standing{{Who: "another goblin's suite", Since: passed, Budget: time.Hour}},
			waiting:   []verify.Standing{{Who: who, Since: now.Add(-30 * time.Minute)}},
			want:      passed,
			wantWords: "waits in the heavy line, which last moved 4m0s ago",
		},
		"only another run stands in the line": {
			holding: []verify.Standing{{Who: "another goblin's suite", Since: passed, Budget: time.Hour}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			moved, words := lineMoved(test.holding, test.waiting, "01M7RUN", now)

			// Assert
			if !moved.Equal(test.want) || !strings.Contains(words, test.wantWords) || (test.wantWords == "") != (words == "") {
				t.Fatalf("lineMoved = %v, %q; want %v and words with %q", moved, words, test.want, test.wantWords)
			}
		})
	}
}

// The prober reads a step's last activity, the heavy line and the step's
// rounds on the task's harness from the gate's own database.
func TestStepEvidenceReadsTheStepsActivityTheHeavyLineAndItsMeasuredRounds(t *testing.T) {
	// Arrange
	root := t.TempDir()
	result, err := (execx.OSRunner{}).Run(context.Background(), execx.Request{Name: "sqlite3", Args: []string{filepath.Join(root, "state.sqlite"), `CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT);
CREATE TABLE step_rounds(step_result_id TEXT,round INTEGER,duration_ms INTEGER);
CREATE TABLE agent_invocations(run_id TEXT,step_name TEXT,agent TEXT);
INSERT INTO step_results VALUES('p1','p','review'),('c1','c','review');
INSERT INTO step_rounds VALUES('p1',1,6960000),('c1',1,600000);
INSERT INTO agent_invocations VALUES('p','review','pi'),('c','review','claude');`}})
	if err != nil {
		t.Skipf("sqlite3 unavailable: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatal(string(result.Stderr))
	}
	now := time.Date(2026, 11, 7, 21, 0, 0, 0, time.UTC)
	const who = `code-goblins at 514b8830, fast level, in C:\gate\worktrees\repo\01M7RUN`
	prober := ExecGateProber{
		Commands: pipelinetest.FixtureCommands{Runner: execx.OSRunner{}},
		Line: func() ([]verify.Standing, []verify.Standing, error) {
			return []verify.Standing{{Who: who, Since: now.Add(-time.Hour), Budget: 90 * time.Minute}}, nil, nil
		},
	}
	sample := GateSample{Active: true, RunID: "01M7RUN", Step: "review", LastActivity: "quiet 40m0s ago: pi thinking"}

	// Act
	got := prober.stepEvidence(context.Background(), root, "pi", sample, now)
	unread := (ExecGateProber{Line: func() ([]verify.Standing, []verify.Standing, error) { return nil, nil, errors.New("access denied") }}).stepEvidence(context.Background(), root, "pi", GateSample{Active: true, RunID: "01M7RUN", Step: "review", LastActivity: "unknown"}, now)

	// Assert
	if !got.MovedAt.Equal(now) || !got.IsMeasured || got.Allowance != 116*time.Minute || !got.IsLineRead || got.Line == "" || len(got.Unread) != 0 {
		t.Fatalf("stepEvidence = %+v, want the heavy line's turn as its movement and pi's 116m review rounds as its allowance", got)
	}
	want := []string{"when the step last showed activity", "the heavy line (access denied)", "how long its rounds take on this machine, which no reader was given"}
	if !unread.MovedAt.IsZero() || unread.IsMeasured || unread.IsLineRead || strings.Join(unread.Unread, "|") != strings.Join(want, "|") {
		t.Fatalf("stepEvidence with nothing readable = %+v, want unread %q", unread, want)
	}
}
