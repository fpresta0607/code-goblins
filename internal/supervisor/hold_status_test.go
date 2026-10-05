package supervisor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestSnapshotAcknowledgementDoesNotResumeHeldWork(t *testing.T) {
	for _, test := range []struct {
		name, sessionPhase, laterReport, wantPhase string
		health                                     monitor.Health
		hasMovingJobs, hasNewDecision              bool
	}{
		{name: "parked without native hooks", health: monitor.HealthParked, wantPhase: "blocked"},
		{name: "idle native session", sessionPhase: "settled", health: monitor.HealthIdle, wantPhase: "blocked"},
		{name: "ended native session", sessionPhase: "ended", health: monitor.HealthUnknown, wantPhase: "blocked"},
		{name: "active turn cannot erase deliberate hold", sessionPhase: "active", health: monitor.HealthBusy, wantPhase: "blocked"},
		{name: "owned job progress remains separate", sessionPhase: "settled", health: monitor.HealthIdle, hasMovingJobs: true, wantPhase: "blocked"},
		{name: "explicit resumed work", sessionPhase: "active", health: monitor.HealthBusy, laterReport: "working: resumed repair", wantPhase: "working"},
		{name: "explicit dependency wait", health: monitor.HealthParked, laterReport: "waiting on ci: revised checks", wantPhase: "waiting"},
		{name: "new unanswered decision", health: monitor.HealthParked, laterReport: "working: resumed repair", hasNewDecision: true, wantPhase: "blocked"},
		{name: "per PR completion remains observable", sessionPhase: "settled", health: monitor.HealthIdle, laterReport: "done: PR https://github.com/o/r/pull/9", wantPhase: "idle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: notify writes the report before the wake, then the CFO acknowledges delivery.
			store, h := testStore(t)
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.Backend, meta.Mode = "native", "direct-PR"
			meta.HerdrSession, meta.HerdrPaneID = "", ""
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			if test.sessionPhase != "" {
				store.db.TaskSessions[meta.ID] = "codex/current"
				store.db.Sessions["codex/current"] = Session{ID: "codex/current", TaskID: meta.ID, Role: "goblin", Generation: meta.SpawnGen, Phase: test.sessionPhase, UpdatedAt: now.Add(-time.Minute)}
			}
			store.db.Tasks[meta.ID] = Evaluation{Generation: meta.SpawnGen, Phase: "review", Reason: "Source review pending"}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			hold := "blocked: Provider reserve hold; no new work"
			lines := fmt.Sprintf("%s working: previous checks\n%s %s\n", now.Add(-20*time.Second).Format(time.RFC3339), now.Add(-10*time.Second).Format(time.RFC3339), hold)
			writeFile(t, state.StatusPath(h.State, meta.ID), lines)
			record, err := wake.Append(h.State, "notify", meta.ID, hold)
			if err != nil {
				t.Fatal(err)
			}
			if err := wake.AckThrough(h.State, record.Seq); err != nil {
				t.Fatal(err)
			}
			if test.laterReport != "" {
				lines += fmt.Sprintf("%s %s\n", now.Format(time.RFC3339), test.laterReport)
				writeFile(t, state.StatusPath(h.State, meta.ID), lines)
				if strings.HasPrefix(test.laterReport, "done: ") {
					if _, err := wake.Append(h.State, "notify", meta.ID, test.laterReport); err != nil {
						t.Fatal(err)
					}
				}
			}
			if test.hasNewDecision {
				if err := state.AppendStatus(h.State, meta.ID, "blocked: Which source should ship?"); err != nil {
					t.Fatal(err)
				}
				if _, err := wake.Append(h.State, "notify", meta.ID, "blocked: Which source should ship?"); err != nil {
					t.Fatal(err)
				}
			}
			observedAt := time.Now().UTC()
			observation := monitor.Observation{TaskID: meta.ID, Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: observedAt, Health: test.health, Reason: monitor.None, Digest: "current-screen", LastSeen: observedAt, LastProgress: observedAt}
			if test.health == monitor.HealthParked {
				observation.Reason = monitor.AwaitingDecision
			}
			if test.health == monitor.HealthUnknown {
				observation.EndpointVerdict, observation.Reason = monitor.ProbeMissing, monitor.EndpointMissing
			}
			if test.hasMovingJobs {
				prior := observedAt.Add(-30 * time.Second)
				observation.JobSampledAt, observation.JobSampledSince = &observedAt, &prior
				observation.HasJobProgress, observation.JobCPU = true, 5*time.Second
			}
			if err := monitor.WriteObservation(h.State, observation); err != nil {
				t.Fatal(err)
			}

			// Act
			view, err := (&Service{Store: store}).Snapshot()

			// Assert
			if err != nil || len(view.Tasks) != 1 {
				t.Fatalf("snapshot=%+v err=%v", view.Tasks, err)
			}
			got := view.Tasks[0]
			if got.Phase != test.wantPhase {
				t.Errorf("phase=%s reason=%s report=%s runtime=%+v, want %s", got.Phase, got.Reason, got.Report, got.Runtime, test.wantPhase)
			}
			if test.laterReport == "" && got.Reason != strings.TrimPrefix(hold, "blocked: ") {
				t.Errorf("hold detail lost: %+v", got)
			}
			if test.hasMovingJobs && !got.Runtime.working() {
				t.Errorf("real job progress hidden: %+v", got.Runtime)
			}
			if test.hasNewDecision && (len(view.Decisions) != 1 || !strings.Contains(got.Reason, "Which source should ship?")) {
				t.Errorf("new decision suppressed: task=%+v decisions=%+v", got, view.Decisions)
			}
			if strings.HasPrefix(test.laterReport, "done: ") && (got.PR != "https://github.com/o/r/pull/9" || got.Report != "done" || len(view.Decisions) != 1 || view.Decisions[0].Detail != test.laterReport) {
				t.Errorf("completion suppressed or treated as whole-task completion: task=%+v wakes=%+v", got, view.Decisions)
			}
		})
	}
}

func TestSnapshotAnswerReceiptAndAcknowledgementDoNotResumeWork(t *testing.T) {
	for _, test := range []struct {
		name, priorReport, wantPhase, wantReason, wantTarget string
	}{
		{"held work", "working: prior turn", "blocked", "Hold until reviewed", ""},
		{"human wait beside question", "waiting on overlord: human proof", "waiting", "human proof", "overlord"},
		{"CI wait beside question", "waiting on ci: exact checkout", "waiting", "exact checkout", "ci"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			if err := state.AppendStatus(h.State, "task-1", test.priorReport); err != nil {
				t.Fatal(err)
			}
			if err := state.AppendStatus(h.State, "task-1", "blocked: Hold until reviewed"); err != nil {
				t.Fatal(err)
			}
			record, err := wake.Append(h.State, "notify", "task-1", "blocked: Hold until reviewed")
			if err != nil {
				t.Fatal(err)
			}
			service := &Service{Store: store}
			view, err := service.Snapshot()
			if err != nil || view.Tasks[0].Phase != "blocked" || view.Tasks[0].Reason != "Waiting on the CFO: Hold until reviewed" {
				t.Fatalf("unanswered question lost: tasks=%+v err=%v", view.Tasks, err)
			}

			// Act
			if err := wake.MarkAnswered(h.State, record.Seq, wake.AnsweredByCFO, "Remain held"); err != nil {
				t.Fatal(err)
			}
			before, err := service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if err := wake.AckThrough(h.State, record.Seq); err != nil {
				t.Fatal(err)
			}
			after, err := service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}

			// Assert
			for _, snapshot := range []Snapshot{before, after} {
				got := snapshot.Tasks[0]
				if got.Phase != test.wantPhase || got.Reason != test.wantReason || got.WaitingOn != test.wantTarget {
					t.Errorf("answer/ACK invented resumption or lost a wait: task=%+v", got)
				}
			}
			if len(before.Decisions) != 1 || before.Decisions[0].Answered != "Remain held" || len(after.Decisions) != 0 {
				t.Errorf("answer receipt or acknowledgement lost: before=%+v after=%+v", before.Decisions, after.Decisions)
			}
		})
	}
}

func TestSnapshotAcknowledgedHoldStaysWithinItsGeneration(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	meta.SpawnGen = fmt.Sprintf("s%d", now.Add(-time.Minute).UnixNano())
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	writeFile(t, state.StatusPath(h.State, meta.ID), now.Add(-2*time.Minute).Format(time.RFC3339)+" blocked: Previous generation held\n")

	// Act
	view, err := (&Service{Store: store}).Snapshot()

	// Assert
	if err != nil || len(view.Tasks) != 1 || view.Tasks[0].Phase == "blocked" || view.Tasks[0].Report != "" || view.Tasks[0].LastReport != "" {
		t.Fatalf("prior generation held the current task: tasks=%+v err=%v", view.Tasks, err)
	}
}

func TestSnapshotHeldTaskKeepsTerminalResultForCFO(t *testing.T) {
	for _, code := range []int{0, 1} {
		t.Run(fmt.Sprintf("exit_%d", code), func(t *testing.T) {
			// Arrange: the provider-held task's already-running model-free job finishes.
			store, h := testStore(t)
			hold := "blocked: Provider held; only the already-running job may finish"
			if err := state.AppendStatus(h.State, "task-1", hold); err != nil {
				t.Fatal(err)
			}
			record, err := wake.Append(h.State, "notify", "task-1", hold)
			if err != nil {
				t.Fatal(err)
			}
			if err := wake.AckThrough(h.State, record.Seq); err != nil {
				t.Fatal(err)
			}
			started := time.Now().UTC()
			run := Run{ID: "cfo-run-held", Identity: strings.Repeat("a", 64), By: "cfo", Title: "Retained check", State: "running", RunAction: "held-check", CreatedAt: started, Started: &started, Shell: "powershell", Command: "Write-Output fixture", Cwd: h.Root}
			store.db.Runs = []Run{run}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(runDir(h.State, run), "output.log"), "authentic model-free result")
			writeFile(t, filepath.Join(runDir(h.State, run), "exit.txt"), fmt.Sprint(code))
			service := &Service{Store: store}

			// Act: no provider or terminal host is launched.
			if err := service.finishRuns(context.Background()); err != nil {
				t.Fatal(err)
			}
			view, err := service.Snapshot()

			// Assert
			finished := store.Snapshot().Runs[0]
			if err != nil || len(view.Tasks) != 1 || view.Tasks[0].Phase != "blocked" {
				t.Fatalf("job completion resumed held work: tasks=%+v err=%v", view.Tasks, err)
			}
			if finished.ExitCode == nil || *finished.ExitCode != code || finished.Output != "authentic model-free result" || !strings.Contains(finished.Untold, fmt.Sprintf("finished with exit code %d", code)) || finished.FinishedAt == nil {
				t.Fatalf("terminal result was lost while the CFO could not receive it: %+v", finished)
			}
			if err := service.finishRuns(context.Background()); err != nil || store.Snapshot().Runs[0].Untold != finished.Untold {
				t.Fatalf("completion was overwritten on rescan: err=%v run=%+v", err, store.Snapshot().Runs[0])
			}
		})
	}
}

func TestSnapshotAcknowledgementDoesNotEraseFailure(t *testing.T) {
	for _, test := range []struct {
		name, gatePhase, wantPhase string
		health                     monitor.Health
		isVerified                 bool
	}{
		{name: "prior working runtime", health: monitor.HealthBusy, wantPhase: "failed"},
		{name: "idle runtime", health: monitor.HealthIdle, wantPhase: "failed"},
		{name: "ended runtime", health: monitor.HealthUnknown, wantPhase: "failed"},
		{name: "gate held", health: monitor.HealthBusy, gatePhase: "blocked", wantPhase: "blocked"},
		{name: "gate failed", health: monitor.HealthIdle, gatePhase: "failed", wantPhase: "failed"},
		{name: "verified gate", health: monitor.HealthIdle, gatePhase: "ready", wantPhase: "ready", isVerified: true},
		{name: "merged evidence", health: monitor.HealthIdle, gatePhase: "merged", wantPhase: "merged"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			now := time.Now().UTC()
			store.db.Tasks["task-1"] = Evaluation{Generation: "g1", Phase: "working", Reason: "Historical turn"}
			if test.gatePhase != "" {
				store.db.Tasks["task-1"] = Evaluation{Generation: "g1", Phase: test.gatePhase, GateStep: "review", Reason: "Independent gate evidence", Verified: test.isVerified}
			}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			if err := state.AppendStatus(h.State, "task-1", "working: previous checks"); err != nil {
				t.Fatal(err)
			}
			failure := "failed: Retained command exited 1"
			if err := state.AppendStatus(h.State, "task-1", failure); err != nil {
				t.Fatal(err)
			}
			record, err := wake.Append(h.State, "notify", "task-1", failure)
			if err != nil {
				t.Fatal(err)
			}
			observation := monitor.Observation{TaskID: "task-1", Endpoint: (herdr.Target{Session: "test", Pane: "p1"}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: now, Health: test.health, Reason: monitor.None, Digest: "current-screen", LastSeen: now, LastProgress: now}
			if test.health == monitor.HealthUnknown {
				observation.EndpointVerdict, observation.Reason = monitor.ProbeMissing, monitor.EndpointMissing
			}
			if err := monitor.WriteObservation(h.State, observation); err != nil {
				t.Fatal(err)
			}
			service := &Service{Store: store}
			before, err := service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}

			// Act
			if err := wake.AckThrough(h.State, record.Seq); err != nil {
				t.Fatal(err)
			}
			after, err := service.Snapshot()

			// Assert
			if err != nil || len(after.Tasks) != 1 || after.Tasks[0].Phase != test.wantPhase || after.Tasks[0].Report != "failed" || after.Tasks[0].Verified != test.isVerified || len(after.Decisions) != 0 {
				t.Fatalf("ACK erased failure or gate custody: tasks=%+v err=%v", after.Tasks, err)
			}
			if test.gatePhase == "" && (before.Tasks[0].Phase != "failed" || before.Tasks[0].Reason != "Waiting on the CFO: Retained command exited 1" || after.Tasks[0].Reason != "Retained command exited 1") {
				t.Errorf("failure receipt confused with resumption: before=%+v after=%+v", before.Tasks[0], after.Tasks[0])
			}
			if test.gatePhase != "" && after.Tasks[0].Reason != "Independent gate evidence" {
				t.Errorf("failure report replaced independent gate evidence: %+v", after.Tasks[0])
			}
		})
	}
}

func TestSnapshotDeliveredHumanAnswerDoesNotInventWorkingActivity(t *testing.T) {
	// Arrange: an idle native task waiting for human proof received an answer, but has not resumed.
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Backend, meta.Mode = "native", "direct-PR"
	meta.HerdrSession, meta.HerdrPaneID = "", ""
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store.db.TaskSessions[meta.ID] = "codex/current"
	store.db.Sessions["codex/current"] = Session{ID: "codex/current", TaskID: meta.ID, Role: "goblin", Generation: meta.SpawnGen, Phase: "settled", UpdatedAt: now.Add(-time.Minute)}
	store.db.Tasks[meta.ID] = Evaluation{Generation: meta.SpawnGen, Phase: "working", Reason: "Historical turn"}
	writeFile(t, state.StatusPath(h.State, meta.ID), now.Add(-10*time.Second).Format(time.RFC3339)+" waiting on overlord: human proof\n")
	wait := openReview("waiting-task-1-7", meta.ID)
	if err := store.acceptReview(wait); err != nil {
		t.Fatal(err)
	}
	store.db.Reviews[0].State, store.db.Reviews[0].Delivered = "answered", true
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	if err := monitor.WriteObservation(h.State, monitor.Observation{TaskID: meta.ID, Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: now, Health: monitor.HealthIdle, Reason: monitor.None, Digest: "idle-screen", LastSeen: now, LastProgress: now}); err != nil {
		t.Fatal(err)
	}

	// Act
	view, err := (&Service{Store: store}).Snapshot()

	// Assert
	if err != nil || len(view.Tasks) != 1 || view.Tasks[0].Phase != "idle" || view.Tasks[0].Runtime.State != "idle" {
		t.Fatalf("delivered answer invented activity: tasks=%+v err=%v", view.Tasks, err)
	}
}

func TestSnapshotMissingEndpointCannotReuseWorkingReport(t *testing.T) {
	for _, test := range []struct {
		name, backend, wantPhase string
		isLinked, hasMovingJobs  bool
	}{
		{"ended native session", "native", "unavailable", true, false},
		{"missing pane without hooks", "herdr", "unavailable", false, false},
		{"quiet native terminal with progressing job", "native", "working", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.Mode, meta.Backend = "direct-PR", test.backend
			if test.backend == "native" {
				meta.HerdrSession, meta.HerdrPaneID = "", ""
			}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if test.isLinked {
				store.db.TaskSessions[meta.ID] = "codex/current"
				store.db.Sessions["codex/current"] = Session{ID: "codex/current", TaskID: meta.ID, Role: "goblin", Generation: meta.SpawnGen, Phase: "ended", UpdatedAt: now.Add(-time.Minute)}
			}
			store.db.Tasks[meta.ID] = Evaluation{Generation: meta.SpawnGen, Phase: "working", Reason: "Historical turn"}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			writeFile(t, state.StatusPath(h.State, meta.ID), now.Add(-20*time.Second).Format(time.RFC3339)+" working: previous checks\n")
			observation := monitor.Observation{TaskID: meta.ID, Endpoint: (herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}).String(), EndpointVerdict: monitor.ProbeMissing, LastObserved: now, Health: monitor.HealthUnknown, Reason: monitor.EndpointMissing}
			if test.hasMovingJobs {
				prior := now.Add(-30 * time.Second)
				observation.EndpointVerdict, observation.Health, observation.Reason = monitor.ProbePresent, monitor.HealthIdle, monitor.None
				observation.Digest, observation.LastSeen, observation.LastProgress = "quiet-screen", now, now
				observation.JobSampledAt, observation.JobSampledSince, observation.HasJobProgress, observation.JobCPU = &now, &prior, true, 5*time.Second
			}
			if err := monitor.WriteObservation(h.State, observation); err != nil {
				t.Fatal(err)
			}

			// Act
			view, err := (&Service{Store: store}).Snapshot()

			// Assert
			if err != nil || len(view.Tasks) != 1 || view.Tasks[0].Phase != test.wantPhase || view.Tasks[0].Runtime.working() != test.hasMovingJobs {
				t.Fatalf("missing endpoint reused work or hid owned progress: tasks=%+v err=%v", view.Tasks, err)
			}
			if !test.hasMovingJobs && view.Tasks[0].Reason != view.Tasks[0].Runtime.Reason {
				t.Errorf("missing endpoint reason lost: %+v", view.Tasks[0])
			}
		})
	}
}
