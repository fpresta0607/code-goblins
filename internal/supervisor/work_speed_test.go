package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type progressGit struct {
	head, pushed string
}

func progressWakeCount(t *testing.T, service *Service) int {
	t.Helper()
	count := 0
	for _, record := range fleetWakeRecords(t, service.Store.Home, "check") {
		if record.Key == "slow-task" && strings.HasPrefix(record.Detail, "progress_stalled:") {
			count++
		}
	}
	return count
}

func (git *progressGit) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	if request.Args[0] == "rev-parse" {
		return execx.Result{Stdout: []byte(git.head)}, nil
	}
	return execx.Result{Stdout: []byte("*\trefs/heads/feat/task\t" + git.head + "\n \trefs/remotes/origin/feat/task\t" + git.pushed + "\n")}, nil
}

type stalledGit struct {
	progressGit
	stalledDirectories []string
}

func (git *stalledGit) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if slices.Contains(git.stalledDirectories, request.Dir) {
		<-ctx.Done()
		return execx.Result{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return execx.Result{}, err
	}
	return git.progressGit.Run(ctx, request)
}

func TestProgressWatchMeasuresLaterGoblinsPastAStalledWorktree(t *testing.T) {
	service, h := fleetService(t)
	liveGoblin(t, h, "a-stalled-task", h.Root)
	liveGoblin(t, h, "b-later-task", h.Root)
	head := strings.Repeat("a", 40)
	service.Options.Progress = &stalledGit{progressGit: progressGit{head: head, pushed: head}, stalledDirectories: []string{filepath.Join(h.Root, ".worktrees", "gb-a-stalled-task")}}

	err := service.checkFleet(t.Context(), time.Now().UTC())

	if err == nil || !strings.Contains(err.Error(), "a-stalled-task") || strings.Contains(err.Error(), "b-later-task") {
		t.Fatalf("errors=%v, want only the stalled goblin's", err)
	}
	watched, err := readFleetWakes(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if got := watched.Progress["b-later-task"].Head; got != head {
		t.Fatalf("later goblin head=%q, want it measured past the stalled worktree", got)
	}
}

func TestProgressPassUsesOneDeadlineForAllStalledGoblins(t *testing.T) {
	service, h := fleetService(t)
	var stalledDirectories []string
	for _, id := range []string{"a-stalled", "b-stalled", "c-stalled"} {
		liveGoblin(t, h, id, h.Root)
		stalledDirectories = append(stalledDirectories, filepath.Join(h.Root, ".worktrees", "gb-"+id))
	}
	liveGoblin(t, h, "d-healthy", h.Root)
	head := strings.Repeat("a", 40)
	service.Options.Progress = &stalledGit{progressGit: progressGit{head: head, pushed: head}, stalledDirectories: stalledDirectories}
	started := time.Now()

	err := service.checkFleet(t.Context(), started.UTC())
	elapsed := time.Since(started)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("progress errors=%v, want the stalled probes' deadline", err)
	}
	if elapsed > PROGRESS_PASS_TIMEOUT+5*time.Second {
		t.Fatalf("progress pass took %s for three stalled goblins, exceeding its single %s budget", elapsed, PROGRESS_PASS_TIMEOUT)
	}
	watched, err := readFleetWakes(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if got := watched.Progress["d-healthy"].Head; got != head {
		t.Fatalf("healthy goblin head=%q, want progress recorded within the same pass", got)
	}
}

func TestProgressWatchReportsOnceAndResetsOnRealProgress(t *testing.T) {
	for _, source := range []string{"commit", "push", "gate", "report"} {
		t.Run(source, func(t *testing.T) {
			service, h := fleetService(t)
			liveGoblin(t, h, "slow-task", h.Root)
			git := &progressGit{head: strings.Repeat("a", 40), pushed: strings.Repeat("a", 40)}
			service.Options.Progress = git
			now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			writeFile(t, filepath.Join(h.State, "slow-task.status"), now.Format(time.RFC3339)+" working: Implementing recovery\n")
			for _, elapsed := range []time.Duration{0, 19 * time.Minute, 20 * time.Minute, 21 * time.Minute} {
				if err := service.checkFleet(t.Context(), now.Add(elapsed)); err != nil {
					t.Fatal(err)
				}
				wakes := progressWakeCount(t, service)
				want := 0
				if elapsed >= 20*time.Minute {
					want = 1
				}
				if wakes != want {
					t.Fatalf("at %s progress wakes=%d, want %d", elapsed, wakes, want)
				}
			}
			switch source {
			case "commit":
				git.head = strings.Repeat("b", 40)
			case "push":
				git.pushed = strings.Repeat("b", 40)
			case "gate":
				service.Store.db.Tasks["slow-task"] = Evaluation{Generation: "s1", GateStep: "test", At: now.Add(22 * time.Minute)}
				if err := service.Store.save(); err != nil {
					t.Fatal(err)
				}
			case "report":
				writeFile(t, filepath.Join(h.State, "slow-task.status"), now.Add(22*time.Minute).Format(time.RFC3339)+" working: Recovery is tested\n")
			}
			if err := service.checkFleet(t.Context(), now.Add(22*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := service.checkFleet(t.Context(), now.Add(41*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if got := progressWakeCount(t, service); got != 1 {
				t.Fatalf("timer was not reset by %s: wakes=%d", source, got)
			}
			if err := service.checkFleet(t.Context(), now.Add(42*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if got := progressWakeCount(t, service); got != 2 {
				t.Fatalf("new stall was not reported: wakes=%d", got)
			}
		})
	}
}

func TestProgressWatchDoesNotTreatRepeatedReportsOrPausedWaitsAsWork(t *testing.T) {
	service, h := fleetService(t)
	liveGoblin(t, h, "slow-task", h.Root)
	service.Options.Progress = &progressGit{head: strings.Repeat("a", 40)}
	now := time.Now().UTC().Truncate(time.Second)
	writeFile(t, filepath.Join(h.State, "slow-task.status"), now.Format(time.RFC3339)+" working: Testing\n")
	if err := service.checkFleet(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "slow-task.status"), now.Add(19*time.Minute).Format(time.RFC3339)+" working: Testing\n")
	if err := service.checkFleet(t.Context(), now.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if progressWakeCount(t, service) != 1 {
		t.Fatal("repeated report hid a stall")
	}
	pausedGoblin(t, h, "slow-task", "overlord", "", now.Add(21*time.Minute))
	if err := service.checkFleet(t.Context(), now.Add(45*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if progressWakeCount(t, service) != 1 {
		t.Fatal("intentional pause reported as a stall")
	}
}

func TestSnapshotDoesNotReadProgressOrDurationsBeforeTheCycle(t *testing.T) {
	service, h := fleetService(t)
	liveGoblin(t, h, "progress-task", h.Root)
	service.cycle(t.Context(), false)
	if err := writeFleetWakes(h.State, fleetWakes{
		Progress:  map[string]WorkProgress{"progress-task": {Generation: "s1", At: time.Now().UTC(), Head: strings.Repeat("a", 40)}},
		Durations: []CIDuration{{Repository: "owner/repo", Kind: "ci", Seconds: 780}},
	}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := service.Snapshot()

	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.CIDurations) != 0 {
		t.Fatal("snapshot read durations before the cycle refreshed them")
	}
	for _, task := range snapshot.Tasks {
		if task.Progress != nil {
			t.Fatal("snapshot read progress before the cycle refreshed it")
		}
	}
}

func TestSnapshotRefreshesProgressAndDurationsWhenTheCycleReadsTheirRecord(t *testing.T) {
	service, h := fleetService(t)
	liveGoblin(t, h, "progress-task", h.Root)
	if _, err := service.Snapshot(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		head    string
		seconds int64
	}{
		{strings.Repeat("a", 40), 780},
		{strings.Repeat("b", 40), 630},
	} {
		now := time.Now().UTC()
		watched := fleetWakes{
			Progress:  map[string]WorkProgress{"progress-task": {Generation: "s1", At: now, Head: test.head}},
			Durations: []CIDuration{{Repository: "owner/repo", Kind: "ci", Seconds: test.seconds}},
		}
		if err := writeFleetWakes(h.State, watched); err != nil {
			t.Fatal(err)
		}
		service.cycle(t.Context(), false)

		snapshot, err := service.Snapshot()

		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.CIDurations) != 1 || snapshot.CIDurations[0].Seconds != test.seconds {
			t.Fatalf("durations=%+v, want the changed record's %d seconds", snapshot.CIDurations, test.seconds)
		}
		index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == "progress-task" })
		if index < 0 {
			t.Fatal("snapshot lost the progress task")
		}
		task := snapshot.Tasks[index]
		if task.Progress == nil || task.Progress.Head != test.head {
			t.Fatalf("task=%+v, want the changed progress head %s", task, test.head)
		}
	}
}
