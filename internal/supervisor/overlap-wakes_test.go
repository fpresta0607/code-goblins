package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

type overlapForge struct {
	*fakeForge
	activity          string
	activityPages     []string
	activityDeadlines []time.Time
	paths             string
	head              string
	base              string
	activityCalls     int
	diffCalls         int
	failure           string
	moveHead          bool
	moveBase          bool
	onActivity        func()
	requests          []execx.Request
}

func (f *overlapForge) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	f.requests = append(f.requests, request)
	command := request.Name + " " + strings.Join(request.Args, " ")
	switch {
	case strings.HasPrefix(command, "git branch --show-current") && strings.HasPrefix(request.Dir, filepath.Join(f.repo, ".worktrees")):
		return execx.Result{Stdout: []byte(f.branch + "\n")}, nil
	case request.Name == "gh" && strings.Contains(command, "recentIssues"):
		f.activityCalls++
		if deadline, ok := ctx.Deadline(); ok {
			f.activityDeadlines = append(f.activityDeadlines, deadline)
		}
		if f.onActivity != nil {
			f.onActivity()
		}
		if f.failure != "" {
			return execx.Result{ExitCode: 1, Stderr: []byte(f.failure)}, nil
		}
		body := f.activity
		if len(f.activityPages) > 0 {
			body = f.activityPages[0]
			f.activityPages = f.activityPages[1:]
		}
		return execx.Result{Stdout: []byte(body)}, nil
	case strings.HasPrefix(command, "git -C "):
		return execx.Result{Stdout: []byte("https://github.com/o/r.git\n")}, nil
	case strings.HasPrefix(command, "git rev-parse HEAD"):
		return execx.Result{Stdout: []byte(f.head + "\n")}, nil
	case command == "git rev-parse origin/main":
		return execx.Result{Stdout: []byte(f.base + "\n")}, nil
	case strings.HasPrefix(command, "git merge-base "):
		return execx.Result{Stdout: []byte("base\n")}, nil
	case strings.HasPrefix(command, "git diff "):
		f.diffCalls++
		if f.moveHead {
			f.head = "moved"
		}
		if f.moveBase {
			f.base = "moved"
		}
		return execx.Result{Stdout: []byte(f.paths)}, nil
	}
	return f.fakeForge.Run(ctx, request)
}

func overlapActivity(t *testing.T, created time.Time, author string, files []string, issueBody string) string {
	t.Helper()
	authorNode := map[string]string{"login": author, "__typename": "User"}
	fileNodes := []map[string]string{}
	for _, path := range files {
		fileNodes = append(fileNodes, map[string]string{"path": path})
	}
	connection := func(nodes []interface{}) map[string]interface{} {
		return map[string]interface{}{"nodes": nodes, "pageInfo": map[string]interface{}{"hasNextPage": false}}
	}
	pulls := []interface{}{map[string]interface{}{"number": 7, "url": "https://github.com/o/r/pull/7", "title": "retry correction", "createdAt": created, "updatedAt": created, "author": authorNode, "headRefName": "fix/retry", "changedFiles": len(files), "files": map[string]interface{}{"nodes": fileNodes}}}
	issues := []interface{}{}
	if issueBody != "" {
		issues = append(issues, map[string]interface{}{"number": 8, "url": "https://github.com/o/r/issues/8", "title": "Improve project retry handling", "body": issueBody, "createdAt": created, "author": authorNode})
	}
	response := map[string]interface{}{"data": map[string]interface{}{"viewer": map[string]string{"login": "overlord"}, "repository": map[string]interface{}{"defaultBranchRef": map[string]interface{}{"name": "main", "target": map[string]interface{}{"oid": "base", "history": map[string]interface{}{"nodes": []interface{}{}}}}, "recentIssues": map[string]interface{}{"nodes": issues}, "recentPullRequests": map[string]interface{}{"nodes": pulls}, "issues": connection(issues), "pullRequests": connection(pulls), "refs": connection([]interface{}{})}}}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func overlapFixture(t *testing.T) (*Service, home.Home, *overlapForge, state.TaskMeta, time.Time) {
	t.Helper()
	service, h := fleetService(t)
	project := t.TempDir()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	meta := state.TaskMeta{ID: "cg-overlap", Project: project, Worktree: filepath.Join(project, ".worktrees", "gb-cg-overlap"), SpawnGen: fmt.Sprintf("s%d", now.Add(-time.Hour).UnixNano()), Backend: "native", Brief: filepath.Join(h.Data, "cg-overlap", "brief.md")}
	if err := os.MkdirAll(meta.Worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(meta.Brief), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meta.Brief, []byte("## Task\nImprove project retry handling in stale/brief.go.\n## Acceptance criteria\nRetries return actionable results.\n## Constraints\nDo not change other/private.go.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	forge := &overlapForge{fakeForge: forgeFor(project, meta.ID), paths: "app/retry.go\x00", head: "head", base: "default-base"}
	forge.branch, forge.pulls, forge.runs = "feat/retry", "[]", "[]"
	forge.activity = overlapActivity(t, now.Add(-time.Minute), "teammate", []string{"app/retry.go"}, "")
	service.Options.CI = forge
	overlapLive(t, h, meta, now)
	return service, h, forge, meta, now
}

func overlapLive(t *testing.T, h home.Home, meta state.TaskMeta, now time.Time) {
	t.Helper()
	if err := monitor.WriteObservation(h.State, monitor.Observation{TaskID: meta.ID, Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: now, Health: monitor.HealthBusy, Reason: monitor.None, Digest: "working", LastSeen: now, LastProgress: now}); err != nil {
		t.Fatal(err)
	}
}

func TestNewTeammateOverlapWakesOnceAcrossRestartAndAreaChanges(t *testing.T) {
	service, h, forge, meta, now := overlapFixture(t)
	if err := state.AppendStatus(h.State, meta.ID, "done: PR https://github.com/o/r/pull/309"); err != nil {
		t.Fatal(err)
	}
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	wakes := fleetWakeRecords(t, h, "pr")
	if len(wakes) != 1 {
		t.Fatalf("new teammate overlap produced %d wakes, want one: %+v", len(wakes), wakes)
	}
	for _, want := range []string{"pr_overlap:", meta.ID, "teammate", "https://github.com/o/r/pull/7", "app/retry.go", "continue", "wait", "narrow"} {
		if !strings.Contains(wakes[0].Detail, want) {
			t.Errorf("wake %q lacks %q", wakes[0].Detail, want)
		}
	}
	service = &Service{Store: service.Store, Options: service.Options}
	forge.paths = "app/retry.go\x00app/added.go\x00"
	for _, elapsed := range []time.Duration{2 * time.Minute, 10*time.Minute - time.Second, 12 * time.Minute, 8 * 24 * time.Hour} {
		overlapLive(t, h, meta, now.Add(elapsed))
		if err := service.checkFleet(context.Background(), now.Add(elapsed)); err != nil {
			t.Fatal(err)
		}
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 1 {
		t.Fatalf("delivered item/generation repeated: %+v", wakes)
	}
	if forge.activityCalls != 3 {
		t.Fatalf("activity reads = %d, want 3 (none at two minutes)", forge.activityCalls)
	}
}

func TestNewTeammateOverlapUsesStrictSpawnBoundaryAndLaterArea(t *testing.T) {
	for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		t.Run(offset.String(), func(t *testing.T) {
			service, h, forge, meta, now := overlapFixture(t)
			forge.activity = overlapActivity(t, spawnTime(meta.SpawnGen).Add(offset), "teammate", []string{"app/later.go"}, "")
			if err := service.checkFleet(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			forge.paths = "app/later.go\x00"
			overlapLive(t, h, meta, now.Add(11*time.Minute))
			if err := service.checkFleet(context.Background(), now.Add(11*time.Minute)); err != nil {
				t.Fatal(err)
			}
			want := 0
			if offset > 0 {
				want = 1
			}
			if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != want {
				t.Fatalf("created offset %s: wakes = %+v, want %d", offset, wakes, want)
			}
		})
	}
}

func TestNewTeammateOverlapIssuesAndRepositoryScopedExclusion(t *testing.T) {
	for _, repository := range []string{"o/r", "other/repo"} {
		t.Run(repository, func(t *testing.T) {
			service, h, forge, meta, now := overlapFixture(t)
			forge.activity = overlapActivity(t, now.Add(-time.Minute), "teammate", []string{"unrelated.go"}, "app/retry.go PRIVATE BODY MUST NOT COPY")
			if err := tickets.WriteRecord(h.State, tickets.Record{TaskID: meta.ID, Repository: repository, Number: 8}); err != nil {
				t.Fatal(err)
			}
			if err := service.checkFleet(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			want := 1
			if repository == "o/r" {
				want = 0
			}
			wakes := fleetWakeRecords(t, h, "pr")
			if len(wakes) != want {
				t.Fatalf("ticket repository %s: wakes = %+v, want %d", repository, wakes, want)
			}
			if want > 0 && (strings.Contains(wakes[0].Detail, "PRIVATE BODY") || !strings.Contains(wakes[0].Detail, "issue #8")) {
				t.Fatalf("issue wake = %+v", wakes)
			}
		})
	}
}

func TestNewTeammateOverlapExcludesViewerBotsAndStoppedTasks(t *testing.T) {
	for _, author := range []string{"OVERLORD", "dependabot[bot]", "claude", "teammate"} {
		t.Run(author, func(t *testing.T) {
			service, h, forge, meta, now := overlapFixture(t)
			forge.activity = overlapActivity(t, now.Add(-time.Minute), author, []string{"app/retry.go"}, "")
			if author == "teammate" {
				if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Phase: "stopped", Action: "stop", Operation: "test-stop"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.checkFleet(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
				t.Fatalf("excluded work woke: %+v", wakes)
			}
		})
	}
}

func TestNewTeammateOverlapKeepsUnknownTimesAndMovedHeadUnread(t *testing.T) {
	for _, problem := range []string{"item creation", "unparseable creation", "generation", "head moved", "base moved", "viewer", "invalid path"} {
		t.Run(problem, func(t *testing.T) {
			service, h, forge, meta, now := overlapFixture(t)
			switch problem {
			case "item creation":
				forge.activity = overlapActivity(t, time.Time{}, "teammate", []string{"app/retry.go"}, "")
			case "unparseable creation":
				forge.activity = strings.Replace(forge.activity, `"createdAt":"`+now.Add(-time.Minute).Format(time.RFC3339)+`"`, `"createdAt":"unparseable"`, 1)
			case "viewer":
				forge.activity = strings.ReplaceAll(forge.activity, `"login":"overlord"`, `"login":""`)
			case "invalid path":
				forge.paths = "../outside.go\x00"
			case "generation":
				meta.SpawnGen = "unparseable"
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
					t.Fatal(err)
				}
			case "head moved":
				forge.moveHead = true
			case "base moved":
				forge.moveBase = true
			}
			if err := service.checkFleet(context.Background(), now); err == nil || !strings.Contains(err.Error(), "overlap") {
				t.Fatalf("%s was not explicitly unread: %v", problem, err)
			}
			if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
				t.Fatalf("unread sample consumed a wake: %+v", wakes)
			}
			if wakes := fleetWakeRecords(t, h, "ci"); len(wakes) != 0 {
				t.Fatalf("overlap unread misrouted to CI: %+v", wakes)
			}
		})
	}
}

func TestNewTeammateOverlapKeepsUnreadDuringBackoffAndUsesValidPartialItems(t *testing.T) {
	service, h, forge, meta, now := overlapFixture(t)
	complete := forge.activity
	forge.activity = "HTTP/2.0 200 OK\r\nX-RateLimit-Remaining: 0\r\nRetry-After: 600\r\n\r\n" + strings.Replace(forge.activity, `"hasNextPage":false`, `"endCursor":"more","hasNextPage":true`, 1)
	if err := service.checkFleet(context.Background(), now); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("partial unread missing: %v", err)
	}
	if forge.activityCalls != 1 || len(fleetWakeRecords(t, h, "pr")) != 1 {
		t.Fatalf("partial read: calls %d, wakes %+v", forge.activityCalls, fleetWakeRecords(t, h, "pr"))
	}
	service = &Service{Store: service.Store, Options: service.Options}
	overlapLive(t, h, meta, now.Add(2*time.Minute))
	if err := service.checkFleet(context.Background(), now.Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("standing unread lost in backoff: %v", err)
	}
	if forge.activityCalls != 1 || len(fleetWakeRecords(t, h, "pr")) != 1 {
		t.Fatal("restart/backoff repeated a read or item wake")
	}
	data, err := os.ReadFile(fleetWakesPath(h.State))
	if err != nil {
		t.Fatal(err)
	}
	var remembered map[string]json.RawMessage
	if err := json.Unmarshal(data, &remembered); err != nil {
		t.Fatal(err)
	}
	if len(remembered["overlap_unread"]) == 0 {
		t.Fatal("overlap unread evidence was not durably recorded separately")
	}
	forge.activity = complete
	overlapLive(t, h, meta, now.Add(12*time.Minute))
	if err := service.checkFleet(context.Background(), now.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(fleetWakesPath(h.State))
	if err != nil {
		t.Fatal(err)
	}
	var current map[string]json.RawMessage
	if err := json.Unmarshal(data, &current); err != nil {
		t.Fatal(err)
	}
	if len(current["overlap_unread"]) > 0 || len(fleetWakeRecords(t, h, "ci")) != 0 || len(fleetWakeRecords(t, h, "pr")) != 1 {
		t.Fatalf("recovery failed to clear only overlap unread without extra wakes: %s", data)
	}
}

func TestNewTeammateOverlapCancellationConsumesNoNotice(t *testing.T) {
	service, h, forge, meta, now := overlapFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forge.onActivity = cancel
	_ = service.checkFleet(ctx, now)
	if ctx.Err() == nil {
		t.Fatal("activity read did not cancel")
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
		t.Fatalf("canceled read notified: %+v", wakes)
	}
	forge.onActivity = nil
	overlapLive(t, h, meta, now.Add(11*time.Minute))
	if err := service.checkFleet(context.Background(), now.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 1 {
		t.Fatalf("canceled read consumed later valid notice: %+v", wakes)
	}
}

func TestNewTeammateOverlapSharesOneReadAndKeepsGenerationMarks(t *testing.T) {
	service, h, forge, meta, now := overlapFixture(t)
	other := meta
	other.ID, other.Worktree = "cg-other", filepath.Join(meta.Project, ".worktrees", "gb-cg-other")
	if err := os.MkdirAll(other.Worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, other); err != nil {
		t.Fatal(err)
	}
	overlapLive(t, h, other, now)
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if forge.activityCalls != 1 || len(fleetWakeRecords(t, h, "pr")) != 2 {
		t.Fatalf("shared poll: reads %d, wakes %+v", forge.activityCalls, fleetWakeRecords(t, h, "pr"))
	}
	meta.SpawnGen = fmt.Sprintf("s%d", spawnTime(meta.SpawnGen).Add(time.Nanosecond).UnixNano())
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	overlapLive(t, h, meta, now.Add(11*time.Minute))
	overlapLive(t, h, other, now.Add(11*time.Minute))
	if err := service.checkFleet(context.Background(), now.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if forge.activityCalls != 2 || len(fleetWakeRecords(t, h, "pr")) != 3 {
		t.Fatalf("new generation: reads %d, wakes %+v", forge.activityCalls, fleetWakeRecords(t, h, "pr"))
	}
}

func TestNewTeammateOverlapUsesCommittedRenameAndDeletePaths(t *testing.T) {
	service, h, forge, _, now := overlapFixture(t)
	forge.paths = "app/old.go\x00app/new.go\x00app/deleted.go\x00"
	forge.activity = overlapActivity(t, now.Add(-time.Minute), "teammate", []string{"app/old.go", "app/new.go", "app/deleted.go"}, "")
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	wakes := fleetWakeRecords(t, h, "pr")
	if len(wakes) != 1 {
		t.Fatalf("rename/delete wake = %+v", wakes)
	}
	for _, want := range []string{"app/old.go", "app/new.go", "app/deleted.go"} {
		if !strings.Contains(wakes[0].Detail, want) {
			t.Errorf("wake lacks %s", want)
		}
	}
	for _, request := range forge.requests {
		if request.Name == "git" && len(request.Args) > 0 && request.Args[0] == "diff" {
			joined := strings.Join(request.Args, " ")
			for _, want := range []string{"--no-renames", "-z", "base", "head"} {
				if !strings.Contains(joined, want) {
					t.Errorf("committed diff %q lacks %q", joined, want)
				}
			}
			return
		}
	}
	t.Fatal("no committed branch diff was read")
}

func TestNewTeammateOverlapClaimsOnlyItsRepositoryAndLeavesConstraintsOut(t *testing.T) {
	for _, claim := range []string{"https://github.com/o/r/issues/8", "https://github.com/other/repo/issues/8", ""} {
		t.Run(claim, func(t *testing.T) {
			service, h, forge, meta, now := overlapFixture(t)
			brief := "## Task\nImprove project retry handling. " + claim + "\n## Constraints\nDo not change other/private.go.\n"
			if err := os.WriteFile(meta.Brief, []byte(brief), 0600); err != nil {
				t.Fatal(err)
			}
			forge.activity = overlapActivity(t, now.Add(-time.Minute), "teammate", []string{"stale/brief.go"}, "other/private.go")
			if claim == "" {
				forge.activity = strings.ReplaceAll(forge.activity, "Improve project retry handling", "Unrelated teammate work")
			}
			if err := service.checkFleet(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			want := 0
			if strings.Contains(claim, "other/repo") {
				want = 1
			}
			if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != want {
				t.Fatalf("claim %s: wakes %+v want %d", claim, wakes, want)
			}
		})
	}
}

func TestNewTeammateOverlapClaimsItsQueuedRowWhenBriefIsMissing(t *testing.T) {
	service, h, forge, meta, now := overlapFixture(t)
	if err := os.Remove(meta.Brief); err != nil {
		t.Fatal(err)
	}
	row := "## Queued\n- [ ] " + meta.ID + " - Improve project retry handling (repo: project)\n  Resolve issue #8.\n"
	if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(row), 0600); err != nil {
		t.Fatal(err)
	}
	forge.activity = overlapActivity(t, now.Add(-time.Minute), "teammate", []string{"unrelated.go"}, "app/retry.go")
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
		t.Fatalf("own backlog issue woke: %+v", wakes)
	}
}

func TestNewTeammateOverlapRequiresCurrentLiveEvidence(t *testing.T) {
	for _, condition := range []string{"missing", "stale", "paused", "idle", "endpoint mismatch", "before spawn", "other generation paused"} {
		t.Run(condition, func(t *testing.T) {
			service, h, forge, meta, now := overlapFixture(t)
			switch condition {
			case "missing":
				if err := os.Remove(monitor.ObservationPath(h.State, meta.ID)); err != nil {
					t.Fatal(err)
				}
			case "stale":
				overlapLive(t, h, meta, now.Add(-3*time.Minute))
			case "paused":
				if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Action: "pause", Operation: "test-pause", Phase: "paused"}); err != nil {
					t.Fatal(err)
				}
			case "idle":
				if err := monitor.WriteObservation(h.State, monitor.Observation{TaskID: meta.ID, Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: now, Health: monitor.HealthIdle, Reason: monitor.None, Digest: "idle", LastSeen: now, LastProgress: now}); err != nil {
					t.Fatal(err)
				}
			case "endpoint mismatch":
				if err := monitor.WriteObservation(h.State, monitor.Observation{TaskID: meta.ID, Endpoint: "other:pane", EndpointVerdict: monitor.ProbePresent, LastObserved: now, Health: monitor.HealthBusy, Reason: monitor.None, Digest: "busy", LastSeen: now, LastProgress: now}); err != nil {
					t.Fatal(err)
				}
			case "before spawn":
				meta.SpawnGen = fmt.Sprintf("s%d", now.Add(-30*time.Second).UnixNano())
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
					t.Fatal(err)
				}
				overlapLive(t, h, meta, now.Add(-time.Minute))
				forge.activity = overlapActivity(t, now.Add(-10*time.Second), "teammate", []string{"app/retry.go"}, "")
			case "other generation paused":
				if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen + "old", Action: "pause", Operation: "old-pause", Phase: "paused"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.checkFleet(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			want := 0
			if condition == "idle" || condition == "other generation paused" {
				want = 1
			}
			if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != want {
				t.Fatalf("liveness %s wakes %+v want %d", condition, wakes, want)
			}
			if want == 0 && forge.activityCalls != 0 {
				t.Fatalf("non-current live evidence made %d activity reads", forge.activityCalls)
			}
		})
	}
}

func TestNewTeammateOverlapBoundsAllSharedReadPages(t *testing.T) {
	service, _, forge, _, now := overlapFixture(t)
	forge.activityPages = []string{strings.Replace(forge.activity, `"hasNextPage":false`, `"endCursor":"more","hasNextPage":true`, 1), forge.activity}
	started := time.Now()
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(forge.activityDeadlines) != 2 || !forge.activityDeadlines[0].Equal(forge.activityDeadlines[1]) {
		t.Fatalf("page deadlines = %v, want one shared deadline", forge.activityDeadlines)
	}
	if span := forge.activityDeadlines[0].Sub(started); span > 30*time.Second+time.Second || span < 28*time.Second {
		t.Fatalf("repository read bound = %s, want 30s", span)
	}
}

func TestNewTeammateOverlapNeverRepeatsAfterCloseAndReopenOrAreaClears(t *testing.T) {
	service, h, forge, meta, now := overlapFixture(t)
	created := now.Add(-time.Minute)
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	complete := forge.activity
	var envelope struct {
		Data struct {
			Viewer     json.RawMessage            `json:"viewer"`
			Repository map[string]json.RawMessage `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(complete), &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Data.Repository["pullRequests"] = json.RawMessage(`{"nodes":[],"pageInfo":{"hasNextPage":false}}`)
	closed, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	for index, activity := range []string{string(closed), complete, overlapActivity(t, created, "teammate", []string{"unrelated.go"}, ""), complete} {
		forge.activity = activity
		at := now.Add(time.Duration(index+1) * 11 * time.Minute)
		service = &Service{Store: service.Store, Options: service.Options}
		overlapLive(t, h, meta, at)
		if err := service.checkFleet(context.Background(), at); err != nil {
			t.Fatal(err)
		}
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 1 {
		t.Fatalf("item reopen/reappear repeated notice: %+v", wakes)
	}
	if forge.activityCalls != 5 {
		t.Fatalf("close/reopen controls made %d reads, want 5", forge.activityCalls)
	}
}

func TestNewTeammateOverlapRechecksTaskAfterActivityRead(t *testing.T) {
	for _, change := range []string{"generation", "stopped", "missing endpoint"} {
		t.Run(change, func(t *testing.T) {
			service, h, forge, meta, now := overlapFixture(t)
			forge.onActivity = func() {
				var err error
				switch change {
				case "generation":
					meta.SpawnGen = fmt.Sprintf("s%d", now.UnixNano())
					err = state.WriteTaskMeta(h.State, meta)
				case "stopped":
					err = state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Action: "stop", Operation: "stop-during-read", Phase: "stopped"})
				case "missing endpoint":
					err = os.Remove(monitor.ObservationPath(h.State, meta.ID))
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := service.checkFleet(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
				t.Fatalf("%s during read woke an obsolete goblin: %+v", change, wakes)
			}
		})
	}
}

func TestNewTeammateOverlapKeepsUnknownAuthorsUnread(t *testing.T) {
	service, h, forge, _, now := overlapFixture(t)
	var response struct {
		Data struct {
			Viewer     json.RawMessage            `json:"viewer"`
			Repository map[string]json.RawMessage `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(forge.activity), &response); err != nil {
		t.Fatal(err)
	}
	var pulls struct {
		Nodes    []map[string]json.RawMessage `json:"nodes"`
		PageInfo json.RawMessage              `json:"pageInfo"`
	}
	if err := json.Unmarshal(response.Data.Repository["pullRequests"], &pulls); err != nil {
		t.Fatal(err)
	}
	pulls.Nodes[0]["author"] = json.RawMessage("null")
	var err error
	response.Data.Repository["pullRequests"], err = json.Marshal(pulls)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	forge.activity = string(data)
	if err := service.checkFleet(context.Background(), now); err == nil || !strings.Contains(err.Error(), "author") {
		t.Fatalf("unknown author was not explicit unread evidence: %v", err)
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
		t.Fatalf("unknown author inferred a teammate: %+v", wakes)
	}
}

func TestNewTeammateOverlapRecoversAnAcknowledgedNoticeBeforeStateCommit(t *testing.T) {
	service, h, forge, meta, now := overlapFixture(t)
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	records := fleetWakeRecords(t, h, "pr")
	if len(records) != 1 {
		t.Fatalf("first overlap notice = %+v", records)
	}
	if err := wake.AckThrough(h.State, records[0].Seq); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fleetWakesPath(h.State))
	if err != nil {
		t.Fatal(err)
	}
	var remembered map[string]json.RawMessage
	if err := json.Unmarshal(data, &remembered); err != nil {
		t.Fatal(err)
	}
	var notices map[string]map[string]json.RawMessage
	if err := json.Unmarshal(remembered["overlap_notices"], &notices); err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 {
		t.Fatalf("durable notices = %s", remembered["overlap_notices"])
	}
	for _, notice := range notices {
		notice["reported"] = json.RawMessage("false")
	}
	remembered["overlap_notices"], err = json.Marshal(notices)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(remembered)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleetWakesPath(h.State), data, 0600); err != nil {
		t.Fatal(err)
	}
	forge.paths = "app/retry.go\x00app/added.go\x00"
	forge.activity = overlapActivity(t, now.Add(-time.Minute), "teammate", []string{"app/retry.go", "app/added.go"}, "")
	service = &Service{Store: service.Store, Options: service.Options}
	overlapLive(t, h, meta, now.Add(11*time.Minute))
	if err := service.checkFleet(context.Background(), now.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if records := fleetWakeRecords(t, h, "pr"); len(records) != 0 {
		t.Fatalf("acknowledged notice repeated after interrupted state commit and changed content: %+v", records)
	}
}

func TestNewTeammateOverlapKeepsMarksWhileTaskMetadataIsUnread(t *testing.T) {
	service, h, forge, meta, now := overlapFixture(t)
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	wakes := fleetWakeRecords(t, h, "pr")
	if len(wakes) != 1 {
		t.Fatalf("initial notice = %+v", wakes)
	}
	if err := wake.AckThrough(h.State, wakes[0].Seq); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.State, meta.ID+".meta")
	if err := os.WriteFile(path, []byte("not task metadata\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service = &Service{Store: service.Store, Options: service.Options}
	if err := service.checkFleet(context.Background(), now.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	remembered, err := readFleetWakes(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(remembered.OverlapNotices) != 1 {
		t.Fatalf("unread metadata erased durable notices: %+v", remembered.OverlapNotices)
	}
	for _, notice := range remembered.OverlapNotices {
		if !notice.Reported || notice.Detail != wakes[0].Detail {
			t.Fatalf("unread metadata altered first proved notice: %+v", notice)
		}
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	forge.paths = "app/retry.go\x00app/added.go\x00"
	forge.activity = overlapActivity(t, now.Add(-time.Minute), "teammate", []string{"app/retry.go", "app/added.go"}, "")
	service = &Service{Store: service.Store, Options: service.Options}
	overlapLive(t, h, meta, now.Add(22*time.Minute))
	if err := service.checkFleet(context.Background(), now.Add(22*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
		t.Fatalf("restored same generation repeated notice: %+v", wakes)
	}
}

func TestNewTeammateOverlapUsesReadableItemWhenCollaborationMetadataFails(t *testing.T) {
	for _, kind := range []string{"pr", "issue"} {
		for _, isRecent := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/recent%t", kind, isRecent), func(t *testing.T) {
				service, h, forge, meta, now := overlapFixture(t)
				meta.SpawnGen = fmt.Sprintf("s%d", now.Add(-60*24*time.Hour).UnixNano())
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
					t.Fatal(err)
				}
				created := now.Add(-time.Minute)
				if !isRecent {
					created = now.Add(-31 * 24 * time.Hour)
				}
				files, issueBody := []string{"app/retry.go"}, ""
				if kind == "issue" {
					files, issueBody = []string{"unrelated.go"}, "app/retry.go"
				}
				var response struct {
					Errors []struct {
						Message string `json:"message"`
					} `json:"errors"`
					Data struct {
						Viewer     json.RawMessage            `json:"viewer"`
						Repository map[string]json.RawMessage `json:"repository"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(overlapActivity(t, created, "teammate", files, issueBody)), &response); err != nil {
					t.Fatal(err)
				}
				response.Errors = append(response.Errors, struct {
					Message string `json:"message"`
				}{Message: "collaboration history unavailable"})
				for _, field := range []string{"recentIssues", "recentPullRequests", "defaultBranchRef"} {
					response.Data.Repository[field] = json.RawMessage("null")
				}
				data, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				forge.activity = string(data)
				if err := service.checkFleet(context.Background(), now); err == nil || !strings.Contains(err.Error(), "collaboration history unavailable") {
					t.Fatalf("failed metadata was not honestly unread: %v", err)
				}
				want := 0
				if isRecent {
					want = 1
				}
				if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != want {
					t.Fatalf("readable %s recent=%t wakes = %+v, want %d", kind, isRecent, wakes, want)
				}
			})
		}
	}
}
