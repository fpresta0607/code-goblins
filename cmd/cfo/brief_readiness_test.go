package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func TestBriefDraftWaitsForFinalLaunchChoicesThroughTheSupervisor(t *testing.T) {
	// Arrange: use the CLI dispatcher, the real supervisor and a fake launcher.
	h := testHome(t)
	t.Setenv("CFO_HOME", h.Root)
	t.Setenv("CFO_STATE_OVERRIDE", h.State)
	project := t.TempDir()
	runtime := defaultCommandRuntime()
	runtime.quota, runtime.repoActivity, runtime.speedHint = nil, nil, nil
	var mutex sync.Mutex
	var requests []spawn.Request
	var needs []auth.Need
	runtime.spawn = func(_ context.Context, h home.Home, request spawn.Request) (spawn.Result, error) {
		content, err := fsx.ReadFile(request.BriefPath)
		if err != nil {
			return spawn.Result{}, err
		}
		need, err := auth.NeedFromBrief(string(content))
		if err != nil {
			return spawn.Result{}, err
		}
		mutex.Lock()
		requests = append(requests, request)
		needs = append(needs, need)
		mutex.Unlock()
		meta := state.TaskMeta{ID: request.ID, Project: request.Project, Mode: request.Mode, Harness: string(request.Harness), SpawnGen: "fixture-start"}
		return spawn.Result{Meta: meta}, state.WriteTaskMeta(h.State, meta)
	}
	var output, failure bytes.Buffer
	if code := runWithRuntime([]string{"brief", "draft-task", "--project", project}, &output, &failure, runtime); code != 0 {
		t.Fatalf("brief: %d %s", code, &failure)
	}
	path := strings.TrimSpace(output.String())
	content, err := fsx.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "routing.json"), []byte(shippedRoutingJSON(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	count := func() int { mutex.Lock(); defer mutex.Unlock(); return len(requests) }
	scan := func() {
		t.Helper()
		service, err := supervisor.Start(t.Context(), h, supervisor.Options{
			Example:   true,
			Reconcile: func(context.Context) error { return nil },
			Dispatch: &supervisor.Dispatch{
				Memory: func() (supervisor.Memory, error) {
					return supervisor.Memory{Available: 16 << 30, CommitAvailable: 32 << 30}, nil
				},
				Disk: func() (supervisor.Disk, error) {
					return supervisor.Disk{Reading: disk.Reading{Free: 200 << 30}, Floor: 15 << 30}, nil
				},
				Spawn: func(_ context.Context, args []string) (string, error) {
					var output, failure bytes.Buffer
					if code := runWithRuntime(args, &output, &failure, runtime); code != 0 {
						return failure.String(), fmt.Errorf("spawn exit %d", code)
					}
					return output.String(), nil
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		deadline := time.Now().Add(time.Minute)
		for time.Now().Before(deadline) {
			view, err := service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			_, startLockErr := os.Stat(filepath.Join(h.State, ".queued-draft-task.lock"))
			if view.Scheduling != nil && os.IsNotExist(startLockErr) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("supervisor did not finish its initial scheduling scan")
	}

	// Act and assert: author requirements first, launch settings last.
	for _, placeholder := range []string{"", "{TASK - what to build or learn, in one clear paragraph}", "{ACCEPTANCE - concrete, verifiable outcomes}", "{CONSTRAINTS - things not to touch, boundaries, non-goals}"} {
		if placeholder != "" {
			content = bytes.ReplaceAll(content, []byte(placeholder), []byte("Authored requirement."))
			if err := fsx.AtomicWriteFile(path, content); err != nil {
				t.Fatal(err)
			}
		}
		scan()
		if count() != 0 {
			t.Fatalf("draft started before final choices, after replacing %q", placeholder)
		}
		if _, err := os.Stat(filepath.Join(h.State, "tasktmp", "draft-task", "pipeline.json")); !os.IsNotExist(err) {
			t.Fatalf("draft froze policy: %v", err)
		}
	}
	content = bytes.ReplaceAll(content, []byte("credentials: none"), []byte("credentials: github"))
	content = bytes.ReplaceAll(content, []byte("mode: no-mistakes"), []byte("mode: local-only\nharness: codex\nmodel: gpt-6.1-sol\neffort: high"))
	content = bytes.ReplaceAll(content, []byte("ready: false"), []byte("ready: true"))
	if err := fsx.AtomicWriteFile(path, content); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		scan()
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(requests) != 1 || requests[0].Mode != "local-only" || requests[0].Model != "gpt-6.1-sol" || requests[0].Effort != "high" || len(needs[0].Services) != 1 || needs[0].Services[0] != "github" {
		t.Fatalf("launches = %+v, needs = %+v; want one launch with final authored choices", requests, needs)
	}
}

func TestExplicitSpawnRejectsAConcurrentBriefEdit(t *testing.T) {
	// Arrange
	h := testHome(t)
	project := t.TempDir()
	path := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(path, []byte(completedCommandBrief), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := testCommandRuntimeForHome(h)
	runtime.spawn = func(ctx context.Context, h home.Home, request spawn.Request) (spawn.Result, error) {
		if err := fsx.AtomicWriteFile(path, []byte(strings.ReplaceAll(completedCommandBrief, "mode: no-mistakes", "mode: local-only"))); err != nil {
			return spawn.Result{}, err
		}
		return (spawn.Service{StateDir: h.State, Project: project}).Spawn(ctx, request)
	}
	var output, failure bytes.Buffer

	// Act
	code := runWithRuntime([]string{"spawn", "race-task", "--project", project, "--brief", path, "--harness", "codex"}, &output, &failure, runtime)

	// Assert
	if code == 0 || !strings.Contains(failure.String(), "brief changed after routing") {
		t.Fatalf("exit=%d stderr=%s", code, &failure)
	}
	if _, err := os.Stat(filepath.Join(h.State, "race-task.meta")); !os.IsNotExist(err) {
		t.Fatalf("concurrent edit froze task choices: %v", err)
	}
}

func TestExplicitSpawnRejectsPartialBriefWrites(t *testing.T) {
	for _, end := range []int{0, 8, strings.Index(completedCommandBrief, "## Acceptance"), strings.Index(completedCommandBrief, "## Constraints"), strings.Index(completedCommandBrief, "## Authentication"), strings.Index(completedCommandBrief, "## Delivery"), len(completedCommandBrief) - 3} {
		t.Run(fmt.Sprint(end), func(t *testing.T) {
			// Arrange
			runtime := testCommandRuntime(t)
			calls := 0
			runtime.spawn = func(context.Context, home.Home, spawn.Request) (spawn.Result, error) {
				calls++
				return spawn.Result{}, nil
			}
			path := filepath.Join(t.TempDir(), "brief.md")
			if err := os.WriteFile(path, []byte(completedCommandBrief[:end]), 0o600); err != nil {
				t.Fatal(err)
			}
			var output, failure bytes.Buffer

			// Act
			code := runWithRuntime([]string{"spawn", "partial-task", "--project", t.TempDir(), "--brief", path, "--harness", "codex"}, &output, &failure, runtime)

			// Assert
			if code == 0 || calls != 0 || !strings.Contains(failure.String(), "brief is not ready") {
				t.Fatalf("exit=%d launches=%d stderr=%s", code, calls, &failure)
			}
		})
	}
}

func TestBriefCreationSharesTheQueuedAdmissionLock(t *testing.T) {
	// Arrange
	h := testHome(t)
	t.Setenv("CFO_HOME", h.Root)
	t.Setenv("CFO_STATE_OVERRIDE", h.State)
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, ".queued-draft-task.lock"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.ReleaseExclusiveNamed(h.State, ".queued-draft-task.lock"); err != nil {
			t.Error(err)
		}
	})
	var output, failure bytes.Buffer

	// Act
	code := runWithRuntime([]string{"brief", "draft-task", "--project", t.TempDir()}, &output, &failure, defaultCommandRuntime())

	// Assert
	if code == 0 {
		t.Fatal("scaffold creation bypassed the queued start lock")
	}
	if _, err := os.Stat(filepath.Join(h.Data, "draft-task", "brief.md")); !os.IsNotExist(err) {
		t.Fatalf("contended creation published a brief: %v", err)
	}
}

func TestConcurrentBriefCreationPublishesOneDraft(t *testing.T) {
	// Arrange
	h := testHome(t)
	t.Setenv("CFO_HOME", h.Root)
	t.Setenv("CFO_STATE_OVERRIDE", h.State)
	project := t.TempDir()
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, mode := range []string{"local-only", "direct-PR"} {
		go func() {
			<-start
			var output, failure bytes.Buffer
			results <- runWithRuntime([]string{"brief", "draft-task", "--project", project, "--mode", mode}, &output, &failure, defaultCommandRuntime())
		}()
	}

	// Act
	close(start)
	successes := 0
	for range 2 {
		select {
		case code := <-results:
			if code == 0 {
				successes++
			}
		case <-time.After(time.Minute):
			t.Fatal("concurrent brief creation did not finish")
		}
	}

	// Assert
	content, err := fsx.ReadFile(filepath.Join(h.Data, "draft-task", "brief.md"))
	if err != nil || successes != 1 || !bytes.Contains(content, []byte("ready: false")) {
		t.Fatalf("successes=%d brief=%s error=%v", successes, content, err)
	}
}

func TestExplicitSpawnRefusesUnfinishedBriefBeforeRouting(t *testing.T) {
	for _, content := range []string{"", "{TASK - describe work}", "## Task\nWork.\n{ACCEPTANCE - concrete outcomes}", "## Task\nWork.\n{CONSTRAINTS - boundaries}", "## Task\nWork.\n## Delivery\nready: false\n", "## Task\nWork.\n## Delivery\nready: perhaps\n"} {
		t.Run(content, func(t *testing.T) {
			// Arrange
			runtime := testCommandRuntime(t)
			calls := 0
			runtime.spawn = func(context.Context, home.Home, spawn.Request) (spawn.Result, error) {
				calls++
				return spawn.Result{}, nil
			}
			path := filepath.Join(t.TempDir(), "brief.md")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			var output, failure bytes.Buffer

			// Act
			code := runWithRuntime([]string{"spawn", "draft-task", "--project", t.TempDir(), "--brief", path, "--harness", "codex"}, &output, &failure, runtime)

			// Assert
			if code == 0 || calls != 0 || !strings.Contains(failure.String(), "brief") {
				t.Fatalf("exit=%d launches=%d stderr=%s", code, calls, &failure)
			}
		})
	}
}
