package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The Overlord, 2026-10-21, of three goblin cards in In progress: "why do
// thee says blocked and failed yet all run". Two read Blocked over what their
// goblins reported since. One worked in a worktree whose branch last ran its
// gate the night before, a run cancelled at its review; the other waited on
// another task's gate run after its own had failed. Each card was held by the
// failed step of a run that had ended, read as a decision the gate waited on.
// A run that ended holds no step and asks for no decision.
func TestAnEndedGateRunHoldsNoGoblinsCard(t *testing.T) {
	const awaited = "01JZTESTRUNOFANOTHERTASK0"
	ended := []pipeline.ProgressStep{{Name: "intent", Status: "completed"}, {Name: "rebase", Status: "completed"}, {Name: "review", Status: "failed"}, {Name: "test", Status: "pending"}, {Name: "ci", Status: "pending"}}
	for _, test := range []struct {
		name        string
		status      string
		reports     []string
		after       []string
		health      monitor.Health
		wantPhase   string
		wantWaiting string
	}{
		{"at work after its branch's last run was cancelled", "cancelled", []string{"blocked: May I fix both findings in this round?", "working: fix round sent as decided"}, []string{"prompt", "tool"}, monitor.HealthBusy, "working", ""},
		{"waiting on another task's run after its own failed", "failed", []string{"working: decision implemented", "waiting on " + awaited + ": one gate test step at a time"}, []string{"settle"}, monitor.HealthIdle, "waiting", awaited},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: the goblin reports, its turn goes on or ends, and the
			// gate is read after both, as the supervisor read it.
			store, h := atWorkFixture(t, "claude", "task-1")
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.Mode = "no-mistakes"
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			gitFixture(t, meta.Worktree)
			now := time.Now().UTC()
			harnessHook(t, store, h, "claude", "task-1", "start", now.Add(-40*time.Minute))
			harnessHook(t, store, h, "claude", "task-1", "prompt", now.Add(-40*time.Minute))
			var log strings.Builder
			for i, report := range test.reports {
				log.WriteString(now.Add(time.Duration(i-3)*10*time.Minute).Format(time.RFC3339) + " " + report + "\n")
			}
			writeFile(t, state.StatusPath(h.State, "task-1"), log.String())
			for i, moment := range test.after {
				harnessHook(t, store, h, "claude", "task-1", moment, now.Add(-15*time.Minute+time.Duration(i)*14*time.Minute))
			}
			seeTerminal(t, h, "task-1", test.health)
			service := &Service{Store: store, Options: Options{Gate: fakeProgress{value: pipeline.Progress{RunID: "01JZTESTRUNOFTHISBRANCH00", Status: test.status, Steps: ended}}}}

			// Act
			evaluation, err := service.execute(context.Background(), Action{Kind: "evaluate", TaskID: "task-1", Generation: "g1"})
			if err != nil {
				t.Fatal(err)
			}
			evaluation.Generation, evaluation.At = "g1", now.Add(-10*time.Minute)
			store.mu.Lock()
			store.db.Tasks["task-1"] = evaluation
			err = store.save()
			store.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			got := snapshotTask(t, service, "task-1")

			// Assert
			if evaluation.Phase == "blocked" || evaluation.GateStep != "" {
				t.Errorf("a %s run reads as phase %q at gate step %q (%s), want no step and no decision", test.status, evaluation.Phase, evaluation.GateStep, evaluation.Reason)
			}
			if got.Phase != test.wantPhase || got.WaitingOn != test.wantWaiting {
				t.Errorf("card reads phase %q waiting on %q (%s), want %q waiting on %q", got.Phase, got.WaitingOn, got.Reason, test.wantPhase, test.wantWaiting)
			}
		})
	}
}

// The Overlord asked for every card to show its goblin's real state and
// follow it live. A card reads its gate from the task's evaluation, which the
// end of a turn asks for and a turn at work does not, so a gate decision the
// goblin answered mid-turn held its card at Blocked until the turn ended
// (#1305). The progress watch's reading of a gate run that moved has the
// evaluation read again while the turn runs, and a run that did not move
// starts no evaluation.
func TestAGateRunThatMovesMidTurnIsReadAgain(t *testing.T) {
	parked := pipeline.Progress{RunID: "01JZTESTRUNOFTHISBRANCH00", Status: "running", Steps: []pipeline.ProgressStep{{Name: "intent", Status: "completed"}, {Name: "review", Status: "awaiting_approval"}, {Name: "test", Status: "pending"}}}
	answered := parked
	answered.Steps = []pipeline.ProgressStep{{Name: "intent", Status: "completed"}, {Name: "review", Status: "fixing"}, {Name: "test", Status: "pending"}}
	cancelled := parked
	cancelled.Status = "cancelled"
	for _, test := range []struct {
		name         string
		after        pipeline.Progress
		wantQueued   int
		wantPhase    string
		wantGateStep string
	}{
		{"answered, its review fixing what it found", answered, 1, "review", "review"},
		{"cancelled at the decision", cancelled, 1, "working", ""},
		{"the decision still waiting", parked, 0, "blocked", "review"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: the goblin's turn ends with its gate parked on a
			// decision, which the progress watch reads.
			store, h := atWorkFixture(t, "claude", "task-1")
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.Mode = "no-mistakes"
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			gitFixture(t, meta.Worktree)
			gate := &fakeProgress{value: parked}
			head := strings.Repeat("a", 40)
			service := &Service{Store: store, Options: Options{Gate: gate, Progress: &progressGit{head: head, pushed: head}}}
			drain := func() {
				t.Helper()
				for store.HasRunnable() {
					if err := store.ProcessOne(context.Background(), service.execute); err != nil {
						t.Fatal(err)
					}
				}
			}
			now := time.Now().UTC()
			harnessHook(t, store, h, "claude", "task-1", "start", now.Add(-30*time.Minute))
			harnessHook(t, store, h, "claude", "task-1", "prompt", now.Add(-30*time.Minute))
			harnessHook(t, store, h, "claude", "task-1", "settle", now.Add(-20*time.Minute))
			drain()
			if err := service.checkFleet(t.Context(), now.Add(-20*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if got := snapshotTask(t, service, "task-1"); got.Phase != "blocked" || got.GateStep != "review" {
				t.Fatalf("card reads phase %q at gate step %q (%s) before its turn, want blocked at review", got.Phase, got.GateStep, got.Reason)
			}
			// The goblin takes a prompt and works on, and its gate run moves
			// or stays.
			harnessHook(t, store, h, "claude", "task-1", "prompt", now.Add(-10*time.Minute))
			harnessHook(t, store, h, "claude", "task-1", "tool", now.Add(-5*time.Minute))
			seeTerminal(t, h, "task-1", monitor.HealthBusy)
			gate.value = test.after

			// Act: the progress watch's next reading, and the work it queued.
			if err := service.checkFleet(t.Context(), now.Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			queued := 0
			for _, action := range store.Snapshot().Actions {
				if action.Kind == "evaluate" && action.Status == "queued" {
					queued++
				}
			}
			drain()
			got := snapshotTask(t, service, "task-1")

			// Assert
			d := store.Snapshot()
			if phase := d.Sessions[d.TaskSessions["task-1"]].Phase; phase != "active" {
				t.Fatalf("the goblin's session reads %q, want its turn still running", phase)
			}
			if queued != test.wantQueued {
				t.Errorf("the reading queued %d evaluations, want %d", queued, test.wantQueued)
			}
			if got.Phase != test.wantPhase || got.GateStep != test.wantGateStep {
				t.Errorf("card reads phase %q at gate step %q (%s), want %q at %q", got.Phase, got.GateStep, got.Reason, test.wantPhase, test.wantGateStep)
			}
		})
	}
}

// A goblin's question is its state while it waits on an answer: Waiting on
// the CFO while the CFO owes one, and Blocked once answered until the answer
// reaches the goblin, since an answer can be to stay held. Once the goblin
// takes the answer, the question holds the card no longer, and it reads
// Working before it writes a line of its own.
func TestAnAnsweredQuestionNoLongerHoldsTheCard(t *testing.T) {
	const question = "blocked: Which port should the board use? options: 8080 (Recommended) | 9090"
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, test := range []struct {
			name        string
			isAnswered  bool
			after       []string
			health      monitor.Health
			wantPhase   string
			isWaitingOn bool
		}{
			{"still unanswered", false, nil, monitor.HealthIdle, "blocked", true},
			{"answered, not yet with the goblin", true, nil, monitor.HealthIdle, "blocked", false},
			{"answered, and the goblin at work on the answer", true, []string{"prompt", "tool"}, monitor.HealthBusy, "working", false},
		} {
			t.Run(harness+"/"+test.name, func(t *testing.T) {
				// Arrange
				store, h := atWorkFixture(t, harness, "task-1")
				now := time.Now().UTC()
				harnessHook(t, store, h, harness, "task-1", "start", now.Add(-10*time.Minute))
				harnessHook(t, store, h, harness, "task-1", "prompt", now.Add(-10*time.Minute))
				writeFile(t, state.StatusPath(h.State, "task-1"), now.Add(-9*time.Minute).Format(time.RFC3339)+" working: reading the brief\n"+now.Add(-8*time.Minute).Format(time.RFC3339)+" "+question+"\n")
				record, _, err := wake.AppendDecision(h.State, "task-1", question)
				if err != nil {
					t.Fatal(err)
				}
				harnessHook(t, store, h, harness, "task-1", "settle", now.Add(-7*time.Minute))
				if test.isAnswered {
					if err := wake.MarkAnswered(h.State, record.Seq, wake.AnsweredByCFO, "8080"); err != nil {
						t.Fatal(err)
					}
				}
				for i, moment := range test.after {
					harnessHook(t, store, h, harness, "task-1", moment, now.Add(time.Duration(i-len(test.after))*time.Minute))
				}
				seeTerminal(t, h, "task-1", test.health)

				// Act
				got := snapshotTask(t, &Service{Store: store}, "task-1")

				// Assert
				if got.Phase != test.wantPhase || strings.HasPrefix(got.Reason, "Waiting on the CFO") != test.isWaitingOn {
					t.Fatalf("phase %q (%s), want %q waiting on the CFO %v", got.Phase, got.Reason, test.wantPhase, test.isWaitingOn)
				}
			})
		}
	}
}

// failedSpawn is the line cfo spawn writes when a goblin's terminal kept its
// instruction in the composer unsubmitted past the start's budget.
const failedSpawn = "failed: spawn: native terminal task-1 never showed its harness working on the instruction: not within 1m30s; its screen ends: > Read the brief"

// A goblin whose terminal kept its instruction unsubmitted wrote a failed
// spawn, and an Enter sent hours later started it working. Its card read
// Failed until it wrote a line of its own. A goblin whose harness works is
// not failed, whatever its last line says, and the failure stays in its
// history.
func TestAFailedSpawnFollowedByAWorkingHarnessIsWorking(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, test := range []struct {
			name      string
			after     []string
			health    monitor.Health
			wantPhase string
		}{
			{"the instruction still sits in its composer", nil, monitor.HealthIdle, "failed"},
			{"an Enter submitted the instruction", []string{"prompt", "tool"}, monitor.HealthBusy, "working"},
		} {
			t.Run(harness+"/"+test.name, func(t *testing.T) {
				// Arrange
				store, h := atWorkFixture(t, harness, "task-1")
				now := time.Now().UTC()
				harnessHook(t, store, h, harness, "task-1", "start", now.Add(-5*time.Hour))
				fileReport(t, h, "task-1", failedSpawn, now.Add(-5*time.Hour+2*time.Minute))
				for i, moment := range test.after {
					harnessHook(t, store, h, harness, "task-1", moment, now.Add(time.Duration(i-len(test.after))*time.Minute))
				}
				seeTerminal(t, h, "task-1", test.health)

				// Act
				got := snapshotTask(t, &Service{Store: store}, "task-1")

				// Assert
				if got.Phase != test.wantPhase {
					t.Fatalf("phase %q (%s), want %q", got.Phase, got.Reason, test.wantPhase)
				}
				if got.Report != "failed" || got.Activity != failedSpawn {
					t.Errorf("report %q activity %q, want the failed spawn kept", got.Report, got.Activity)
				}
			})
		}
	}
	t.Run("a harness with no native hooks, its terminal busy", func(t *testing.T) {
		// Arrange
		store, h := atWorkFixture(t, "codex", "task-1")
		fileReport(t, h, "task-1", failedSpawn, time.Now().Add(-5*time.Hour))
		seeTerminal(t, h, "task-1", monitor.HealthBusy)

		// Act
		got := snapshotTask(t, &Service{Store: store}, "task-1")

		// Assert
		if got.Phase != "working" {
			t.Fatalf("phase %q (%s), want working", got.Phase, got.Reason)
		}
	})
}

// A board left open sees a goblin's card change as the goblin does, over its
// event stream, without a reload: the failed goblin's card reads Working
// once the Enter sent to its terminal starts its harness.
func TestAnOpenBoardSeesAFailedGoblinWorkingWithoutAReload(t *testing.T) {
	// Arrange: a running supervisor and one board open on it, whose card
	// for the goblin reads Failed.
	_, h := atWorkFixture(t, "claude", "task-1")
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	gitFixture(t, meta.Worktree)
	started := time.Now().UTC()
	spool(t, h.Root, h.State, "SessionStart", started.Add(-5*time.Hour))
	fileReport(t, h, "task-1", failedSpawn, started.Add(-5*time.Hour+2*time.Minute))
	seeTerminal(t, h, "task-1", monitor.HealthIdle)
	s, err := Start(context.Background(), h, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := NewHTTP(s, "", nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phases := streamPhases(t, ctx, server.URL, "task-1")
	awaitPhase(t, phases, "failed", "the open board never read the failed spawn")

	// Act: an Enter submits the instruction, and the harness takes it and
	// works, as its hook and its terminal show.
	entered := time.Now().UTC()
	observation := monitor.Observation{TaskID: "task-1", Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: entered.Add(time.Second), Health: monitor.HealthBusy, Reason: monitor.None, Digest: "working", LastSeen: entered, LastProgress: entered}
	if err := monitor.WriteObservation(h.State, observation); err != nil {
		t.Fatal(err)
	}
	spool(t, h.Root, h.State, "UserPromptSubmit", entered)

	// Assert
	awaitPhase(t, phases, "working", "the open board never read the goblin working")
	t.Logf("the card read Working %s after the Enter", time.Since(entered).Round(time.Millisecond))
}

// spool has task-1's Claude session spool its hook event name at at, as its
// native hook does.
func spool(t *testing.T, root, stateDir, name string, at time.Time) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"hook_event_name": name, "session_id": "task-1-session", "cwd": filepath.Join(root, "work")})
	e, err := nativehook.Normalize(bytes.NewReader(payload), nativehook.Context{Harness: "claude", Role: "goblin", TaskID: "task-1", Generation: "g1", Now: at})
	if err != nil {
		t.Fatal(err)
	}
	if err := nativehook.Spool(stateDir, e); err != nil {
		t.Fatal(err)
	}
}

// streamPhases opens one board's event stream and reports the phase each
// snapshot it receives gives task.
func streamPhases(t *testing.T, ctx context.Context, url, task string) <-chan string {
	t.Helper()
	request, _ := http.NewRequestWithContext(ctx, "GET", url+"/api/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	phases := make(chan string, 64)
	go func() {
		reader := bufio.NewReaderSize(response.Body, 4<<20)
		name := ""
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			switch line = strings.TrimRight(line, "\n"); {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: ") && name == "snapshot":
				var snapshot Snapshot
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &snapshot) != nil {
					continue
				}
				if i := slices.IndexFunc(snapshot.Tasks, func(candidate Task) bool { return candidate.ID == task }); i >= 0 {
					phases <- snapshot.Tasks[i].Phase
				}
			}
		}
	}()
	return phases
}

// awaitPhase waits for a snapshot that gives the task phase.
func awaitPhase(t *testing.T, phases <-chan string, phase, failure string) {
	t.Helper()
	deadline := time.After(time.Minute)
	for {
		select {
		case got := <-phases:
			if got == phase {
				return
			}
		case <-deadline:
			t.Fatal(failure)
		}
	}
}
