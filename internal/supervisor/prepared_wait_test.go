package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type preparedGit struct{ head, branch, dirty string }

func (git *preparedGit) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	switch request.Args[0] {
	case "rev-parse":
		return execx.Result{Stdout: []byte(git.head)}, nil
	case "symbolic-ref":
		return execx.Result{Stdout: []byte(git.branch)}, nil
	case "status":
		return execx.Result{Stdout: []byte(git.dirty)}, nil
	case "for-each-ref":
		return execx.Result{Stdout: []byte("*\trefs/heads/" + git.branch + "\t" + git.head + "\n")}, nil
	}
	return execx.Result{}, errors.New("unexpected prepared source read")
}

type preparedGate struct {
	progress     pipeline.Progress
	err, custody error
}

func (gate *preparedGate) Progress(context.Context, string, string) (pipeline.Progress, error) {
	return gate.progress, gate.err
}
func (gate *preparedGate) CanSteer(context.Context, string, string, string) error {
	return gate.custody
}
func (*preparedGate) CanTypeFreshDetached(context.Context, string) error { return nil }

type preparedFixture struct {
	service *Service
	home    home.Home
	meta    state.TaskMeta
	record  state.PreparedWait
	git     *preparedGit
	gate    *preparedGate
	spawner *spawnRecorder
	tree    fleettree.Tree
	now     time.Time
}

func preparedPackage(t *testing.T) *preparedFixture {
	t.Helper()
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC()
	meta := state.TaskMeta{ID: "rex-package", SpawnGen: "generation-rex", Backend: "native", Worktree: h.Root, Project: h.Root, TaskTmp: filepath.Join(h.State, "tasktmp", "rex-package")}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "package-delivery", SpawnGen: "generation-package"}); err != nil {
		t.Fatal(err)
	}
	condition, err := state.NewPauseCondition("dependency", "task:package-delivery", now.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	handoff := filepath.Join(h.Data, meta.ID, "handoff.md")
	writeFile(t, handoff, "Prepared package; waiting for the named delivery.\n")
	digest := sha256.Sum256([]byte("Prepared package; waiting for the named delivery.\n"))
	record := state.PreparedWait{ID: meta.ID, Generation: meta.SpawnGen, Project: h.Root, Worktree: h.Root, Session: "saved-rex", Branch: "feat/rex", Head: strings.Repeat("a", 40), Handoff: handoff, Digest: hex.EncodeToString(digest[:]), Condition: condition, PreparedAt: condition.At, Phase: "prepared"}
	record.Operation = record.OperationID()
	fixture := &preparedFixture{service: handler.Service, home: h, meta: meta, record: record, git: &preparedGit{head: record.Head, branch: record.Branch}, gate: &preparedGate{err: pipeline.ErrNoProgress}, spawner: spawner, now: now, tree: fleettree.Tree{TaskID: meta.ID, Generation: meta.SpawnGen, Session: record.Session, FetchedAt: now}}
	fixture.save(t)
	if err := state.WriteConversation(h.State, state.Conversation{ID: meta.ID, Generation: meta.SpawnGen, Session: record.Session}); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, meta.ID, record.Report()); err != nil {
		t.Fatal(err)
	}
	if err := monitor.WriteObservation(h.State, monitor.Observation{TaskID: meta.ID, Endpoint: ":", EndpointVerdict: monitor.ProbePresent, LastObserved: now, LastSeen: now, LastProgress: now, Digest: "idle", Health: monitor.HealthIdle, Reason: monitor.None}); err != nil {
		t.Fatal(err)
	}
	fixture.service.Options.Progress = fixture.git
	fixture.service.Options.Gate = fixture.gate
	fixture.service.Options.PreparedOwnership = func(context.Context, state.TaskMeta) (fleettree.Tree, error) { return fixture.tree, nil }
	fixture.service.Options.Dispatch.Idle = func(context.Context, state.TaskMeta) (bool, error) { return true, nil }
	spawner.during = func(args []string) {
		request := lifecycle.Request{ID: meta.ID, Action: args[0], Generation: valueAfter(args, "--generation"), Operation: valueAfter(args, "--operation"), Reason: valueAfter(args, "--reason"), Until: valueAfter(args, "--until"), Session: record.Session}
		operations := lifecycle.Operations{
			Notify:  func(state.Lifecycle) error { return nil },
			Prepare: func(context.Context, state.TaskMeta, string) error { return nil },
			Stop: func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) {
				return []string{"synthetic terminal"}, nil
			},
			Memory: func() (uint64, uint64, error) { return 8 * gigabyte, 8 * gigabyte, nil },
			Resume: func(_ context.Context, current state.TaskMeta, _ state.Lifecycle) error {
				current.SpawnGen, current.ResumeOperation = "generation-resumed-rex", request.Operation
				return state.WriteTaskMeta(h.State, current)
			},
		}
		if _, err := (lifecycle.Service{StateDir: h.State, Operations: operations}).Run(context.Background(), request); err != nil {
			t.Errorf("synthetic lifecycle: %v", err)
		}
	}
	return fixture
}

func valueAfter(args []string, flag string) string {
	for index, value := range args {
		if value == flag && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func (fixture *preparedFixture) save(t *testing.T) {
	t.Helper()
	fixture.record.Operation = fixture.record.OperationID()
	if err := state.WritePreparedWait(fixture.home.State, fixture.record); err != nil {
		t.Fatal(err)
	}
}

func (fixture *preparedFixture) reconcile(t *testing.T) state.PreparedWait {
	t.Helper()
	if err := fixture.service.reconcilePreparedWaits(t.Context(), &fleetWakes{}, fixture.now); err != nil {
		t.Fatal(err)
	}
	record, err := state.ReadPreparedWait(fixture.home.State, fixture.meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func (fixture *preparedFixture) reopen(t *testing.T) {
	t.Helper()
	store, err := Open(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	fixture.service = &Service{Store: store, Options: fixture.service.Options}
}

func TestPreparedPackageWaitReproducesRetainedTerminal(t *testing.T) {
	fixture := preparedPackage(t)
	if err := fixture.service.checkFleet(t.Context(), fixture.now); err != nil {
		t.Fatal(err)
	}
	for scan := 0; scan < 3; scan++ {
		if scan == 2 {
			fixture.reopen(t)
		}
		if record := fixture.reconcile(t); record.Phase != "paused" {
			t.Fatalf("phase=%s, problem=%s", record.Phase, record.Problem)
		}
	}
	calls := fixture.spawner.recorded()
	if len(calls) != 1 || calls[0][0] != "pause" || valueAfter(calls[0], "--operation") != fixture.record.Operation || valueAfter(calls[0], "--until") != "task:package-delivery" {
		t.Fatalf("lifecycle calls=%v, want one stable dependency pause", calls)
	}
	paused, err := state.ReadLifecycle(fixture.home.State, fixture.meta.ID)
	if err != nil || !paused.HandoffSaved || paused.Session != "saved-rex" || !slices.Contains(paused.Kept, "task session and branch") || !slices.Contains(paused.Kept, "worktree "+fixture.meta.Worktree) {
		t.Fatalf("preserved lifecycle=%+v, err=%v", paused, err)
	}
	if data, err := os.ReadFile(paused.Handoff); err != nil || string(data) != "Prepared package; waiting for the named delivery.\n" {
		t.Fatalf("saved handoff=%q, err=%v", data, err)
	}
}

func TestPreparedWaitReportsReplayWithoutRenewingAuthorization(t *testing.T) {
	fixture := preparedPackage(t)
	if err := os.Remove(filepath.Join(fixture.home.State, "prepared-waits", fixture.meta.ID+".json")); err != nil {
		t.Fatal(err)
	}
	for report := 0; report < 2; report++ {
		if err := publishPreparedWait(t.Context(), fixture.home, fixture.meta, fixture.record.Condition.Until, fixture.record.Handoff, fixture.git); err != nil {
			t.Fatal(err)
		}
	}
	record, err := state.ReadPreparedWait(fixture.home.State, fixture.meta.ID)
	if err != nil || record.Operation != fixture.record.Operation {
		t.Fatalf("record=%+v, error=%v", record, err)
	}
	record.Phase, record.Problem = "invalid", "newer activity"
	if err := state.WritePreparedWait(fixture.home.State, record); err != nil {
		t.Fatal(err)
	}
	if err := publishPreparedWait(t.Context(), fixture.home, fixture.meta, fixture.record.Condition.Until, fixture.record.Handoff, fixture.git); err != nil {
		t.Fatal(err)
	}
	replayed, err := state.ReadPreparedWait(fixture.home.State, fixture.meta.ID)
	if err != nil || replayed != record {
		t.Fatalf("replay renewed invalid authorization: %+v, %v", replayed, err)
	}
}

func TestPreparedWaitRequiresKnownPullRequestMergeEvidence(t *testing.T) {
	for _, phase := range []string{"OPEN", "MERGED", "CLOSED", "unknown", "unreadable", "unavailable"} {
		t.Run(phase, func(t *testing.T) {
			fixture := preparedPackage(t)
			fixture.record.Condition.Until = "pr:https://github.com/owner/repo/pull/7"
			fixture.save(t)
			if err := state.AppendStatus(fixture.home.State, fixture.meta.ID, fixture.record.Report()); err != nil {
				t.Fatal(err)
			}
			if phase != "unavailable" {
				fixture.service.Options.PullRequestState = func(context.Context, string) (PullRequestInfo, error) {
					if phase == "unreadable" {
						return PullRequestInfo{}, errors.New("PR lookup failed")
					}
					return PullRequestInfo{State: phase}, nil
				}
			}
			record := fixture.reconcile(t)
			calls := fixture.spawner.recorded()
			if phase == "OPEN" {
				if len(calls) != 1 || record.Phase != "paused" {
					t.Fatalf("record=%+v, calls=%v", record, calls)
				}
			} else if len(calls) != 0 {
				t.Fatalf("non-open PR authorized pause: %v", calls)
			}
			if phase == "MERGED" && record.Phase != "cleared" {
				t.Fatalf("merged PR not reconciled: %+v", record)
			}
			if phase != "OPEN" && phase != "MERGED" && record.Problem == "" {
				t.Fatalf("unreadable PR has no visible reason: %+v", record)
			}
		})
	}
}

func TestPreparedWaitAdmitsOnlyAfterWorkerSettles(t *testing.T) {
	fixture := preparedPackage(t)
	fixture.service.Options.Dispatch.Idle = func(context.Context, state.TaskMeta) (bool, error) { return false, nil }
	if record := fixture.reconcile(t); record.Phase != "prepared" || record.Problem == "" {
		t.Fatalf("record=%+v", record)
	}
	fixture.service.Options.Dispatch.Idle = func(context.Context, state.TaskMeta) (bool, error) { return true, nil }
	observation, err := monitor.ReadObservation(fixture.home.State, fixture.meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	observation.Health, observation.Reason = monitor.HealthParked, monitor.AwaitingDecision
	if err := monitor.WriteObservation(fixture.home.State, observation); err != nil {
		t.Fatal(err)
	}
	if record := fixture.reconcile(t); record.Phase != "paused" {
		t.Fatalf("record=%+v", record)
	}
}

func TestPreparedResumeFailureAndInterruptedAdmissionDoNotRetry(t *testing.T) {
	for _, isInterrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "interrupted admission"}[isInterrupted], func(t *testing.T) {
			fixture := preparedPackage(t)
			fixture.reconcile(t)
			fixture.deliver(t)
			paused, err := state.ReadLifecycle(fixture.home.State, fixture.meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			if isInterrupted {
				if _, isAdmitted, err := fixture.service.admitPreparedResume(paused); err != nil || !isAdmitted {
					t.Fatalf("admitted=%t, error=%v", isAdmitted, err)
				}
			} else {
				fixture.spawner.err, fixture.spawner.during = errors.New("resume failed"), nil
				if _, err := fixture.service.schedule(t.Context(), fixture.now, &fleetWakes{}, Memory{Available: 8 * gigabyte}, ""); err != nil {
					t.Fatal(err)
				}
				awaitDispatch(t, fixture.service, fixture.spawner, 2)
			}
			for scan := 0; scan < 2; scan++ {
				fixture.reopen(t)
				fixture.reconcile(t)
				if _, err := fixture.service.schedule(t.Context(), fixture.now, &fleetWakes{}, Memory{Available: 8 * gigabyte}, ""); err != nil {
					t.Fatal(err)
				}
			}
			want := 2
			if isInterrupted {
				want = 1
			}
			if calls := fixture.spawner.recorded(); len(calls) != want {
				t.Fatalf("uncertain resume retried: %v", calls)
			}
		})
	}
}

func TestPreparedWaitRefusesUnsafeEvidence(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		change       func(*testing.T, *preparedFixture)
	}{
		{"generation", "generation or source changed", func(t *testing.T, f *preparedFixture) {
			f.meta.SpawnGen = "new"
			if err := state.WriteTaskMeta(f.home.State, f.meta); err != nil {
				t.Fatal(err)
			}
		}},
		{"commit", "commit changed", func(_ *testing.T, f *preparedFixture) { f.git.head = strings.Repeat("b", 40) }},
		{"branch", "branch or commit changed", func(_ *testing.T, f *preparedFixture) { f.git.branch = "feat/new" }},
		{"pending source", "pending worker-owned changes", func(_ *testing.T, f *preparedFixture) { f.git.dirty = " M pending.go" }},
		{"handoff", "handoff changed", func(t *testing.T, f *preparedFixture) { writeFile(t, f.record.Handoff, "new handoff") }},
		{"working report", "newer worker activity", func(t *testing.T, f *preparedFixture) {
			if err := state.AppendStatus(f.home.State, f.meta.ID, "working: still preparing"); err != nil {
				t.Fatal(err)
			}
		}},
		{"unknown dependency", "unknown", func(t *testing.T, f *preparedFixture) {
			if err := os.Remove(state.TaskMetaPath(f.home.State, "package-delivery")); err != nil {
				t.Fatal(err)
			}
		}},
		{"unread ownership", "ownership is unreadable", func(_ *testing.T, f *preparedFixture) { f.tree.Unread = []string{"process ownership"} }},
		{"stale ownership", "ownership is unreadable or stale", func(_ *testing.T, f *preparedFixture) { f.tree.FetchedAt = f.now.Add(-3 * time.Minute) }},
		{"owned shell", "owned shell remains", func(_ *testing.T, f *preparedFixture) {
			f.tree.Children = []fleettree.Node{{Kind: fleettree.KindShell, State: fleettree.Waiting}}
		}},
		{"active gate", "active or parked gate", func(_ *testing.T, f *preparedFixture) {
			f.gate.err = nil
			f.gate.progress = pipeline.Progress{RunID: "protected", Status: "running"}
		}},
		{"parked gate", "active or parked gate", func(_ *testing.T, f *preparedFixture) {
			f.gate.err = nil
			f.gate.progress = pipeline.Progress{RunID: "protected", Status: "awaiting_agent"}
		}},
		{"unread gate", "gate ownership", func(_ *testing.T, f *preparedFixture) { f.gate.err = errors.New("database unreadable") }},
		{"custody", "custody", func(_ *testing.T, f *preparedFixture) { f.gate.custody = pipeline.ErrCustodyHeld }},
		{"not idle", "not idle", func(_ *testing.T, f *preparedFixture) {
			f.service.Options.Dispatch.Idle = func(context.Context, state.TaskMeta) (bool, error) { return false, nil }
		}},
		{"cycle", "cyclic", func(t *testing.T, f *preparedFixture) {
			condition, err := state.NewPauseCondition("dependency", "task:"+f.meta.ID, f.now)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(f.home.State, state.Lifecycle{ID: "package-delivery", Generation: "generation-package", Operation: "pause-delivery", Action: "pause", Phase: "paused", Pause: &condition}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := preparedPackage(t)
			test.change(t, fixture)
			record := fixture.reconcile(t)
			fixture.reopen(t)
			fixture.reconcile(t)
			if calls := fixture.spawner.recorded(); len(calls) != 0 || !strings.Contains(record.Problem, test.reason) {
				t.Fatalf("calls=%v, phase=%s, reason=%q; want %q", calls, record.Phase, record.Problem, test.reason)
			}
			if wakes := fleetWakeRecords(t, fixture.home, "check"); len(wakes) != 1 {
				t.Fatalf("visible refusals=%v, want one durable explanation", wakes)
			}
		})
	}
}

func TestPreparedWaitRechecksBeforeLifecycleAdmission(t *testing.T) {
	for _, change := range []string{"handoff", "ownership", "gate", "dependency finished"} {
		t.Run(change, func(t *testing.T) {
			fixture := preparedPackage(t)
			reads := 0
			fixture.service.Options.PreparedOwnership = func(context.Context, state.TaskMeta) (fleettree.Tree, error) {
				reads++
				if reads == 1 {
					switch change {
					case "handoff":
						writeFile(t, fixture.record.Handoff, "changed while reading")
					case "gate":
						fixture.gate.err = nil
						fixture.gate.progress.Status = "running"
					case "dependency finished":
						fixture.deliver(t)
					}
				}
				tree := fixture.tree
				if reads == 2 && change == "ownership" {
					tree.Children = []fleettree.Node{{Kind: fleettree.KindSubagent, State: fleettree.Working}}
				}
				return tree, nil
			}
			record := fixture.reconcile(t)
			if calls := fixture.spawner.recorded(); len(calls) != 0 {
				t.Fatalf("stale admission called lifecycle: %v, record=%+v", calls, record)
			}
		})
	}
}

func (fixture *preparedFixture) deliver(t *testing.T) {
	t.Helper()
	if err := state.WriteOutcome(fixture.home.State, state.Outcome{ID: "package-delivery", Generation: "generation-package", Phase: "done", Evidence: "synthetic delivery", At: fixture.now}); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedWaitClearedBeforePauseRemainsRunning(t *testing.T) {
	fixture := preparedPackage(t)
	fixture.deliver(t)
	for scan := 0; scan < 3; scan++ {
		fixture.reopen(t)
		if record := fixture.reconcile(t); record.Phase != "cleared" {
			t.Fatalf("record=%+v", record)
		}
	}
	if calls := fixture.spawner.recorded(); len(calls) != 0 {
		t.Fatalf("already delivered dependency parked a worker: %v", calls)
	}
}

func TestPreparedWaitFailureIsDurableWithoutRetry(t *testing.T) {
	fixture := preparedPackage(t)
	fixture.spawner.err, fixture.spawner.during = errors.New("synthetic lifecycle failure"), nil
	for scan := 0; scan < 3; scan++ {
		fixture.reopen(t)
		if record := fixture.reconcile(t); record.Phase != "failed" || !strings.Contains(record.Problem, "synthetic lifecycle failure") {
			t.Fatalf("record=%+v", record)
		}
	}
	if calls := fixture.spawner.recorded(); len(calls) != 1 {
		t.Fatalf("failed admission retried: %v", calls)
	}
}

func TestPreparedPauseResumesOnceAfterDependencyDeliveryAndRestart(t *testing.T) {
	for _, isRestarted := range []bool{false, true} {
		t.Run(map[bool]string{false: "live supervisor", true: "reopened supervisor"}[isRestarted], func(t *testing.T) {
			fixture := preparedPackage(t)
			fixture.reconcile(t)
			fixture.deliver(t)
			if isRestarted {
				fixture.reopen(t)
				fixture.reconcile(t)
			}
			for scan := 0; scan < 2; scan++ {
				if _, err := fixture.service.schedule(t.Context(), fixture.now, &fleetWakes{}, Memory{Available: 8 * gigabyte}, ""); err != nil {
					t.Fatal(err)
				}
				awaitDispatch(t, fixture.service, fixture.spawner, 2)
			}
			calls := fixture.spawner.recorded()
			if len(calls) != 2 || calls[1][0] != "resume" || valueAfter(calls[1], "--operation") != "resume-"+fixture.record.Operation {
				t.Fatalf("calls=%v", calls)
			}
		})
	}
}

func TestPreparedRexCleanupDoesNotInventDelivery(t *testing.T) {
	fixture := preparedPackage(t)
	fixture.deliver(t)
	if err := state.WriteOutcome(fixture.home.State, state.Outcome{ID: "package-delivery", Generation: "generation-package", Phase: "stopped", Reason: "Worktree returned by cleanup", At: fixture.now}); err != nil {
		t.Fatal(err)
	}
	fixture.reconcile(t)
	fixture.reopen(t)
	fixture.reconcile(t)
	if _, err := fixture.service.schedule(t.Context(), fixture.now, &fleetWakes{}, Memory{Available: 8 * gigabyte}, ""); err != nil {
		t.Fatal(err)
	}
	if calls := fixture.spawner.recorded(); len(calls) != 1 || calls[0][0] != "pause" {
		t.Fatalf("cleanup invented delivery: %v", calls)
	}
	outcome, err := state.ReadOutcome(fixture.home.State, "package-delivery")
	if err != nil || outcome.Phase != "stopped" || outcome.Reason != "Worktree returned by cleanup" {
		t.Fatalf("truthful cleanup outcome overwritten: %+v, %v", outcome, err)
	}
}
