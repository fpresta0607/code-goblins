package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	// churnHead moves the head on every diff, as a goblin committing
	// through both reads would; nextBranch is the branch the goblin
	// switches to once its branch was first read.
	churnHead  bool
	nextBranch string
	// headDeadlines are the deadlines the worktree's HEAD was read under.
	headDeadlines []time.Time
	onActivity        func()
	requests          []execx.Request
}

func (f *overlapForge) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	f.requests = append(f.requests, request)
	command := request.Name + " " + strings.Join(request.Args, " ")
	switch {
	case strings.HasPrefix(command, "git branch --show-current") && strings.HasPrefix(request.Dir, filepath.Join(f.repo, ".worktrees")):
		branch := f.branch
		if f.nextBranch != "" {
			f.branch, f.nextBranch = f.nextBranch, ""
		}
		return execx.Result{Stdout: []byte(branch + "\n")}, nil
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
		if deadline, ok := ctx.Deadline(); ok {
			f.headDeadlines = append(f.headDeadlines, deadline)
		}
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
		if f.churnHead {
			f.head += "+"
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
	for _, problem := range []string{"item creation", "unparseable creation", "generation", "viewer", "invalid path"} {
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

// On 2026-10-08 the board showed "overlap read of C:\dev\code-goblins
// incomplete: cg-afk-report branch area: branch head, base or checkout changed
// during the read" for ten minutes: the goblin had made a new branch after the
// poll first read its branch. A supervisor read that races a goblin's own git
// work is the goblin working, never an error: its area is read on the branch it
// names itself, a move during the read is read again once, and a goblin still
// moving is left for the next pass.
func TestOverlapReadOfAGoblinMovingItsBranchIsNeverAnError(t *testing.T) {
	for _, move := range []string{"a new branch after the poll read it", "head moved once", "base moved once", "head moving through both reads"} {
		t.Run(move, func(t *testing.T) {
			// Arrange
			service, h, forge, _, now := overlapFixture(t)
			switch move {
			case "a new branch after the poll read it":
				forge.nextBranch = "feat/retry-fast"
			case "head moved once":
				forge.moveHead = true
			case "base moved once":
				forge.moveBase = true
			case "head moving through both reads":
				forge.churnHead = true
			}

			// Act
			err := service.checkFleet(context.Background(), now)

			// Assert
			if err != nil {
				t.Fatalf("a goblin moving its own branch was reported: %v", err)
			}
			persisted, readErr := readFleetWakes(h.State)
			if readErr != nil || len(persisted.OverlapUnread) != 0 {
				t.Fatalf("overlap unread = %+v, %v; want none", persisted.OverlapUnread, readErr)
			}
			wakes := fleetWakeRecords(t, h, "pr")
			if move == "head moving through both reads" {
				if len(wakes) != 0 {
					t.Fatalf("a goblin still moving was read: %+v", wakes)
				}
				return
			}
			if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "pr_overlap") {
				t.Fatalf("overlap wakes = %+v, want the teammate's overlap found on the re-read", wakes)
			}
			if move == "a new branch after the poll read it" && !strings.Contains(wakes[0].Detail, "feat/retry-fast") {
				t.Fatalf("overlap wake %q does not name the branch the goblin moved to", wakes[0].Detail)
			}
		})
	}
}

// The same read at 01:34:16Z also said "cg-voice-at-install branch area:
// context deadline exceeded": every goblin's git reads ran on the one
// 30-second deadline of the GitHub read before them, at 100 to 200 ms a git
// call, so the goblins read last ran out of time. Each goblin's area is read
// on a deadline of its own.
func TestOverlapReadGivesEachGoblinsAreaADeadlineOfItsOwn(t *testing.T) {
	// Arrange
	service, _, forge, _, now := overlapFixture(t)
	forge.onActivity = func() { time.Sleep(20 * time.Millisecond) }

	// Act
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(forge.activityDeadlines) == 0 || len(forge.headDeadlines) == 0 {
		t.Fatalf("activity deadlines %v, head deadlines %v; want both read", forge.activityDeadlines, forge.headDeadlines)
	}
	if shared := forge.activityDeadlines[0]; !forge.headDeadlines[0].After(shared) {
		t.Fatalf("the goblin's area was read under the GitHub read's deadline %v (its own: %v)", shared, forge.headDeadlines[0])
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

// Each page of the shared read is bounded by thirty seconds of its own, so a
// slow page never leaves the next one less time.
func TestNewTeammateOverlapBoundsEachSharedReadPage(t *testing.T) {
	service, _, forge, _, now := overlapFixture(t)
	forge.activityPages = []string{strings.Replace(forge.activity, `"hasNextPage":false`, `"endCursor":"more","hasNextPage":true`, 1), forge.activity}
	forge.onActivity = func() { time.Sleep(20 * time.Millisecond) }
	started := time.Now()
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(forge.activityDeadlines) != 2 || !forge.activityDeadlines[1].After(forge.activityDeadlines[0]) {
		t.Fatalf("page deadlines = %v, want each page on a deadline of its own", forge.activityDeadlines)
	}
	if span := forge.activityDeadlines[0].Sub(started); span > 30*time.Second+time.Second || span < 28*time.Second {
		t.Fatalf("page read bound = %s, want 30s", span)
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

func TestOverlapReadPutsTheTeammatesInAGoblinsAreaOnItsCardWheneverTheirWorkOpened(t *testing.T) {
	// Arrange: a teammate's pull request and issue in the goblin's area, both
	// opened before it started, so neither wakes the CFO.
	service, h, forge, meta, now := overlapFixture(t)
	opened := spawnTime(meta.SpawnGen).Add(-time.Hour)
	forge.activity = strings.ReplaceAll(overlapActivity(t, opened, "teammate", []string{"app/retry.go"}, "app/retry.go times out"), `"login":"teammate"`, `"avatarUrl":"https://avatars.githubusercontent.com/u/7","login":"teammate"`)
	shown := func(at time.Time) []Overlap {
		t.Helper()
		if err := service.checkFleet(context.Background(), at); err != nil {
			t.Fatal(err)
		}
		service.cycle(context.Background(), false)
		snapshot, err := service.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == meta.ID })
		if index < 0 {
			t.Fatalf("tasks = %+v, want %s", snapshot.Tasks, meta.ID)
		}
		return snapshot.Tasks[index].Overlaps
	}

	// Act
	whileLive := shown(now)
	afterItStopped := shown(now.Add(11 * time.Minute))

	// Assert
	teammate := tickets.Actor{Login: "teammate", AvatarURL: "https://avatars.githubusercontent.com/u/7"}
	want := []Overlap{{Actor: teammate, What: "PR #7", URL: "https://github.com/o/r/pull/7"}, {Actor: teammate, What: "issue #8", URL: "https://github.com/o/r/issues/8"}}
	if !slices.Equal(whileLive, want) {
		t.Fatalf("overlaps on the card = %+v, want %+v", whileLive, want)
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 0 {
		t.Fatalf("wakes = %+v, want none for work opened before the goblin started", wakes)
	}
	if len(afterItStopped) != 0 {
		t.Fatalf("overlaps once the goblin is no longer live = %+v, want none", afterItStopped)
	}
}

func TestOverlapReadLeavesTheGoblinsOwnTicketOffItsCard(t *testing.T) {
	// Arrange: issue #8 names the goblin's area, and it is the goblin's own
	// ticket.
	service, h, forge, meta, now := overlapFixture(t)
	forge.activity = overlapActivity(t, now.Add(-time.Minute), "teammate", []string{"unrelated.go"}, "app/retry.go times out")
	if err := tickets.WriteRecord(h.State, tickets.Record{TaskID: meta.ID, Repository: "o/r", Number: 8, State: tickets.InProgress}); err != nil {
		t.Fatal(err)
	}

	// Act
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	w, err := readFleetWakes(h.State)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if area, ok := w.SameArea[meta.ID]; !ok || len(area.Overlaps) != 0 {
		t.Fatalf("same area = %+v, %v, want the read kept with nothing on the card: issue #8 is the goblin's own ticket", area, ok)
	}
}

func TestOverlapReadForgetsTheCardOfACleanedUpGoblin(t *testing.T) {
	// Arrange
	service, h, _, meta, now := overlapFixture(t)
	if err := service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if w, err := readFleetWakes(h.State); err != nil || len(w.SameArea[meta.ID].Overlaps) == 0 {
		t.Fatalf("same area = %+v, %v, want the teammate's pull request kept first", w.SameArea, err)
	}
	if err := state.RemoveTaskMeta(h.State, meta.ID); err != nil {
		t.Fatal(err)
	}

	// Act
	if err := service.checkFleet(context.Background(), now.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	w, err := readFleetWakes(h.State)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if area, ok := w.SameArea[meta.ID]; ok {
		t.Fatalf("same area = %+v, want nothing kept for a goblin the fleet no longer has", area)
	}
}

// pagedGitHub answers the overlap read's GraphQL rounds as GitHub would for a
// repository with branches branches and pulls open pull requests: the first
// round's metadata, and a page of each connection a round asks for after the
// cursor it names, a hundred branches and fifty pull requests at a time. The
// oldest pull request, #1, is the teammate's that changes the goblin's file.
// timeouts names the pull request cursors whose page times out, and how many
// more times it does, and rounds holds what each round asked and its
// deadline.
type pagedGitHub struct {
	*overlapForge
	branches, pulls int
	created         time.Time
	timeouts        map[string]int
	rounds          []pagedRound
}

type pagedRound struct {
	flags, cursors map[string]string
	deadline       time.Time
}

func pagedFor(forge *overlapForge, branches, pulls int, created time.Time) *pagedGitHub {
	return &pagedGitHub{overlapForge: forge, branches: branches, pulls: pulls, created: created, timeouts: map[string]int{}}
}

func (g *pagedGitHub) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "gh" || !strings.Contains(strings.Join(request.Args, " "), "recentIssues") {
		return g.overlapForge.Run(ctx, request)
	}
	round := pagedRound{flags: map[string]string{}, cursors: map[string]string{}}
	round.deadline, _ = ctx.Deadline()
	// Windows reads the time a clock tick at a time, so two pages read within
	// one tick would share a deadline whether or not each had its own.
	time.Sleep(20 * time.Millisecond)
	for i := 0; i+1 < len(request.Args); i++ {
		name, value, _ := strings.Cut(request.Args[i+1], "=")
		switch request.Args[i] {
		case "-F":
			round.flags[name] = value
		case "-f":
			if strings.HasSuffix(name, "After") {
				round.cursors[name] = value
			}
		}
	}
	g.rounds = append(g.rounds, round)
	if cursor := round.cursors["pullsAfter"]; round.flags["pulls"] == "true" && g.timeouts[cursor] > 0 {
		g.timeouts[cursor]--
		return execx.Result{}, context.DeadlineExceeded
	}
	page := func(cursor, prefix string, size, total int) (int, int, map[string]interface{}) {
		start := 0
		if cursor != "" {
			start, _ = strconv.Atoi(strings.TrimPrefix(cursor, prefix))
		}
		end := min(start+size, total)
		return start, end, map[string]interface{}{"hasNextPage": end < total, "endCursor": fmt.Sprintf("%s%d", prefix, end)}
	}
	repository := map[string]interface{}{"nameWithOwner": "o/r"}
	response := map[string]interface{}{"repository": repository}
	if round.flags["first"] == "true" {
		response["viewer"] = map[string]string{"login": "overlord"}
		repository["defaultBranchRef"] = map[string]interface{}{"name": "main", "target": map[string]interface{}{"oid": "base", "history": map[string]interface{}{"nodes": []interface{}{}}}}
		repository["recentIssues"] = map[string]interface{}{"nodes": []interface{}{}}
		repository["recentPullRequests"] = map[string]interface{}{"nodes": []interface{}{}}
	}
	if round.flags["issues"] == "true" {
		repository["issues"] = map[string]interface{}{"pageInfo": map[string]interface{}{"hasNextPage": false}, "nodes": []interface{}{}}
	}
	if round.flags["pulls"] == "true" {
		start, end, info := page(round.cursors["pullsAfter"], "P", 50, g.pulls)
		var nodes []interface{}
		for i := start; i < end; i++ {
			number := g.pulls - i
			path := fmt.Sprintf("other/file-%d.go", number)
			if number == 1 {
				path = "app/retry.go"
			}
			nodes = append(nodes, map[string]interface{}{"number": number, "url": fmt.Sprintf("https://github.com/o/r/pull/%d", number), "title": "teammate work", "createdAt": g.created, "updatedAt": g.created,
				"author": map[string]string{"login": "teammate", "__typename": "User"}, "headRefName": fmt.Sprintf("feat/%d", number), "changedFiles": 1, "files": map[string]interface{}{"nodes": []interface{}{map[string]string{"path": path}}}})
		}
		repository["pullRequests"] = map[string]interface{}{"pageInfo": info, "nodes": nodes}
	}
	if round.flags["refs"] == "true" {
		start, end, info := page(round.cursors["refsAfter"], "R", 100, g.branches)
		var nodes []interface{}
		for i := start; i < end; i++ {
			nodes = append(nodes, map[string]interface{}{"name": fmt.Sprintf("goblin/branch-%04d", i), "target": map[string]interface{}{"oid": fmt.Sprintf("b%04d", i), "committedDate": g.created, "author": map[string]interface{}{"name": "Overlord", "user": map[string]string{"login": "overlord"}}}})
		}
		repository["refs"] = map[string]interface{}{"pageInfo": info, "nodes": nodes}
	}
	body, err := json.Marshal(map[string]interface{}{"data": response})
	return execx.Result{Stdout: body}, err
}

// overlapErrorWakes returns the supervisor_error wakes that name the overlap
// read.
func overlapErrorWakes(t *testing.T, s *Service) []string {
	t.Helper()
	var wakes []string
	for _, detail := range supervisorErrorWakes(t, s) {
		if strings.Contains(detail, "overlap read") {
			wakes = append(wakes, detail)
		}
	}
	return wakes
}

// On 2026-10-08 the overlap read of fpresta0607/code-goblins, which has 403
// branches, paged through every one of them under one 30-second deadline
// every ten minutes, timed out after three or four pages and woke the CFO
// each time. It reads only what can meet a goblin's area, so a repository
// with ten times the branches costs it the same few pages.
func TestOverlapReadOfARepositoryWithHundredsOfBranchesReadsTheSameFewPages(t *testing.T) {
	for _, branches := range []int{403, 4030} {
		t.Run(fmt.Sprintf("%d branches", branches), func(t *testing.T) {
			// Arrange
			service, h, forge, _, now := overlapFixture(t)
			github := pagedFor(forge, branches, 1, now.Add(-time.Minute))
			service.Options.CI = github

			// Act
			err := service.checkFleet(context.Background(), now)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(github.rounds) != 1 {
				t.Fatalf("the overlap read asked GitHub %d times for a repository with %d branches, want one page: %+v", len(github.rounds), branches, github.rounds)
			}
			if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "PR #1") {
				t.Fatalf("pr wakes = %+v, want the teammate's pull request that meets the goblin's area", wakes)
			}
		})
	}
}

// A page that times out costs its own pass and never the read: each page
// runs on a deadline of its own, the next poll asks for the page that timed
// out where the read left off, and the CFO hears nothing of it.
func TestOverlapReadThatTimesOutOncePicksUpWhereItLeftOffWithoutWakingTheCFO(t *testing.T) {
	// Arrange
	service, h, forge, meta, now := overlapFixture(t)
	service.work, service.subscribers = make(chan struct{}, 1), map[chan struct{}]struct{}{}
	github := pagedFor(forge, 403, 120, now.Add(-time.Minute))
	github.timeouts["P50"] = 1
	service.Options.CI = github

	// Act
	for _, at := range []time.Time{now, now.Add(ciPollEvery)} {
		overlapLive(t, h, meta, at)
		_ = service.checkFleet(context.Background(), at)
		service.cycle(context.Background(), true)
	}

	// Assert
	if wakes := overlapErrorWakes(t, service); len(wakes) != 0 {
		t.Fatalf("one page that timed out woke the CFO: %q", wakes)
	}
	var asked []string
	for _, round := range github.rounds {
		asked = append(asked, "first="+round.flags["first"]+" after="+round.cursors["pullsAfter"])
	}
	if want := []string{"first=true after=", "first=false after=P50", "first=false after=P50", "first=false after=P100"}; !slices.Equal(asked, want) {
		t.Fatalf("pages asked = %q, want the page that timed out asked again on the next poll and nothing read twice: %q", asked, want)
	}
	for i := 1; i < len(github.rounds); i++ {
		if !github.rounds[i].deadline.After(github.rounds[i-1].deadline) {
			t.Fatalf("page %d ran on deadline %v, page %d on %v: want each page on a deadline of its own", i, github.rounds[i-1].deadline, i+1, github.rounds[i].deadline)
		}
	}
	if wakes := fleetWakeRecords(t, h, "pr"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "PR #1") {
		t.Fatalf("pr wakes = %+v, want the teammate's pull request on the last page", wakes)
	}
	if w, err := readFleetWakes(h.State); err != nil || len(w.OverlapUnread) != 0 {
		t.Fatalf("overlap unread = %v, %v, want nothing once the read finished", w.OverlapUnread, err)
	}
}

// A read that keeps failing still reaches the CFO, once it failed on three
// passes in a row.
func TestOverlapReadThatKeepsFailingWakesTheCFOOnTheThirdPassInARow(t *testing.T) {
	// Arrange
	service, h, forge, meta, now := overlapFixture(t)
	service.work, service.subscribers = make(chan struct{}, 1), map[chan struct{}]struct{}{}
	forge.failure = `Post "https://api.github.com/graphql": context deadline exceeded`

	for pass, want := range []int{0, 0, 1} {
		at := now.Add(time.Duration(pass) * ciPollEvery)
		overlapLive(t, h, meta, at)

		// Act
		_ = service.checkFleet(context.Background(), at)
		service.cycle(context.Background(), true)

		// Assert
		if wakes := overlapErrorWakes(t, service); len(wakes) != want {
			t.Fatalf("after %d failing passes the CFO had %d overlap wakes, want %d: %q", pass+1, len(wakes), want, wakes)
		}
	}
	if forge.activityCalls != 3 {
		t.Fatalf("the failing read was asked %d times in three polls, want once a poll", forge.activityCalls)
	}
}
