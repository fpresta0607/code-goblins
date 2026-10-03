package supervisor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSubscriptionVisibilityRequiresLiveHarnessEvidence(t *testing.T) {
	now := time.Now().UTC()
	liveTask := Task{ID: "goblin", Harness: "codex", Generation: "g1", Session: "session", Runtime: RuntimeEvidence{State: "busy", At: now}}
	for _, test := range []struct {
		name     string
		cfo      cfoState
		tasks    []Task
		sessions []Session
		want     string
	}{
		{"CFO only", cfoState{registered: true, harness: "claude"}, nil, nil, "claude"},
		{"goblin only", cfoState{}, []Task{liveTask}, nil, "codex"},
		{"both", cfoState{registered: true, harness: "claude"}, []Task{liveTask}, nil, "claude,codex"},
		{"idle harness", cfoState{}, []Task{{Harness: "claude", Generation: "g1", Runtime: RuntimeEvidence{State: "idle", At: now}}}, nil, "claude"},
		{"weekly-limited harness remains visible", cfoState{}, []Task{{Harness: "claude", Generation: "g1", Runtime: RuntimeEvidence{State: "harness-erroring", At: now}}}, nil, "claude"},
		{"configured CFO", cfoState{harness: "claude"}, nil, nil, ""},
		{"lost CFO", cfoState{registered: true, harness: "claude", problem: "terminal ended"}, nil, nil, ""},
		{"foreign session", cfoState{}, nil, []Session{{Role: "foreign", Harness: "claude", Phase: "active"}}, ""},
		{"saved model", cfoState{}, []Task{{Model: "claude-opus", Generation: "g1", Runtime: liveTask.Runtime}}, nil, ""},
		{"paused", cfoState{}, []Task{{Harness: "claude", Generation: "g1", Runtime: RuntimeEvidence{State: "paused", At: now}}}, nil, ""},
		{"ended", cfoState{}, []Task{liveTask}, []Session{{ID: "session", TaskID: "goblin", Generation: "g1", Phase: "ended"}}, ""},
		{"canonical paused session", cfoState{}, []Task{liveTask}, []Session{{ID: "session", TaskID: "goblin", Generation: "g1", Phase: "paused"}}, ""},
		{"obsolete ended session", cfoState{}, []Task{liveTask}, []Session{{ID: "old-session", TaskID: "goblin", Generation: "g1", Phase: "ended"}}, "codex"},
		{"stale", cfoState{}, []Task{{Harness: "claude", Generation: "g1", Runtime: RuntimeEvidence{State: "active", At: now.Add(-3 * time.Minute)}}}, nil, ""},
		{"archived", cfoState{}, []Task{{Harness: "claude", Generation: "g1", Archived: true, Runtime: liveTask.Runtime}}, nil, ""},
		{"launching host alone", cfoState{}, []Task{{Harness: "claude", Generation: "g1", Runtime: RuntimeEvidence{State: "launching", At: now}}}, nil, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &Service{}
			usage := service.subscriptionUsage(test.cfo, Snapshot{At: now, Tasks: test.tasks, Sessions: test.sessions})
			providers := []string{}
			for _, provider := range usage {
				providers = append(providers, provider.Provider)
				if provider.Status != "unavailable" || provider.PercentRemaining != nil {
					t.Fatalf("missing measurement = %+v", provider)
				}
			}
			if strings.Join(providers, ",") != test.want {
				t.Fatalf("providers = %v, want %s", providers, test.want)
			}
		})
	}
}

func TestSnapshotHidesPausedSessionsBeforeCachedLivenessChanges(t *testing.T) {
	for _, health := range []monitor.Health{monitor.HealthIdle, monitor.HealthActive} {
		t.Run(string(health), func(t *testing.T) {
			store, h := testStore(t)
			if err := store.Accept(event(t, h, "SessionStart", "session", "", time.Now().Add(-time.Second))); err != nil {
				t.Fatal(err)
			}
			observation := monitor.Observation{Schema: monitor.Schema, TaskID: "task-1", Endpoint: "test:p1", EndpointVerdict: monitor.ProbePresent, Health: health, Reason: monitor.None, LastObserved: time.Now().UTC()}
			observation.Digest, observation.LastSeen, observation.LastProgress = strings.Repeat("a", 64), observation.LastObserved, observation.LastObserved
			if err := monitor.WriteObservation(h.State, observation); err != nil {
				t.Fatal(err)
			}
			service := &Service{Store: store}
			before, err := service.Snapshot()
			if err != nil || len(before.Subscriptions) != 1 {
				t.Fatalf("live before pause = %+v, %v", before.Subscriptions, err)
			}

			now := time.Now().UTC()
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: "task-1", Generation: "g1", Operation: "pause-task-1", Action: "pause", Phase: "paused", Started: now, Updated: now}); err != nil {
				t.Fatal(err)
			}
			after, err := service.Snapshot()
			if err != nil || len(after.Subscriptions) != 0 {
				t.Fatalf("paused provider still shown: %+v, %v", after.Subscriptions, err)
			}
			if len(after.Sessions) != 1 || after.Sessions[0].Phase != "paused" || after.Sessions[0].Runtime.State != "paused" {
				t.Fatalf("canonical paused snapshot = %+v", after.Sessions)
			}
			cached, err := monitor.ReadObservation(h.State, "task-1")
			if err != nil || cached.Health != health {
				t.Fatalf("test changed cached liveness instead of honoring the pause: %+v, %v", cached, err)
			}
		})
	}
}

func TestSnapshotDoesNotWaitForQuotaRefresh(t *testing.T) {
	store, _ := testStore(t)
	started := make(chan struct{})
	service := &Service{Store: store, Options: Options{Quota: func(ctx context.Context) (quota.Report, string) {
		if _, isBounded := ctx.Deadline(); !isBounded {
			t.Error("quota refresh has no deadline")
		}
		close(started)
		<-ctx.Done()
		return quota.Report{}, "private provider failure payload"
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); service.keepSubscriptionUsage(ctx, time.Minute) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not begin")
	}

	began := time.Now()
	for range 3 {
		if _, err := service.Snapshot(); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(began); elapsed > time.Second {
		t.Fatalf("snapshots waited for quota: %s", elapsed)
	}
}

func TestSnapshotShowsUsageOnlyWhileTheFixtureGoblinIsLive(t *testing.T) {
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	probe := &runtimeProbe{sample: monitor.EndpointSample{
		Verdict: monitor.ProbePresent, Agent: herdr.AgentAlive, Status: herdr.AgentWorking, Busy: herdr.BusyWorking,
		Endpoint: herdr.Endpoint{Target: herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}, WorkspaceID: meta.HerdrWorkspaceID, TabID: meta.HerdrTabID, PaneID: meta.HerdrPaneID},
		TabLabel: "gb-" + meta.ID, Capture: []byte("fixture goblin is working"),
	}}
	observer := monitor.Service{StateDir: h.State, Probe: probe}
	if _, err := observer.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store}
	claudeRemaining, codexRemaining := 0.0, 75.0
	now := time.Now().UTC()
	service.subscriptionReadings = map[string]quota.WeeklyReading{
		"claude": {Status: "available", PercentRemaining: &claudeRemaining, ReadAt: now, ResetsAt: now.Add(time.Hour), Source: "oauth"},
		"codex":  {Status: "available", PercentRemaining: &codexRemaining, ReadAt: now, ResetsAt: now.Add(time.Hour), Source: "oauth"},
	}
	view, err := service.Snapshot()
	if err != nil || len(view.Subscriptions) != 1 || view.Subscriptions[0].Provider != "codex" || view.Subscriptions[0].PercentRemaining == nil || *view.Subscriptions[0].PercentRemaining != 75 {
		t.Fatalf("live fixture usage = %+v, %v", view.Subscriptions, err)
	}

	probe.sample = monitor.EndpointSample{Verdict: monitor.ProbeMissing}
	if _, err := observer.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err = service.Snapshot()
	if err != nil || len(view.Subscriptions) != 0 {
		t.Fatalf("ended fixture usage = %+v, %v", view.Subscriptions, err)
	}
}

func TestSubscriptionRefreshProjectsOnlySafeWeeklyFields(t *testing.T) {
	now := time.Now().UTC()
	service := &Service{Options: Options{Quota: func(context.Context) (quota.Report, string) {
		return quota.Report{GeneratedAt: now, Providers: map[string]quota.Provider{"codex": {Source: "oauth", Status: "fresh", Known: true, Windows: []quota.Window{{ID: "weekly", PercentUsed: 24, ResetsAt: now.Add(24 * time.Hour)}}, Resets: map[string]time.Time{"weekly": now.Add(24 * time.Hour)}, Credits: &quota.Credits{Remaining: 999, Unit: "private-balance"}}}}, ""
	}}}
	service.refreshSubscriptionUsage(context.Background())
	view := Snapshot{At: now}
	usage := service.subscriptionUsage(cfoState{registered: true, harness: "codex"}, view)
	if len(usage) != 1 || usage[0].PercentRemaining == nil || *usage[0].PercentRemaining != 76 {
		t.Fatalf("usage = %+v", usage)
	}
	data, err := json.Marshal(usage)
	if err != nil || strings.Contains(string(data), "private-balance") || strings.Contains(string(data), "999") {
		t.Fatalf("unsafe projection: %s, %v", data, err)
	}

	view.At = now.Add(2 * time.Hour)
	usage = service.subscriptionUsage(cfoState{registered: true, harness: "codex"}, view)
	if usage[0].Status != "stale" || usage[0].PercentRemaining != nil {
		t.Fatalf("cache age concealed: %+v", usage)
	}

	service.Options.Quota = func(context.Context) (quota.Report, string) { return quota.Report{}, "secret-token-error" }
	service.refreshSubscriptionUsage(context.Background())
	usage = service.subscriptionUsage(cfoState{registered: true, harness: "codex"}, Snapshot{At: now})
	if usage[0].Status != "unavailable" || usage[0].PercentRemaining != nil {
		t.Fatalf("failed read retained a number: %+v", usage)
	}
	data, _ = json.Marshal(usage)
	if strings.Contains(string(data), "secret-token-error") {
		t.Fatalf("provider error escaped: %s", data)
	}
}
