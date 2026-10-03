package supervisor

import (
	"context"
	"path/filepath"
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
