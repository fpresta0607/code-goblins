package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestSnapshotAndStartAgreeOnBriefQueueMembership(t *testing.T) {
	for _, test := range []struct {
		name, backlog string
		isQueued      bool
	}{
		{"held in flight", "## In flight\n- [ ] next-task - Held draft (repo: code-goblins) (hold: PR is verified and held for merge authority) (hold-kind: captain)\n", false},
		{"done", "## Done\n- [x] next-task - Delivered (repo: code-goblins)\n", false},
		{"parked", "## Parked\n- [ ] next-task - Later (repo: code-goblins)\n", false},
		{"other listed section", "## In progress\n- **next-task** - Already allocated (repo: code-goblins)\n", false},
		{"legacy queued", "## Queued\n- **next-task** - Ship it (repo: code-goblins)\n", true},
		{"modern queued", "## Queued\n- [ ] next-task - Ship it (repo: code-goblins)\n", true},
		{"unlisted brief", "## Queued\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 11*gigabyte, spawner)
			writeFile(t, filepath.Join(h.Data, "backlog.md"), test.backlog)
			writeFile(t, filepath.Join(h.Data, "next-task", "brief.md"), plainBrief)

			view, err := handler.Service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			isQueued := false
			for _, task := range view.Tasks {
				if task.ID == "next-task" {
					isQueued = task.Phase == "queued"
					if test.isQueued && task.QueueRevision == "" {
						t.Error("queued card lacks Start admission revision")
					}
				}
			}
			if isQueued != test.isQueued {
				t.Errorf("snapshot queued=%t, want %t; tasks=%+v", isQueued, test.isQueued, view.Tasks)
			}
			response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
			if test.isQueued {
				if response.Code != 202 {
					t.Fatalf("legitimate Start=%d %s", response.Code, response.Body)
				}
				waitStarted(t, handler, "next-task")
				if len(spawner.recorded()) != 1 {
					t.Fatal("legitimate queued task did not start exactly once")
				}
			} else if response.Code != 409 || len(spawner.recorded()) != 0 {
				t.Fatalf("nonqueued Start=%d %s; dispatches=%d", response.Code, response.Body, len(spawner.recorded()))
			}
		})
	}
}

func TestSnapshotOldQuestionCannotHoldANewNativeGeneration(t *testing.T) {
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	meta.SpawnGen = fmt.Sprintf("s%d", now.Add(-time.Minute).UnixNano())
	meta.Mode, meta.Backend = "direct-PR", "native"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	store.db.TaskSessions[meta.ID] = "codex/current"
	store.db.Sessions["codex/current"] = Session{ID: "codex/current", TaskID: meta.ID, Role: "goblin", Generation: meta.SpawnGen, Phase: "settled", UpdatedAt: now.Add(-time.Minute)}
	store.db.Tasks[meta.ID] = Evaluation{Generation: meta.SpawnGen, Phase: "review", Reason: "Current independent evidence"}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, state.StatusPath(h.State, meta.ID), now.Add(-10*time.Second).Format(time.RFC3339)+" done: PR https://github.com/o/r/pull/338\n")
	record := wake.Record{Seq: 1, Time: now.Add(-2 * time.Minute), Kind: "notify", Key: meta.ID, Detail: "blocked: Previous generation's question"}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, ".wake-queue"), string(data)+"\n")

	view, err := (&Service{Store: store}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Tasks) != 1 || view.Tasks[0].Phase != "review" || view.Tasks[0].Verified || len(view.Decisions) != 1 {
		t.Fatalf("prior question changed this generation or was removed: tasks=%+v decisions=%+v", view.Tasks, view.Decisions)
	}
}

func TestSnapshotNativeReportsRespectQuestionsGatesAndFreshRuntime(t *testing.T) {
	for _, test := range []struct {
		name, report, gatePhase, gateStep, question, wantPhase, wantReason string
		health                                                             monitor.Health
		isOldObservation, isAnswered, isResumed, isVerified, isActive      bool
		hasMovingJobs, hasSleepingJobs                                     bool
	}{
		{name: "manual reported PR at ready composer", report: "done: PR https://github.com/o/r/pull/338", health: monitor.HealthIdle, wantPhase: "idle", wantReason: "Native terminal reports idle"},
		{name: "held work report at ready composer", report: "working: No implementation remains; merge authority is held", health: monitor.HealthIdle, wantPhase: "idle", wantReason: "Native terminal reports idle"},
		{name: "quiet terminal with owned work moving", report: "working: running package checks", health: monitor.HealthIdle, hasMovingJobs: true, wantPhase: "working", wantReason: "running package checks"},
		{name: "sleeping owned process does not claim active work", report: "working: No implementation remains; merge authority is held", health: monitor.HealthIdle, hasSleepingJobs: true, wantPhase: "idle", wantReason: "Native terminal reports idle"},
		{name: "newer work report than idle observation", report: "working: running package checks", health: monitor.HealthIdle, isOldObservation: true, wantPhase: "working", wantReason: "running package checks"},
		{name: "done is per PR while next work runs", report: "done: PR https://github.com/o/r/pull/338", health: monitor.HealthBusy, wantPhase: "working"},
		{name: "new active turn keeps independent source binding", report: "done: PR https://github.com/o/r/pull/338", health: monitor.HealthBusy, isActive: true, wantPhase: "working"},
		{name: "newer held question beats older work", report: "working: collection evidence ready", health: monitor.HealthBusy, question: "blocked: Hold for live-auth evidence?", wantPhase: "blocked", wantReason: "Waiting on the CFO: Hold for live-auth evidence?"},
		{name: "answered question releases earlier report", report: "working: collecting evidence", health: monitor.HealthBusy, question: "blocked: Which source?", isAnswered: true, wantPhase: "working", wantReason: "collecting evidence"},
		{name: "newer report resumes without deleting decision", report: "working: old work", health: monitor.HealthBusy, question: "blocked: Which source?", isResumed: true, wantPhase: "working", wantReason: "resumed work"},
		{name: "explicit CI wait survives idle", report: "waiting on ci: hosted checks", health: monitor.HealthIdle, wantPhase: "waiting", wantReason: "hosted checks"},
		{name: "gate decision survives idle and working report", report: "working: local checks done", health: monitor.HealthIdle, gatePhase: "blocked", gateStep: "review", wantPhase: "blocked", wantReason: "Independent gate evidence"},
		{name: "running gate survives idle", report: "done: PR https://github.com/o/r/pull/338", health: monitor.HealthIdle, gatePhase: "review", gateStep: "test", wantPhase: "review", wantReason: "Independent gate evidence"},
		{name: "running gate survives older work report", report: "working: final checks", health: monitor.HealthIdle, gatePhase: "review", gateStep: "test", wantPhase: "review", wantReason: "Independent gate evidence"},
		{name: "verified readiness survives idle", report: "done: PR https://github.com/o/r/pull/338", health: monitor.HealthIdle, gatePhase: "ready", isVerified: true, wantPhase: "ready", wantReason: "Independent gate evidence"},
		{name: "verified readiness survives older work report", report: "working: local checks", health: monitor.HealthIdle, gatePhase: "ready", isVerified: true, wantPhase: "ready", wantReason: "Independent gate evidence"},
		{name: "active turn cannot erase independent gate custody", report: "done: PR https://github.com/o/r/pull/338", health: monitor.HealthBusy, gatePhase: "ready", isVerified: true, isActive: true, wantPhase: "ready", wantReason: "Independent gate evidence"},
		{name: "merged evidence survives idle", report: "working: held", health: monitor.HealthIdle, gatePhase: "merged", wantPhase: "merged", wantReason: "Independent gate evidence"},
		{name: "unknown runtime retains manual evidence boundary", report: "done: PR https://github.com/o/r/pull/338", health: monitor.HealthUnknown, wantPhase: "review", wantReason: "Manual task mode requires its existing review and delivery evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, h := testStore(t)
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.Mode, meta.Backend = "direct-PR", "native"
			meta.HerdrSession, meta.HerdrPaneID = "", ""
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			for _, name := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
				if err := store.Accept(event(t, h, name, "current", "turn-1", now.Add(-time.Minute))); err != nil {
					t.Fatal(err)
				}
			}
			gitFixture(t, meta.Worktree)
			if test.isActive {
				if err := store.Accept(event(t, h, "UserPromptSubmit", "current", "turn-2", now.Add(-10*time.Second))); err != nil {
					t.Fatal(err)
				}
			}
			service := &Service{Store: store}
			evaluation, err := service.execute(context.Background(), Action{Kind: "evaluate", TaskID: meta.ID, Generation: meta.SpawnGen})
			if err != nil || evaluation.Phase != "review" || evaluation.Verified {
				t.Fatalf("manual independent evaluation=%+v, err=%v", evaluation, err)
			}
			evaluation.Generation, evaluation.At = meta.SpawnGen, now.Add(-time.Minute)
			if test.gatePhase != "" {
				evaluation.Phase, evaluation.Reason = test.gatePhase, "Independent gate evidence"
				evaluation.GateStep, evaluation.Verified = test.gateStep, test.isVerified
			}
			store.db.Tasks[meta.ID] = evaluation
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			reportedAt := now.Add(-20 * time.Second)
			lines := fmt.Sprintf("%s %s\n", reportedAt.Format(time.RFC3339), test.report)
			if test.question != "" {
				lines += fmt.Sprintf("%s %s\n", now.Add(-10*time.Second).Format(time.RFC3339), test.question)
				record, err := wake.Append(h.State, "notify", meta.ID, test.question)
				if err != nil {
					t.Fatal(err)
				}
				if test.isAnswered {
					if err := wake.MarkAnswered(h.State, record.Seq, "cfo", "Use the source"); err != nil {
						t.Fatal(err)
					}
				}
				if test.isResumed {
					lines += fmt.Sprintf("%s working: resumed work\n", now.Add(time.Second).Format(time.RFC3339))
				}
			}
			writeFile(t, state.StatusPath(h.State, meta.ID), lines)
			observedAt := now
			if test.isOldObservation {
				observedAt = now.Add(-30 * time.Second)
			}
			if test.health != monitor.HealthUnknown {
				observation := monitor.Observation{TaskID: meta.ID, Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: observedAt, Health: test.health, Reason: monitor.None, Digest: "current-screen", LastSeen: observedAt, LastProgress: observedAt}
				if test.hasMovingJobs || test.hasSleepingJobs {
					prior := observedAt.Add(-time.Minute)
					observation.JobSampledAt, observation.JobSampledSince = &observedAt, &prior
					observation.JobCPU, observation.EvidenceAt = 5*time.Second, &observedAt
					observation.HasJobProgress = test.hasMovingJobs
					if test.hasSleepingJobs {
						observation.EvidenceAt = &prior
					}
				}
				if err := monitor.WriteObservation(h.State, observation); err != nil {
					t.Fatal(err)
				}
			}

			view, err := service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if len(view.Tasks) != 1 {
				t.Fatalf("tasks=%+v", view.Tasks)
			}
			got := view.Tasks[0]
			if got.Phase != test.wantPhase || test.wantReason != "" && got.Reason != test.wantReason || got.Verified != test.isVerified {
				t.Errorf("snapshot=%+v, want phase=%s reason=%s verified=%t", got, test.wantPhase, test.wantReason, test.isVerified)
			}
			if strings.HasPrefix(test.report, "done: ") && got.PR != "https://github.com/o/r/pull/338" {
				t.Errorf("reported PR was lost: %+v", got)
			}
			if test.question != "" && len(view.Decisions) != 1 {
				t.Errorf("projection removed the durable question: %+v", view.Decisions)
			}
			if got.Head != evaluation.Head || got.Generation != meta.SpawnGen || got.GateStep != evaluation.GateStep {
				t.Errorf("projection changed independent evidence: %+v", got.Evaluation)
			}
		})
	}
}

type queueStatusProgressProbe struct {
	sample monitor.ProgressSample
	err    error
}

func (p *queueStatusProgressProbe) InspectProgress(context.Context, state.TaskMeta, monitor.EndpointSample) (monitor.ProgressSample, error) {
	return p.sample, p.err
}

func TestSnapshotKeepsJudgedOwnedProgressAcrossShortRescans(t *testing.T) {
	for _, test := range []struct {
		name                                      string
		hasMovingJobs, hasBaselineTranscript      bool
		hasTranscriptOnly                         bool
		hasReadError, hasNoJobs, hasNewTranscript bool
	}{
		{name: "moving jobs then sleeping", hasMovingJobs: true},
		{name: "sleeping jobs"},
		{name: "unjudged baseline with transcript", hasBaselineTranscript: true},
		{name: "transcript without CPU progress", hasTranscriptOnly: true},
		{name: "read error after positive CPU", hasMovingJobs: true, hasReadError: true},
		{name: "jobs end after positive CPU", hasMovingJobs: true, hasNoJobs: true},
		{name: "short rescan with newer transcript", hasMovingJobs: true, hasNewTranscript: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, h := testStore(t)
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.Mode, meta.Backend = "direct-PR", "native"
			meta.HerdrSession, meta.HerdrPaneID, meta.HerdrWorkspaceID, meta.HerdrTabID = "", "", "", ""
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Add(-11*time.Minute - 21*time.Second)
			if err := os.Chtimes(filepath.Join(h.State, meta.ID+".meta"), now, now); err != nil {
				t.Fatal(err)
			}
			store.db.TaskSessions[meta.ID] = "codex/current"
			store.db.Sessions["codex/current"] = Session{ID: "codex/current", TaskID: meta.ID, Role: "goblin", Generation: meta.SpawnGen, Phase: "settled", UpdatedAt: now}
			store.db.Tasks[meta.ID] = Evaluation{Generation: meta.SpawnGen, Phase: "review", Reason: "Manual independent evaluation", At: now}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			writeFile(t, state.StatusPath(h.State, meta.ID), now.Format(time.RFC3339)+" working: running package checks\n")
			probe := &runtimeProbe{sample: monitor.EndpointSample{Verdict: monitor.ProbePresent, Agent: herdr.AgentAlive, Status: herdr.AgentUnknown, TabLabel: "gb-" + meta.ID, CountersUnavailable: true, Capture: []byte("quiet native terminal")}}
			progress := &queueStatusProgressProbe{sample: monitor.ProgressSample{Jobs: []string{"go.exe (pid 51)"}, JobCPU: time.Second}}
			watcher := monitor.Service{StateDir: h.State, Probe: probe, Progress: progress, Now: func() time.Time { return now }}
			service := &Service{Store: store}
			now = now.Add(10*time.Minute + time.Second)
			idleSince := now.Add(-10 * time.Minute)
			info, err := os.Stat(state.StatusPath(h.State, meta.ID))
			if err != nil {
				t.Fatal(err)
			}
			observation := monitor.Observation{TaskID: meta.ID, Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: now, LastSeen: idleSince, LastProgress: idleSince, IdleSince: &idleSince, Health: monitor.HealthIdle, Reason: monitor.None, Digest: "prior-screen", StatusStamp: fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())}
			if err := monitor.WriteObservation(h.State, observation); err != nil {
				t.Fatal(err)
			}
			var sampledAt time.Time
			for index, checkpoint := range []struct {
				name  string
				step  time.Duration
				phase string
			}{
				{"baseline", 0, "idle"},
				{"judged sample", time.Minute, "idle"},
				{"short rescan", 10 * time.Second, "idle"},
				{"later judged no progress", 50 * time.Second, "idle"},
				{"short rescan after no progress", 10 * time.Second, "idle"},
			} {
				now = now.Add(checkpoint.step)
				if index == 0 && test.hasBaselineTranscript || index == 1 && test.hasTranscriptOnly {
					progress.sample.TranscriptAt = now
				}
				if test.hasMovingJobs && index == 1 {
					progress.sample.JobCPU += 4 * time.Second
				}
				if index == 2 {
					if test.hasReadError {
						progress.err = errors.New("CPU evidence unavailable")
					}
					if test.hasNoJobs {
						progress.sample.Jobs = nil
					}
					if test.hasNewTranscript {
						progress.sample.TranscriptAt = now
					}
				}
				if test.hasMovingJobs && (index == 1 || index == 2 && !test.hasReadError && !test.hasNoJobs) {
					checkpoint.phase = "working"
				}
				if index >= 2 && (test.hasReadError || test.hasNoJobs) {
					checkpoint.phase = "working"
				}
				result, err := watcher.Scan(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Observations) != 1 || result.Observations[0].Health != monitor.HealthIdle && !(index >= 2 && (test.hasNoJobs || test.hasReadError)) {
					t.Fatalf("%s monitor observation=%+v", checkpoint.name, result.Observations)
				}
				observation := result.Observations[0]
				if index >= 2 && (test.hasReadError || test.hasNoJobs) && observation.HasJobProgress {
					t.Error("unavailable or ended jobs retained positive CPU judgment")
				}
				if test.hasNoJobs && index >= 2 {
					if observation.JobSampledAt != nil || observation.JobSampledSince != nil || observation.HasJobProgress {
						t.Fatalf("ended jobs retained CPU progress: %+v", observation)
					}
				} else if observation.JobSampledAt == nil || observation.JobSampledSince == nil {
					t.Fatalf("%s lacks CPU sample provenance: %+v", checkpoint.name, observation)
				} else if index == 2 || index == 4 {
					if !observation.JobSampledAt.Equal(sampledAt) || !observation.LastObserved.Equal(now) {
						t.Fatalf("short rescan changed the judged sample: %+v", observation)
					}
				} else {
					sampledAt = *observation.JobSampledAt
				}
				view, err := service.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if len(view.Tasks) != 1 || view.Tasks[0].Phase != checkpoint.phase || view.Tasks[0].Verified {
					t.Errorf("%s snapshot=%+v, want phase=%s without verified delivery", checkpoint.name, view.Tasks, checkpoint.phase)
				}
				if index >= 2 && (test.hasReadError || test.hasNoJobs) && view.Tasks[0].Runtime.working() {
					t.Errorf("unavailable or ended jobs became current runtime progress: %+v", view.Tasks[0].Runtime)
				}
			}
		})
	}
}

func TestRuntimeOwnedProgressRequiresCurrentCPUSample(t *testing.T) {
	for _, test := range []struct {
		name                                                          string
		sampledAgo                                                    time.Duration
		isBaseline, isMissingSample                                   bool
		isMissingSince, isReversedSince                               bool
		isPriorNode                                                   bool
		isForeignEndpoint, isForeignGeneration, isUnreadable          bool
		isPriorGenerationBaseline, isCurrentGeneration, hasNoJudgment bool
		wantState                                                     string
	}{
		{name: "current CPU sample", wantState: "busy"},
		{name: "short rescan keeps positive sample", sampledAgo: 10 * time.Second, wantState: "busy"},
		{name: "CPU stale behind fresh observation", sampledAgo: 2*time.Minute + time.Nanosecond, wantState: "idle"},
		{name: "CPU at existing stale limit", sampledAgo: 2 * time.Minute, wantState: "busy"},
		{name: "future CPU beyond existing limit", sampledAgo: -time.Minute - time.Nanosecond, wantState: "idle"},
		{name: "CPU newer than terminal observation", sampledAgo: -time.Second, wantState: "idle"},
		{name: "baseline has no judged interval", isBaseline: true, wantState: "idle"},
		{name: "missing CPU sample", isMissingSample: true, wantState: "idle"},
		{name: "missing CPU baseline", isMissingSince: true, wantState: "idle"},
		{name: "reversed CPU interval", isReversedSince: true, wantState: "idle"},
		{name: "CPU predates current native event", sampledAgo: 10 * time.Second, isPriorNode: true, wantState: "idle"},
		{name: "foreign endpoint", isForeignEndpoint: true, wantState: "unknown"},
		{name: "foreign generation", isForeignGeneration: true, wantState: "unknown"},
		{name: "CPU baseline from prior generation", isPriorGenerationBaseline: true, wantState: "idle"},
		{name: "CPU interval within current generation", isCurrentGeneration: true, wantState: "busy"},
		{name: "legacy shared timestamp has no CPU verdict", hasNoJudgment: true, wantState: "idle"},
		{name: "unreadable persisted CPU", isUnreadable: true, wantState: "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, h := testStore(t)
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			at := now.Add(-test.sampledAgo)
			since := at.Add(-time.Minute)
			observation := monitor.Observation{TaskID: meta.ID, Endpoint: (herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: now, LastSeen: now, LastProgress: now, Health: monitor.HealthIdle, Reason: monitor.None, Digest: "quiet-pane", EvidenceAt: &now, JobCPU: 5 * time.Second, JobSampledAt: &at, JobSampledSince: &since, HasJobProgress: true}
			node := Session{Generation: meta.SpawnGen, UpdatedAt: now.Add(-10 * time.Minute)}
			if test.isPriorGenerationBaseline || test.isCurrentGeneration {
				born := now.Add(-30 * time.Second)
				if test.isCurrentGeneration {
					born = now.Add(-2 * time.Minute)
				}
				meta.SpawnGen = fmt.Sprintf("s%d", born.UnixNano())
				node.Generation, node.UpdatedAt = meta.SpawnGen, born
			}
			if test.hasNoJudgment {
				observation.HasJobProgress = false
			}
			if test.isBaseline {
				since = at
			}
			if test.isReversedSince {
				since = at.Add(time.Second)
			}
			if test.isMissingSample {
				observation.JobSampledAt = nil
			}
			if test.isMissingSince {
				observation.JobSampledSince = nil
			}
			if test.isPriorNode {
				node.UpdatedAt = now.Add(-time.Second)
			}
			if test.isForeignEndpoint {
				observation.Endpoint = "foreign:terminal"
			}
			if test.isForeignGeneration {
				node.Generation = "previous-generation"
			}
			if err := monitor.WriteObservation(h.State, observation); err != nil {
				t.Fatal(err)
			}
			if test.isUnreadable {
				writeFile(t, monitor.ObservationPath(h.State, meta.ID), `{"has_job_progress":true,"job_sampled_at":"unreadable"}`)
			}
			got := (&Service{Store: store}).runtimeEvidence(meta, node, now)
			if got.State != test.wantState {
				t.Errorf("runtime=%+v, want %s", got, test.wantState)
			}
			if got.State == "busy" && !got.At.Equal(at) {
				t.Errorf("terminal rescan refreshed CPU evidence time: %+v, want %s", got, at)
			}
		})
	}
}
