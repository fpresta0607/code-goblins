package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestScratchHomeVirtualDayResumesThreeReasonsWithoutReplacingWork(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	day := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	head := strings.Repeat("a", 40)
	pull := "https://github.com/owner/repo/pull/42"
	controller := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
		Prepare: func(_ context.Context, _ state.TaskMeta, path string) error {
			return os.WriteFile(path, []byte("Retain the unfinished work and the saved branch."), 0o600)
		},
		Stop: func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) {
			return []string{"scratch process fixture"}, nil
		},
		Memory: func() (uint64, uint64, error) { return 8 * gigabyte, 8 * gigabyte, nil },
		Resume: func(_ context.Context, meta state.TaskMeta, prior state.Lifecycle) error {
			if prior.Session != "saved-"+meta.ID || !prior.HandoffSaved {
				t.Errorf("resume lost the session or handoff: %+v", prior)
			}
			return nil
		},
		Notify: func(record state.Lifecycle) error { return lifecycle.Report(h.State, record) },
	}}
	for _, task := range []struct{ id, reason, until string }{
		{"memory-task", "memory", ""},
		{"dependency-task", "dependency", "pr:" + pull},
		{"ci-task", "ci", "pr:" + pull + "@" + head},
	} {
		meta := state.TaskMeta{ID: task.id, SpawnGen: "generation-" + task.id, Backend: "native", Harness: "codex", Project: h.Root, Worktree: filepath.Join(h.Root, ".worktrees", "gb-"+task.id), TaskTmp: filepath.Join(h.State, "tasktmp", task.id)}
		writeFile(t, filepath.Join(meta.Worktree, "unfinished.txt"), "Uncommitted work survives the whole day.")
		if err := os.MkdirAll(meta.TaskTmp, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := state.WriteTaskMeta(h.State, meta); err != nil {
			t.Fatal(err)
		}
		record, err := controller.Run(t.Context(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-" + meta.ID, Action: "pause", Reason: task.reason, Until: task.until, Session: "saved-" + meta.ID})
		if err != nil {
			t.Fatal(err)
		}
		record.Pause.At = day
		if err := state.WriteLifecycle(h.State, record); err != nil {
			t.Fatal(err)
		}
	}
	handler.Service.Options.Dispatch.Spawn = func(ctx context.Context, args []string) (string, error) {
		_, _ = spawner.spawn(ctx, args)
		values := map[string]string{}
		for index := 2; index+1 < len(args); index += 2 {
			values[args[index]] = args[index+1]
		}
		_, err := controller.Run(ctx, lifecycle.Request{ID: args[1], Action: args[0], Generation: values["--generation"], Operation: values["--operation"], Reason: values["--reason"]})
		return "", err
	}
	isMerged := false
	handler.Service.Options.PullRequestState = func(context.Context, string) (PullRequestInfo, error) {
		if isMerged {
			return PullRequestInfo{State: "MERGED"}, nil
		}
		return PullRequestInfo{State: "OPEN"}, nil
	}
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	shown := 0
	for _, task := range snapshot.Tasks {
		if task.Lifecycle != nil && task.Lifecycle.Phase == "paused" {
			shown++
			t.Logf("00:00 board %s: %s", task.ID, task.Reason)
			if task.Reason == "" || task.Lifecycle.Pause == nil {
				t.Fatal("board lost a pause condition")
			}
		}
	}
	if shown != 3 {
		t.Fatalf("board showed %d of three pause reasons", shown)
	}
	for index, moment := range []time.Duration{8 * time.Hour, 8*time.Hour + time.Minute} {
		if err := handler.Service.checkFleet(t.Context(), day.Add(moment)); err != nil {
			t.Fatal(err)
		}
		if index == 0 && len(spawner.recorded()) != 0 {
			t.Fatal("memory resumed before the second reading")
		}
	}
	if calls := awaitDispatch(t, handler.Service, spawner, 1); calls[0][1] != "memory-task" {
		t.Fatalf("memory boundary: %v", calls)
	}
	t.Log("08:01 memory-task resumed in place after the second 8 GB memory/commit reading")
	isMerged = true
	if err := handler.Service.checkFleet(t.Context(), day.Add(14*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if calls := awaitDispatch(t, handler.Service, spawner, 2); calls[1][1] != "dependency-task" {
		t.Fatalf("merge boundary: %v", calls)
	}
	t.Log("14:00 dependency-task resumed in place when its named PR merged")
	watched, err := readFleetWakes(h.State)
	if err != nil {
		t.Fatal(err)
	}
	finished := day.Add(20 * time.Hour)
	if err := reportChecks(h.State, &watched, "ci-task", ghPullRequest{URL: pull, HeadRefOid: head, Checks: []ghCheck{{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: finished.Add(-13 * time.Minute).Format(time.RFC3339), CompletedAt: finished.Format(time.RFC3339)}}}, finished); err != nil {
		t.Fatal(err)
	}
	if err := writeFleetWakes(h.State, watched); err != nil {
		t.Fatal(err)
	}
	if err := handler.Service.checkFleet(t.Context(), finished); err != nil {
		t.Fatal(err)
	}
	if calls := awaitDispatch(t, handler.Service, spawner, 3); calls[2][1] != "ci-task" {
		t.Fatalf("CI boundary: %v", calls)
	}
	t.Log("20:00 ci-task resumed in place on ci_finished for its exact head; CI measured 780 seconds")
	for _, id := range []string{"memory-task", "dependency-task", "ci-task"} {
		meta, err := state.ReadTaskMeta(h.State, id)
		if err != nil || meta.SpawnGen != "generation-"+id {
			t.Fatalf("task identity replaced: %+v %v", meta, err)
		}
		data, err := os.ReadFile(filepath.Join(meta.Worktree, "unfinished.txt"))
		if err != nil || string(data) != "Uncommitted work survives the whole day." {
			t.Fatalf("unlanded work lost: %s %v", data, err)
		}
	}
	t.Log("24:00 all three task ids, worktrees, saved sessions, handoffs and unfinished files retained")
}
