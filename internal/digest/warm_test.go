package digest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

type startupTestEndpoint struct {
	reads   int
	isIdle  bool
	started chan struct{}
	release chan struct{}
}

func (endpoint *startupTestEndpoint) Read(_ context.Context, _ state.TaskMeta) (bool, herdr.BusyState, error) {
	endpoint.reads++
	if endpoint.started != nil {
		select {
		case endpoint.started <- struct{}{}:
		default:
		}
		<-endpoint.release
	}
	if endpoint.isIdle {
		return true, herdr.BusyIdle, nil
	}
	return true, herdr.BusyWorking, nil
}

func (endpoint *startupTestEndpoint) Validate(_ context.Context, meta state.TaskMeta) (bool, error) {
	return meta.Backend == "native", nil
}

func TestStartupSamplePreservesVerifiedIdleStatusFallback(t *testing.T) {
	h := newDigestHome(t)
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "task", Worktree: h.Root, Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, "task", "waiting: the declared dependency"); err != nil {
		t.Fatal(err)
	}
	endpoint := &startupTestEndpoint{isIdle: true}
	var output bytes.Buffer
	if err := composeBriefWithEndpoint(context.Background(), h, os.Getpid(), "idle", false, false, endpoint, &output); err != nil {
		t.Fatal(err)
	}
	long, err := os.ReadFile(filepath.Join(h.State, FullDigestFile))
	if err != nil || endpoint.reads != 1 || !strings.Contains(output.String(), "current=waiting (status)") || !bytes.Contains(long, []byte("current=waiting (status)")) {
		t.Fatalf("bounded sample lost verified idle fallback: reads=%d, error=%v", endpoint.reads, err)
	}
}

func TestStartupSamplesCurrentStateOnceAcrossAllRenderings(t *testing.T) {
	for _, hasCatalog := range []bool{false, true} {
		t.Run(fmt.Sprint(hasCatalog), func(t *testing.T) {
			h := newDigestHome(t)
			if hasCatalog {
				if err := os.MkdirAll(filepath.Join(h.Data, "memory"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), []byte(`{"version":1,"sources":[]}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "task", Worktree: h.Root, Backend: "native"}); err != nil {
				t.Fatal(err)
			}
			endpoint := &startupTestEndpoint{}
			var output bytes.Buffer

			if err := composeBriefWithEndpoint(context.Background(), h, os.Getpid(), "sample", false, false, endpoint, &output); err != nil {
				t.Fatal(err)
			}

			long, err := os.ReadFile(filepath.Join(h.State, FullDigestFile))
			if err != nil || endpoint.reads != 1 || !strings.Contains(output.String(), "current=working") || !bytes.Contains(long, []byte("current=working")) {
				t.Fatalf("startup did not reuse one sample: reads=%d, error=%v", endpoint.reads, err)
			}
		})
	}
}

func TestStartupQueueUsesCanonicalRowsAcrossCompositionEntryPoints(t *testing.T) {
	for _, hasCatalog := range []bool{false, true} {
		for _, entryPoint := range []string{"manual", "saved brief", "read-only"} {
			for _, queueSize := range []int{1, 6} {
				t.Run(fmt.Sprintf("catalog=%t/%s/%d", hasCatalog, entryPoint, queueSize), func(t *testing.T) {
					h := newDigestHome(t)
					if hasCatalog {
						if err := os.MkdirAll(filepath.Join(h.Data, "memory"), 0o700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), []byte(`{"version":1,"sources":[]}`), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					meta := state.TaskMeta{ID: "paused-task", SpawnGen: "current"}
					if err := state.WriteTaskMeta(h.State, meta); err != nil {
						t.Fatal(err)
					}
					if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause", Action: "pause", Phase: "paused"}); err != nil {
						t.Fatal(err)
					}
					for _, outcome := range []state.Outcome{
						{ID: "delivered", Phase: "done", Evidence: "verified delivery"},
						{ID: "next-1", Phase: "stopped", Reason: "queued for another attempt"},
					} {
						if err := state.WriteOutcome(h.State, outcome); err != nil {
							t.Fatal(err)
						}
					}
					backlog := "## Queued\n- **paused-task** - Already dispatched\n- [ ] delivered - Already delivered\n"
					for index := 1; index <= queueSize; index++ {
						backlog += fmt.Sprintf("- **next-%d** - Next task %d\n  Detail line\n", index, index)
					}
					backlog += "## Parked\n- **parked** - Parked task\n  Held detail\n"
					if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(backlog), 0o600); err != nil {
						t.Fatal(err)
					}
					canonical, err := fleet.BuildSnapshot(t.Context(), h, &startupTestEndpoint{})
					if err != nil || len(canonical.Backlog.Queued) != 2*queueSize {
						t.Fatalf("canonical queue should retain task and detail rows: %+v, %v", canonical.Backlog, err)
					}
					var output bytes.Buffer
					switch entryPoint {
					case "manual":
						err = Compose(h, os.Getpid(), "canonical", &output)
					case "saved brief":
						err = ComposeBrief(h, os.Getpid(), "canonical", false, false, &output)
						if err == nil {
							var saved []byte
							saved, err = os.ReadFile(filepath.Join(h.State, FullDigestFile))
							output.Reset()
							output.Write(saved)
						}
					case "read-only":
						err = ComposeReadOnly(h, "inspection", &output)
					}
					if err != nil {
						t.Fatal(err)
					}
					_, queue, hasQueue := strings.Cut(output.String(), "Queued:")
					queue, _, hasEnd := strings.Cut(queue, "Canonical current state:")
					expected := " 1; parked: 1. First queued tasks (display only):\n- **next-1** - Next task 1\n"
					if queueSize == 6 {
						expected = " 6; parked: 1. First queued tasks (display only):\n- **next-1** - Next task 1\n- **next-2** - Next task 2\n- **next-3** - Next task 3\n- **next-4** - Next task 4\n- **next-5** - Next task 5\n(+1 more queued)\n"
					}
					if !hasQueue || !hasEnd || queue != expected {
						t.Fatalf("startup queue diverged from canonical task set: %q", queue)
					}
				})
			}
		}
	}
}

func TestStartupBudgetUsesConfiguredAllowanceForEquivalentDirectories(t *testing.T) {
	h := newDigestHome(t)
	words := []byte("Preserve the exact ruling.\n")
	if err := os.MkdirAll(filepath.Join(h.Data, "memory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.Root, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "overlord.md"), words, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(words))
	catalog := fmt.Sprintf(`{"version":1,"sources":[{"path":"overlord.md","sha256":%q,"entries":[{"id":"rule","start":0,"end":%d,"sha256":%q,"authority":"ruling","location":"warm"}]}]}`, hash, len(words), hash)
	if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Root, "config", "startup-memory-budget"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(h.Data, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ workingDir, dataDir string }{
		{h.Root, h.Data}, {h.Root, h.Data + "/"}, {h.Root, h.Data + string(filepath.Separator)},
		{h.Root, "data"}, {h.Root, "data/"}, {h.Root, "data" + string(filepath.Separator)},
		{h.Data, "."}, {h.Data, "./"}, {h.Data, "." + string(filepath.Separator)}, {h.Data, "../data"},
		{child, ".."}, {child, "../"}, {child, ".." + string(filepath.Separator)}, {child, "../../data/"},
	} {
		t.Run(test.workingDir+"/"+test.dataDir, func(t *testing.T) {
			t.Chdir(test.workingDir)
			directoryHome := h
			directoryHome.Data = test.dataDir
			var output bytes.Buffer

			if err := ComposeReadOnly(directoryHome, "allowance", &output); err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(output.String(), "budget 1 estimated tokens") || !strings.Contains(output.String(), "MEMORY OVER BUDGET") || !bytes.Contains(output.Bytes(), words) {
				t.Fatalf("equivalent directory bypassed the startup allowance or dropped authority: %s", output.String())
			}
		})
	}
}

func TestStartupDeadlineDoesNotWaitForAnUnresponsiveTerminal(t *testing.T) {
	h := newDigestHome(t)
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "task", Worktree: h.Root, Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	endpoint := &startupTestEndpoint{started: make(chan struct{}, 1), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	finished := make(chan error, 1)
	go func() {
		finished <- composeBriefWithEndpoint(ctx, h, os.Getpid(), "deadline", false, false, endpoint, &output)
	}()
	select {
	case <-endpoint.started:
	case <-time.After(2 * time.Second):
		close(endpoint.release)
		t.Fatal("startup never reached the terminal boundary")
	}
	cancel()
	isReturned := false
	select {
	case err := <-finished:
		isReturned = true
		if err != nil || !strings.Contains(output.String(), "current=unknown") {
			t.Errorf("timed-out state was not explicit: %s, %v", output.String(), err)
		}
	case <-time.After(time.Second):
		t.Error("startup waited for the terminal after its shared deadline")
	}
	close(endpoint.release)
	if !isReturned {
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Fatal("startup did not stop after releasing the terminal fixture")
		}
	}
}

func TestLegacyStartupBoundsStateWhileRetainingEveryAuthorityByte(t *testing.T) {
	h := bigHome(t, 200, 200)
	var authority [][]byte
	authorityBytes := 0
	for _, name := range []string{"projects.md", "overlord.md", "memory/MEMORY.md"} {
		content, err := os.ReadFile(filepath.Join(h.Data, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		authority = append(authority, content)
		authorityBytes += len(content)
	}
	meta := state.TaskMeta{ID: "cg-goblin-01", SpawnGen: "current", Worktree: h.Root, Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.AcquireNamedOwner(h.State, state.LifecycleLockName(meta.ID), os.Getpid(), "controller"); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume", Action: "resume", Phase: "resuming"}); err != nil {
		t.Fatal(err)
	}
	backlog := "## Queued\n- **queued-1** - First task\n  Detail one\n  Detail two\n- **queued-2** - Second task\n## Parked\n- **parked-1** - Held task\n  Held detail\n"
	if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(backlog), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reap.WriteRecord(h.State, reap.Record{Time: time.Now().UTC(), Findings: []reap.Finding{{Class: reap.OrphanProcess, PID: 4242, Detail: strings.Repeat("Synthetic orphan detail. ", 4000)}}}); err != nil {
		t.Fatal(err)
	}
	var manual, readOnly bytes.Buffer
	if err := Compose(h, os.Getpid(), "legacy", &manual); err != nil {
		t.Fatal(err)
	}
	if err := ComposeReadOnly(h, "inspection", &readOnly); err != nil {
		t.Fatal(err)
	}
	composeBrief(t, h)
	long, err := os.ReadFile(filepath.Join(h.State, FullDigestFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range [][]byte{manual.Bytes(), readOnly.Bytes(), long} {
		t.Logf("legacy startup: authority=%d state_and_bookkeeping=%d total=%d bytes", authorityBytes, len(output)-authorityBytes, len(output))
		if len(output)-authorityBytes > 18000 || !bytes.Contains(output, []byte("current=resuming (metadata)")) || !bytes.Contains(output, []byte("Queued: 2; parked: 1.")) || bytes.Contains(output, []byte("Detail one")) {
			t.Errorf("legacy state projection: overhead=%d, current=%t, counts=%t", len(output)-authorityBytes, bytes.Contains(output, []byte("current=resuming (metadata)")), bytes.Contains(output, []byte("Queued: 2; parked: 1.")))
		}
		for _, content := range authority {
			if !bytes.Contains(output, content) {
				t.Error("bounded state omitted legacy authority bytes")
			}
		}
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 200 {
		t.Fatalf("legacy projection acknowledged omitted wakes: %d, %v", len(pending), err)
	}
}

func TestComposeUsesVerifiedWarmMemoryInsteadOfHistoricalRulings(t *testing.T) {
	// Arrange: the approved cold range stays byte-identical in its original file.
	h := newDigestHome(t)
	active := "Never merge without an explicit instruction.\n"
	history := strings.Repeat("A completed incident with no current action.\n", 500)
	content := active + history
	if err := os.MkdirAll(filepath.Join(h.Data, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "overlord.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := map[string]any{"version": 1, "sources": []any{map[string]any{
		"path": "overlord.md", "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(content))),
		"entries": []any{
			map[string]any{"id": "merge-authority", "start": 0, "end": len(active), "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(active))), "authority": "ruling", "location": "warm"},
			map[string]any{"id": "completed-incident", "start": len(active), "end": len(content), "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(history))), "authority": "history", "location": "cold", "trigger": "incident investigation", "topics": []string{"incident"}, "approval": map[string]string{"by": "Overlord", "quote": "Move completed-incident to cold retrieval.", "date": "2026-10-30", "destination": "overlord.md#completed-incident"}},
		},
	}}}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	var output bytes.Buffer
	if err := Compose(h, os.Getpid(), "warm", &output); err != nil {
		t.Fatal(err)
	}

	// Assert: this is the emitted startup interface, not a source-text assertion.
	if !strings.Contains(output.String(), active) || strings.Contains(output.String(), history) {
		t.Fatalf("startup did not select current authority independently of cold history: %s", output.String())
	}
	if !strings.Contains(output.String(), "cfo memory retrieve") || !strings.Contains(output.String(), "completed-incident") {
		t.Fatalf("startup omitted the cold retrieval route: %s", output.String())
	}
	if output.Len() > 18000 {
		t.Fatalf("startup unexpectedly grew with cold history: %d bytes", output.Len())
	}
	preserved, err := os.ReadFile(filepath.Join(h.Data, "overlord.md"))
	if err != nil || string(preserved) != content {
		t.Fatal("composition changed the original ruling bytes")
	}
}

func TestResumedLegacyStartupRequiresChangedAuthorityToBeRead(t *testing.T) {
	for _, name := range []string{"overlord.md", "projects.md", "memory/MEMORY.md"} {
		t.Run(name, func(t *testing.T) {
			h := newDigestHome(t)
			path := filepath.Join(h.Data, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("Original authority.\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			composeBrief(t, h)
			if err := os.WriteFile(path, []byte("Corrected authority.\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			var output bytes.Buffer
			if err := ComposeBrief(h, os.Getpid(), "s1", false, true, &output); err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(output.String(), "Read all of it") || strings.Contains(output.String(), "do not read it again") {
				t.Fatalf("changed legacy authority was assumed read: %s", output.String())
			}
			long, err := os.ReadFile(filepath.Join(h.State, FullDigestFile))
			if err != nil || !bytes.Contains(long, []byte("Corrected authority.")) {
				t.Fatalf("current authority not composed: %s, %v", long, err)
			}
		})
	}
}

func TestWarmStartupUsesCurrentLifecycleAndCountsTaskRecords(t *testing.T) {
	for _, test := range []struct{ phase, generation, current, source string }{
		{"pausing", "current", "pausing", "metadata"},
		{"paused", "current", "paused", "metadata"},
		{"resuming", "current", "resuming", "metadata"},
		{"stopping", "current", "stopping", "metadata"},
		{"stopped", "current", "stopped", "metadata"},
		{"running", "current", "unknown", "none"},
		{"failed", "current", "unknown", "none"},
		{"paused", "previous", "unknown", "none"},
	} {
		t.Run(test.phase+"/"+test.generation, func(t *testing.T) {
			h := newDigestHome(t)
			if err := os.MkdirAll(filepath.Join(h.Data, "memory"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), []byte(`{"version":1,"sources":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			meta := state.TaskMeta{ID: "task", SpawnGen: "current", Worktree: h.Root, Backend: "native"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if _, err := lock.AcquireNamedOwner(h.State, state.LifecycleLockName(meta.ID), os.Getpid(), "controller"); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: test.generation, Operation: "operation", Action: "resume", Phase: test.phase}); err != nil {
				t.Fatal(err)
			}
			if err := state.AppendStatus(h.State, meta.ID, "working: old event"); err != nil {
				t.Fatal(err)
			}
			backlog := "## Queued\n- **queued-1** - First task\n  Detail one\n  Detail two\n  Detail three\n  Detail four\n- **queued-2** - Second task\n## Parked\n- **parked-1** - Held task\n  Held detail one\n  Held detail two\n"
			if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(backlog), 0o600); err != nil {
				t.Fatal(err)
			}

			var output bytes.Buffer
			if err := Compose(h, os.Getpid(), "current", &output); err != nil {
				t.Fatal(err)
			}
			brief := composeBrief(t, h)

			for _, text := range []string{output.String(), brief} {
				if !strings.Contains(text, "current="+test.current+" ("+test.source+")") || !strings.Contains(text, "event:") {
					t.Fatalf("lifecycle was hidden behind an event: %s", text)
				}
			}
			if !strings.Contains(output.String(), "Queued: 2; parked: 1.") || !strings.Contains(output.String(), "Second task") || strings.Contains(output.String(), "Detail one") {
				t.Fatalf("backlog detail lines consumed task count or preview: %s", output.String())
			}
		})
	}
}

func TestWarmStartupBoundsCanonicalStateWithoutAcknowledgingOmittedWakes(t *testing.T) {
	// Arrange: cold history and current state are separate bounded inputs.
	h := bigHome(t, 200, 200)
	for _, name := range []string{"projects.md", "overlord.md", filepath.Join("memory", "MEMORY.md")} {
		if err := os.Remove(filepath.Join(h.Data, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), []byte(`{"version":1,"sources":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	var output bytes.Buffer
	if err := Compose(h, os.Getpid(), "bounded", &output); err != nil {
		t.Fatal(err)
	}
	remaining, err := wake.Pending(h.State)

	// Assert: the remainder is retrievable and is never marked handled.
	if err != nil || len(remaining) != 200 || output.Len() > 18000 {
		t.Fatalf("state output=%d bytes, pending=%d, error=%v", output.Len(), len(remaining), err)
	}
	if !strings.Contains(output.String(), "cfo fleet-view") || !strings.Contains(output.String(), "cfo drain") {
		t.Fatal("bounded state omitted its canonical retrieval commands")
	}
}

func TestResumedWarmStartupRequiresChangedAuthorityToBeRead(t *testing.T) {
	h := newDigestHome(t)
	if err := os.MkdirAll(filepath.Join(h.Data, "memory"), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(words string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(h.Data, "overlord.md"), []byte(words), 0o600); err != nil {
			t.Fatal(err)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(words)))
		catalog := fmt.Sprintf(`{"version":1,"sources":[{"path":"overlord.md","sha256":%q,"entries":[{"id":"rule","start":0,"end":%d,"sha256":%q,"authority":"ruling","location":"warm"}]}]}`, hash, len(words), hash)
		if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), []byte(catalog), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("The first ruling.\n")
	composeBrief(t, h)
	write("The corrected ruling.\n")
	var output bytes.Buffer
	if err := ComposeBrief(h, os.Getpid(), "s1", false, true, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "do not read it again") || !strings.Contains(output.String(), "Read all of it") {
		t.Fatal("resume claimed this conversation already holds changed authority")
	}
}

func TestWarmStartupKeepsBudgetAndSourceFailuresExplicit(t *testing.T) {
	for _, scenario := range []string{"over budget", "corrupt source", "missing source", "absent sources"} {
		t.Run(scenario, func(t *testing.T) {
			h := newDigestHome(t)
			if err := os.MkdirAll(filepath.Join(h.Data, "memory"), 0o700); err != nil {
				t.Fatal(err)
			}
			words := "Preserve this exact pinned authority even when the allowance is small.\n"
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(words)))
			catalog := fmt.Sprintf(`{"version":1,"sources":[{"path":"overlord.md","sha256":%q,"entries":[{"id":"rule","start":0,"end":%d,"sha256":%q,"authority":"ruling","location":"warm"}]}]}`, hash, len(words), hash)
			if err := os.WriteFile(filepath.Join(h.Data, "overlord.md"), []byte(words), 0o600); err != nil {
				t.Fatal(err)
			}
			if scenario == "corrupt source" {
				if err := os.WriteFile(filepath.Join(h.Data, "overlord.md"), []byte("changed authority"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing source" || scenario == "absent sources" {
				if err := os.Remove(filepath.Join(h.Data, "overlord.md")); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "absent sources" {
				catalog = `{"version":1,"sources":[]}`
			}
			if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), []byte(catalog), 0o600); err != nil {
				t.Fatal(err)
			}
			if scenario == "over budget" {
				if err := os.MkdirAll(filepath.Join(h.Root, "config"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(h.Root, "config", "startup-memory-budget"), []byte("1"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			if err := Compose(h, os.Getpid(), scenario, &output); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "over budget":
				if !strings.Contains(output.String(), words) || !strings.Contains(output.String(), "MEMORY OVER BUDGET") {
					t.Fatal("a small budget silently dropped pinned authority")
				}
			case "absent sources":
				if !strings.Contains(output.String(), "MEMORY SOURCES ABSENT") {
					t.Fatal("an empty catalog concealed absent authority")
				}
			default:
				if !strings.Contains(output.String(), "MEMORY UNREADABLE") || strings.Contains(output.String(), words) {
					t.Fatal("invalid authority selected a silent legacy fallback")
				}
			}
		})
	}
}

func TestReadOnlyWarmPreviewUsesSelectiveRetrieval(t *testing.T) {
	h := newDigestHome(t)
	if err := os.MkdirAll(filepath.Join(h.Data, "memory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "memory", "startup.json"), []byte(`{"version":1,"sources":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := ComposeBrief(h, 0, "read-only", false, false, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "cfo memory retrieve") || strings.Contains(output.String(), "read "+filepath.Join(h.Data, "overlord.md")) {
		t.Fatal("read-only startup selected the full history instead of selective retrieval")
	}
	if _, err := os.Stat(filepath.Join(h.State, FullDigestFile)); !os.IsNotExist(err) {
		t.Fatal("read-only startup wrote a shared digest")
	}
}
