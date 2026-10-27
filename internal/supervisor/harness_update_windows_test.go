package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// standInScreen is what the stand-in terminals show, which a test changes
// between looks.
type standInScreen struct {
	mu   sync.Mutex
	rows []string
}

type unrelatedGate struct{ calls int }

func (gate *unrelatedGate) Progress(context.Context, string, string) (pipeline.Progress, error) {
	gate.calls++
	return pipeline.Progress{}, errors.New("unrelated gate unavailable")
}

func (*unrelatedGate) CanSteer(context.Context, string, string, string) error { return nil }

func (s *standInScreen) show(rows []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = rows
}

func (s *standInScreen) read(host.Record) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.rows), nil
}

// recordTerminal records a host for native terminal id that runs in this test
// process, whose program started at childStart.
func recordTerminal(t *testing.T, stateDir, id string, childStart time.Time) {
	t.Helper()
	recordTerminalStarted(t, stateDir, id, childStart, time.Now().UTC())
}

// recordTerminalStarted records the same host, opened at started.
func recordTerminalStarted(t *testing.T, stateDir, id string, childStart, started time.Time) {
	t.Helper()
	data, err := json.Marshal(host.Record{ID: id, Pipe: `\\.\pipe\stand-in-` + id, Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: os.Getpid(), ChildStart: childStart, Started: started})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", id+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// updateBoard is a board whose CFO runs Claude Code in native terminal cfo,
// stood in for by this test process, whose restarts the first-run machine
// counts, and whose terminals show screen.
func updateBoard(t *testing.T) (*HTTP, *firstRunMachine, *standInScreen) {
	t.Helper()
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	machine := newFirstRunMachine(t)
	machine.runs = true
	screen := &standInScreen{}
	s := handler.Service
	s.Options.FirstRun = machine.run
	s.Options.ReadScreen = screen.read
	s.Options.ProgramInstalled = func(harness.Kind) (string, time.Time, error) {
		return `C:\Tools\claude.exe`, time.Now().Add(-48 * time.Hour), nil
	}
	nativePrimary(t, h.State)
	recordTerminal(t, h.State, NativeCFOTerminal, time.Now().Add(-time.Hour))
	return handler, machine, screen
}

// A selection saved while the CFO's terminal holds nothing registered in it
// waits for that terminal's CFO to start and register, rather than being read
// as one a session it never replaced left behind.
func TestASelectionSavedForAStartingTerminalWaitsForItsCFO(t *testing.T) {
	// Arrange: a native terminal that runs with no CFO registered in it yet,
	// which is what a Codex or pi CFO is until its first prompt registers it.
	handler, h := startBoard(t, 5*gigabyte, &spawnRecorder{})
	machine := newFirstRunMachine(t)
	machine.runs = true
	s := handler.Service
	s.Options.FirstRun = machine.run
	s.Options.CFOJobActivity = func(host.Record) (string, error) { return "", nil }
	hostTerminal(t, h.State, NativeCFOTerminal)
	if cfo := readCFOState(h.State); !cfo.starting || cfo.registered {
		t.Fatalf("cfo state = %+v, want a starting terminal with nothing registered", cfo)
	}
	if err := os.WriteFile(filepath.Join(h.State, "cfo-engine.json"), []byte(`{"harness":"codex","model":"selected-model","effort":"high","pending":true,"since":"2026-10-26T20:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	s.applyCFOEngine(t.Context())

	// Assert
	choice, err := ReadCFOEngine(h.State)
	if err != nil || !choice.Pending || choice.Problem != "" || !strings.Contains(choice.Waiting, "terminal to start") || machine.restarted != 0 {
		t.Fatalf("choice=%+v err=%v restarted=%d, want the selection still pending on a starting terminal", choice, err, machine.restarted)
	}
}

func pressCFOUpdate(handler *HTTP, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://board.local/api/cfo/update", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://board.local")
	request.Header.Set("X-CFO-Token", orderToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func cfoUpdateOf(t *testing.T, s *Service) *CFOUpdate {
	t.Helper()
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.CFOUpdate
}

func TestCFOEngineSelectionWaitsForIdleAndAnEmptyComposer(t *testing.T) {
	handler, machine, screen := updateBoard(t)
	if err := os.WriteFile(filepath.Join(machine.home, ".codex", "models_cache.json"), []byte(`{"models":[{"slug":"selected-model","visibility":"list","supported_reasoning_levels":[{"effort":"high"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	screen.show(append([]string{"✻ Working… (12s · esc to interrupt)"}, claudeAtPrompt("")...))
	response := taskControlRequest(handler, "/api/cfo/engine", map[string]string{"harness": "codex", "model": "selected-model", "effort": "high"})
	if response.Code != http.StatusOK {
		t.Fatalf("selection = %d %s", response.Code, response.Body)
	}
	handler.Service.applyCFOEngine(t.Context())
	if machine.restarted != 0 {
		t.Fatal("selection interrupted the active turn")
	}
	screen.show(claudeAtPrompt("unsent draft"))
	handler.Service.applyCFOEngine(t.Context())
	if machine.restarted != 0 {
		t.Fatal("selection discarded a draft")
	}
	isGateRunning := true
	handler.Service.Options.CFOJobActivity = func(host.Record) (string, error) {
		if isGateRunning {
			return "Waiting for the CFO's gate command to finish", nil
		}
		return "", nil
	}
	meta := state.TaskMeta{ID: "gate-task", SpawnGen: "s1", Mode: "no-mistakes", Worktree: handler.Service.Store.Home.Root, Project: handler.Service.Store.Home.Root}
	if err := state.WriteTaskMeta(handler.Service.Store.Home.State, meta); err != nil {
		t.Fatal(err)
	}
	screen.show(claudeAtPrompt(""))
	handler.Service.applyCFOEngine(t.Context())
	if machine.restarted != 0 {
		t.Fatal("selection interrupted an active gate")
	}
	choice, err := ReadCFOEngine(handler.Service.Store.Home.State)
	if err != nil || !strings.Contains(choice.Waiting, "gate command") {
		t.Fatalf("pending choice = %+v, %v; want an actionable gate reason", choice, err)
	}
	isGateRunning = false
	handler.Service.applyCFOEngine(t.Context())
	handler.Service.applyCFOEngine(t.Context())
	if machine.restarted != 1 {
		t.Fatalf("restarted %d times, want once at the safe boundary", machine.restarted)
	}
}

// The restart that changed the CFO's session applied the saved selection on
// the harness it names, so the board is left with it applied and nothing
// pending.
func TestCFOEngineSelectionTheCFOAlreadyRunsLeavesNothingPending(t *testing.T) {
	// Arrange
	handler, machine, _ := updateBoard(t)
	stateDir := handler.Service.Store.Home.State
	if err := os.WriteFile(filepath.Join(machine.home, ".codex", "models_cache.json"), []byte(`{"models":[{"slug":"selected-model","visibility":"list","supported_reasoning_levels":[{"effort":"high"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	response := taskControlRequest(handler, "/api/cfo/engine", map[string]string{"harness": "codex", "model": "selected-model", "effort": "high"})
	if response.Code != http.StatusOK {
		t.Fatalf("selection = %d %s", response.Code, response.Body)
	}
	saved, err := ReadCFOEngine(stateDir)
	if err != nil || !saved.Pending {
		t.Fatalf("saved choice = %+v, %v; want it pending", saved, err)
	}

	// Act: the CFO comes back as codex in a session that began after the save.
	nativePrimaryAs(t, stateDir, "codex")
	recordTerminal(t, stateDir, NativeCFOTerminal, time.Now().Add(-time.Minute))
	handler.Service.applyCFOEngine(t.Context())

	// Assert
	choice, err := ReadCFOEngine(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Pending || choice.Problem != "" {
		t.Fatalf("choice = %+v, want the selection the CFO now runs, with nothing pending", choice)
	}
	if choice.Model != "selected-model" || choice.Effort != "high" || machine.restarted != 0 {
		t.Fatalf("choice = %+v restarted = %d; want the same engine kept and nothing restarted", choice, machine.restarted)
	}
}

// A selection saved while the CFO's native terminal runs and nothing has
// registered in it yet belongs to the CFO's next start, so it is saved
// pending with no session of its own and waits there, never reading as the
// engine a running session already runs.
func TestCFOEngineSelectionSavedWhileTheCFOStartsStaysPending(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	machine := newFirstRunMachine(t)
	machine.runs = true
	handler.Service.Options.FirstRun = machine.run
	if err := os.WriteFile(filepath.Join(machine.home, ".codex", "models_cache.json"), []byte(`{"models":[{"slug":"selected-model","visibility":"list","supported_reasoning_levels":[{"effort":"high"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hostTerminal(t, h.State, NativeCFOTerminal).standIn(t)

	// Act
	response := taskControlRequest(handler, "/api/cfo/engine", map[string]string{"harness": "codex", "model": "selected-model", "effort": "high"})

	// Assert
	if response.Code != http.StatusOK {
		t.Fatalf("selection = %d %s", response.Code, response.Body)
	}
	var saved CFOEngine
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Pending || !saved.Since.IsZero() {
		t.Fatalf("saved = %+v, want a pending selection with no session to replace", saved)
	}
	handler.Service.applyCFOEngine(t.Context())
	choice, err := ReadCFOEngine(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if !choice.Pending || choice.Problem != "" || choice.Waiting == "" || machine.restarted != 0 {
		t.Fatalf("choice = %+v restarted = %d; want it pending and waiting for the CFO's next start", choice, machine.restarted)
	}
}

// A pending selection saved before the CFO's session was known adopts the
// session it finds as the one to replace, keeping its wait, and the restart
// that replaces that session clears it.
func TestCFOEngineSelectionWithoutASessionAdoptsItAndWaits(t *testing.T) {
	// Arrange
	handler, machine, screen := updateBoard(t)
	stateDir := handler.Service.Store.Home.State
	if err := writeCFOEngine(stateDir, CFOEngine{Harness: "codex", Model: "selected-model", Effort: "high", Pending: true}); err != nil {
		t.Fatal(err)
	}
	running := readCFOState(stateDir).since
	screen.show(append([]string{"✻ Reticulating… (12s · esc to interrupt)"}, claudeAtPrompt("")...))

	// Act
	handler.Service.applyCFOEngine(t.Context())

	// Assert
	choice, err := ReadCFOEngine(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !choice.Since.Equal(running) {
		t.Fatalf("since = %v, want the session it found, %v", choice.Since, running)
	}
	if !choice.Pending || choice.Problem != "" || choice.Waiting == "" || machine.restarted != 0 {
		t.Fatalf("choice = %+v restarted = %d; want it pending and waiting, never read as applied", choice, machine.restarted)
	}

	// Act: the CFO comes back as codex in a session that began after that one.
	nativePrimaryAs(t, stateDir, "codex")
	recordTerminalStarted(t, stateDir, NativeCFOTerminal, time.Now().Add(-time.Minute), running.Add(time.Minute))
	handler.Service.applyCFOEngine(t.Context())

	// Assert
	choice, err = ReadCFOEngine(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Pending || choice.Problem != "" {
		t.Fatalf("choice = %+v, want the restart that replaced the session to clear it", choice)
	}
}

// A session change that did not consume the saved selection, a restart as
// another harness or a session that began before it was saved, leaves it
// saved for the CFO's next start and says so.
func TestCFOEngineSelectionAnotherSessionKeepsItForTheNextStart(t *testing.T) {
	for name, test := range map[string]struct {
		harness string
		started func(time.Time) time.Time
	}{
		"a restart as another harness": {"claude", func(saved time.Time) time.Time { return saved.Add(time.Minute) }},
		"a session before the save":    {"codex", func(saved time.Time) time.Time { return saved.Add(-2 * time.Hour) }},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			handler, machine, _ := updateBoard(t)
			stateDir := handler.Service.Store.Home.State
			if err := os.WriteFile(filepath.Join(machine.home, ".codex", "models_cache.json"), []byte(`{"models":[{"slug":"selected-model","visibility":"list","supported_reasoning_levels":[{"effort":"high"}]}]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			response := taskControlRequest(handler, "/api/cfo/engine", map[string]string{"harness": "codex", "model": "selected-model", "effort": "high"})
			if response.Code != http.StatusOK {
				t.Fatalf("selection = %d %s", response.Code, response.Body)
			}
			saved, err := ReadCFOEngine(stateDir)
			if err != nil || !saved.Pending {
				t.Fatalf("saved choice = %+v, %v; want it pending", saved, err)
			}

			// Act
			nativePrimaryAs(t, stateDir, test.harness)
			recordTerminalStarted(t, stateDir, NativeCFOTerminal, time.Now().Add(-time.Minute), test.started(saved.Since))
			handler.Service.applyCFOEngine(t.Context())

			// Assert
			choice, err := ReadCFOEngine(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			if choice.Pending || !strings.Contains(choice.Problem, "saved for its next start") || machine.restarted != 0 {
				t.Fatalf("choice = %+v restarted = %d; want it saved for the next start and nothing restarted", choice, machine.restarted)
			}
		})
	}
}

// A selection the installed catalog no longer offers at the safe boundary
// says it stays saved for the CFO's next start, and it does.
func TestCFOEngineSelectionThatFailsAtApplyStaysSavedForTheNextStart(t *testing.T) {
	// Arrange
	handler, machine, screen := updateBoard(t)
	stateDir := handler.Service.Store.Home.State
	handler.Service.Options.CFOJobActivity = func(host.Record) (string, error) { return "", nil }
	catalog := func(models string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(machine.home, ".codex", "models_cache.json"), []byte(models), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	catalog(`{"models":[{"slug":"selected-model","visibility":"list","supported_reasoning_levels":[{"effort":"high"}]}]}`)
	screen.show(claudeAtPrompt(""))
	response := taskControlRequest(handler, "/api/cfo/engine", map[string]string{"harness": "codex", "model": "selected-model", "effort": "high"})
	if response.Code != http.StatusOK {
		t.Fatalf("selection = %d %s", response.Code, response.Body)
	}

	// Act: the model leaves the installed catalog before the CFO's turn ends.
	catalog(`{"models":[{"slug":"another-model","visibility":"list","supported_reasoning_levels":[{"effort":"high"}]}]}`)
	handler.Service.applyCFOEngine(t.Context())

	// Assert
	choice, err := ReadCFOEngine(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Pending || machine.restarted != 0 {
		t.Fatalf("choice = %+v restarted = %d; want nothing pending and nothing restarted", choice, machine.restarted)
	}
	for _, want := range []string{"stays saved", "selected-model", "high", "next start"} {
		if !strings.Contains(choice.Problem, want) {
			t.Fatalf("problem = %q, want it to name %q", choice.Problem, want)
		}
	}
	if choice.Model != "selected-model" || choice.Effort != "high" {
		t.Fatalf("choice = %+v; want the selection kept for the CFO's next start", choice)
	}
}

func TestCFOEngineSelectionIgnoresUnrelatedTaskGateRecords(t *testing.T) {
	for _, kind := range []string{"absent", "paused", "detached", "stale", "slow"} {
		t.Run(kind, func(t *testing.T) {
			handler, machine, screen := updateBoard(t)
			handler.Service.Options.CFOJobActivity = func(host.Record) (string, error) { return "", nil }
			gate := &unrelatedGate{}
			handler.Service.Options.Gate = gate
			if err := os.WriteFile(filepath.Join(machine.home, ".codex", "models_cache.json"), []byte(`{"models":[{"slug":"selected-model","visibility":"list","supported_reasoning_levels":[{"effort":"high"}]}]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if kind != "absent" {
				meta := state.TaskMeta{ID: "other-task", SpawnGen: "s1", Mode: "no-mistakes", Worktree: filepath.Join(t.TempDir(), kind), Project: t.TempDir()}
				if kind == "detached" {
					meta.Worktree = gitFixture(t)
					command := exec.Command("git", "checkout", "--quiet", "--detach")
					command.Dir = meta.Worktree
					if output, err := command.CombinedOutput(); err != nil {
						t.Fatalf("detach fixture: %s %v", output, err)
					}
				}
				if err := state.WriteTaskMeta(handler.Service.Store.Home.State, meta); err != nil {
					t.Fatal(err)
				}
				if kind == "paused" {
					if err := state.WriteLifecycle(handler.Service.Store.Home.State, state.Lifecycle{ID: meta.ID, Generation: "s1", Operation: "pause-1", Action: "pause", Phase: "paused"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			screen.show(claudeAtPrompt(""))
			response := taskControlRequest(handler, "/api/cfo/engine", map[string]string{"harness": "codex", "model": "selected-model", "effort": "high"})
			if response.Code != http.StatusOK {
				t.Fatalf("selection = %d %s", response.Code, response.Body)
			}
			handler.Service.applyCFOEngine(t.Context())
			if machine.restarted != 1 || gate.calls != 0 {
				t.Fatalf("unrelated %s record: restarted=%d gate reads=%d", kind, machine.restarted, gate.calls)
			}
		})
	}
}

func TestCFOEngineSelectionReportsUnverifiableTerminalCustody(t *testing.T) {
	handler, machine, screen := updateBoard(t)
	handler.Service.Options.CFOJobActivity = func(host.Record) (string, error) { return "", errors.New("access denied") }
	if err := os.WriteFile(filepath.Join(machine.home, ".codex", "models_cache.json"), []byte(`{"models":[{"slug":"selected-model","visibility":"list","supported_reasoning_levels":[{"effort":"high"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	screen.show(claudeAtPrompt(""))
	response := taskControlRequest(handler, "/api/cfo/engine", map[string]string{"harness": "codex", "model": "selected-model", "effort": "high"})
	if response.Code != http.StatusOK {
		t.Fatalf("selection = %d %s", response.Code, response.Body)
	}
	handler.Service.applyCFOEngine(t.Context())
	choice, err := ReadCFOEngine(handler.Service.Store.Home.State)
	if err != nil || !choice.Pending || !strings.Contains(choice.Waiting, "access denied") || machine.restarted != 0 {
		t.Fatalf("choice=%+v err=%v restarted=%d", choice, err, machine.restarted)
	}
}

// Claude Code's "Update installed · Restart to update" in the CFO's footer
// puts Update on the CFO's header and restarts nothing by itself. His press
// waits while the CFO is in a turn, then restarts the CFO once, on its
// conversation, through the board's Restart CFO path, as soon as its turn has
// ended; the press is then spent.
func TestTheCFOsUpdateRestartsItOnlyAtHisPressAndOnlyOnceItsTurnHasEnded(t *testing.T) {
	// Arrange
	handler, machine, screen := updateBoard(t)
	s := handler.Service
	footer := []string{"  ⏵⏵ auto mode on (shift+tab to cycle)", "  ✓ Update installed · Restart to update"}
	idle := claudeAtPrompt("", footer...)
	turn := append([]string{"✻ Reticulating… (12s · esc to interrupt)"}, idle...)
	screen.show(idle)

	// Act: an update waits, and nobody pressed it.
	s.lookForHarnessUpdates()
	s.applyCFOUpdate(t.Context())

	// Assert
	update := cfoUpdateOf(t, s)
	if update == nil || update.Line != "✓ Update installed · Restart to update" || update.Pending || machine.restarted != 0 {
		t.Fatalf("before the press: update %+v, restarted %d; want Update offered and nothing restarted", update, machine.restarted)
	}

	// Act: he presses Update while the CFO is in a turn.
	screen.show(turn)
	response := pressCFOUpdate(handler, "{}")
	s.applyCFOUpdate(t.Context())

	// Assert
	if response.Code != http.StatusAccepted || machine.restarted != 0 {
		t.Fatalf("press mid-turn = %d %s, restarted %d; want it accepted and waiting", response.Code, response.Body, machine.restarted)
	}
	if update := cfoUpdateOf(t, s); update == nil || !update.Pending {
		t.Fatalf("after the press: update %+v, want it pending", update)
	}

	// Act: its turn ends.
	screen.show(idle)
	s.applyCFOUpdate(t.Context())
	s.applyCFOUpdate(t.Context())

	// Assert
	if machine.restarted != 1 {
		t.Fatalf("restarted %d times at the end of its turn, want once", machine.restarted)
	}
	if update := cfoUpdateOf(t, s); update != nil && (update.Pending || update.Updating || update.Problem != "") {
		t.Errorf("after the restart: update %+v, want the press spent", update)
	}
}

// A press is for the CFO it was made for: once that CFO's terminal was
// replaced, by a restart of its own or a new CFO, the press is dropped and
// restarts nothing. With no update waiting, Update is refused, and a press
// taken back restarts nothing either.
func TestTheCFOsUpdatePressNeverOutlivesTheCFOItWasFor(t *testing.T) {
	t.Run("a terminal started again", func(t *testing.T) {
		// Arrange
		handler, machine, screen := updateBoard(t)
		s := handler.Service
		screen.show(claudeAtPrompt("", "  ✓ Update installed · Restart to update"))
		s.lookForHarnessUpdates()
		if response := pressCFOUpdate(handler, "{}"); response.Code != http.StatusAccepted {
			t.Fatalf("press = %d %s", response.Code, response.Body)
		}
		recordTerminal(t, s.Store.Home.State, NativeCFOTerminal, time.Now())

		// Act
		s.lookForHarnessUpdates()
		s.applyCFOUpdate(t.Context())

		// Assert
		if _, err := os.Stat(filepath.Join(s.Store.Home.State, cfoUpdateFile)); !os.IsNotExist(err) || machine.restarted != 0 {
			t.Errorf("press file: %v, restarted %d; want the press dropped and nothing restarted", err, machine.restarted)
		}
	})
	t.Run("no update waits", func(t *testing.T) {
		// Arrange
		handler, machine, screen := updateBoard(t)
		screen.show(claudeAtPrompt("", "  ⏵⏵ auto mode on (shift+tab to cycle)"))
		handler.Service.lookForHarnessUpdates()

		// Act
		response := pressCFOUpdate(handler, "{}")

		// Assert
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "No harness update waits for the CFO") || machine.restarted != 0 {
			t.Errorf("press = %d %s, restarted %d; want it refused", response.Code, response.Body, machine.restarted)
		}
	})
	t.Run("a press taken back", func(t *testing.T) {
		// Arrange
		handler, machine, screen := updateBoard(t)
		s := handler.Service
		screen.show(claudeAtPrompt("", "  ✓ Update installed · Restart to update"))
		s.lookForHarnessUpdates()
		pressCFOUpdate(handler, "{}")

		// Act
		response := pressCFOUpdate(handler, `{"cancel":true}`)
		s.applyCFOUpdate(t.Context())

		// Assert
		if response.Code != http.StatusOK || machine.restarted != 0 {
			t.Errorf("cancel = %d %s, restarted %d; want nothing restarted", response.Code, response.Body, machine.restarted)
		}
		if update := cfoUpdateOf(t, s); update == nil || update.Pending {
			t.Errorf("after the cancel: update %+v, want Update offered again", update)
		}
	})
}

// A goblin whose harness was installed again after it started has Update on
// its card. His press waits for the end of its turn, with no gate step,
// background shell, monitor or sub-agent of its own running, and then
// restarts it in place with its own harness, model and effort, on its own
// conversation, through cfo switch --restart, once.
func TestAGoblinsUpdateRestartsItInPlaceOnlyAtTheEndOfItsTurn(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	s := handler.Service
	s.Options.FirstRun = newFirstRunMachine(t).run
	started := time.Now().Add(-2 * time.Hour)
	s.Options.ReadScreen = (&standInScreen{}).read
	s.Options.ProgramInstalled = func(harness.Kind) (string, time.Time, error) {
		return `C:\Tools\claude.exe`, started.Add(time.Hour), nil
	}
	isIdle := false
	s.Options.Dispatch.Idle = func(context.Context, state.TaskMeta) (bool, error) { return isIdle, nil }
	s.Options.Gate = &engineGate{}
	meta := state.TaskMeta{ID: "goblin", SpawnGen: "s1", Harness: "claude", Model: "opus", Effort: "xhigh", Backend: "native", Worktree: h.Root, Project: h.Root}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, meta.Worktree)
	recordTerminal(t, h.State, meta.ID, started)
	s.lookForHarnessUpdates()
	if card := engineCard(t, s, meta.ID); card.HarnessUpdate == nil || !card.HarnessUpdate.Installed.Equal(started.Add(time.Hour)) {
		t.Fatalf("card update = %+v, want the install since it started", card.HarnessUpdate)
	}

	// Act: he presses Update mid-turn.
	response := taskControlRequest(handler, "/api/tasks/engine", map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "when": "update"})
	now := time.Now().UTC()
	if err := s.applyEngineChoices(t.Context(), now); err != nil {
		t.Fatal(err)
	}

	// Assert
	choice, err := state.ReadEngineChoice(h.State, meta.ID)
	if response.Code != http.StatusAccepted || err != nil || choice.When != "update" || choice.Harness != "claude" || choice.Model != "opus" || choice.Effort != "xhigh" || len(spawner.recorded()) != 0 {
		t.Fatalf("press = %d %s, choice %+v %v, calls %+v; want its own values waiting for the end of its turn", response.Code, response.Body, choice, err, spawner.recorded())
	}
	if card := engineCard(t, s, meta.ID); card.PendingEngine == nil || card.PendingEngine.When != "update" {
		t.Fatalf("card pending = %+v, want the update pending", card.PendingEngine)
	}

	// Act: the turn ends, but a monitor of its own still runs.
	isIdle = true
	s.mu.Lock()
	s.trees = map[string]fleettree.Tree{meta.ID: {TaskID: meta.ID, Generation: meta.SpawnGen, Children: []fleettree.Node{{ID: "monitor:m1", Kind: fleettree.KindMonitor, State: fleettree.Working}}}}
	s.mu.Unlock()
	for _, elapsed := range []time.Duration{time.Second, 3 * time.Second} {
		if err := s.applyEngineChoices(t.Context(), now.Add(elapsed)); err != nil {
			t.Fatal(err)
		}
	}

	// Assert: a switch is marked changing before its command runs.
	s.starts.Lock()
	isChanging := s.changing[meta.ID] != ""
	s.starts.Unlock()
	if isChanging || len(spawner.recorded()) != 0 {
		t.Fatalf("changing %v, calls %+v; want nothing restarted while its monitor runs", isChanging, spawner.recorded())
	}

	// Act: its monitor ends.
	s.mu.Lock()
	s.trees = map[string]fleettree.Tree{meta.ID: {TaskID: meta.ID, Generation: meta.SpawnGen, Children: []fleettree.Node{{ID: "monitor:m1", Kind: fleettree.KindMonitor, State: fleettree.Done}}}}
	s.mu.Unlock()
	for _, elapsed := range []time.Duration{4 * time.Second, 6 * time.Second} {
		if err := s.applyEngineChoices(t.Context(), now.Add(elapsed)); err != nil {
			t.Fatal(err)
		}
	}
	waitEngineChange(t, s, meta.ID)

	// Assert
	want := []string{"switch", meta.ID, "--generation", meta.SpawnGen, "--restart"}
	if calls := spawner.recorded(); len(calls) != 1 || !slices.Equal(calls[0], want) {
		t.Fatalf("calls %+v; want one %q", calls, want)
	}
}

// Nothing restarts by itself: with updates waiting for the CFO and a goblin
// and AFK mode on, every pass of the supervisor that could restart one, and
// AFK mode's own, restarts neither.
func TestNothingRestartsOntoAnUpdateByItselfEvenInAFKMode(t *testing.T) {
	// Arrange
	handler, machine, screen := updateBoard(t)
	s := handler.Service
	h := s.Store.Home
	spawner := &spawnRecorder{}
	s.Options.Dispatch.Spawn = spawner.spawn
	s.Options.Dispatch.Idle = func(context.Context, state.TaskMeta) (bool, error) { return true, nil }
	s.Options.Gate = &engineGate{}
	screen.show(claudeAtPrompt("", "  ✓ Update installed · Restart to update"))
	meta := state.TaskMeta{ID: "goblin", SpawnGen: "s1", Harness: "claude", Model: "opus", Effort: "xhigh", Backend: "native", Worktree: h.Root, Project: h.Root}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	recordTerminal(t, h.State, meta.ID, time.Now().Add(-time.Hour))
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Act
	now := time.Now().UTC()
	for elapsed := range 4 {
		s.lookForHarnessUpdates()
		s.applyCFOUpdate(t.Context())
		if err := s.applyEngineChoices(t.Context(), now.Add(time.Duration(elapsed)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := s.holdForOverlord(now.Add(time.Duration(elapsed) * 2 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	card := engineCard(t, s, meta.ID)
	if snapshot.CFOUpdate == nil || card.HarnessUpdate == nil {
		t.Fatalf("CFO update %+v, goblin update %+v; want both offered", snapshot.CFOUpdate, card.HarnessUpdate)
	}
	if machine.restarted != 0 || len(spawner.recorded()) != 0 || card.PendingEngine != nil || snapshot.CFOUpdate.Pending {
		t.Errorf("restarted %d, calls %+v, pending %+v / %v; want nothing pressed and nothing restarted", machine.restarted, spawner.recorded(), card.PendingEngine, snapshot.CFOUpdate.Pending)
	}
}

// screenShows waits until terminal id's screen, read through its host, has a
// row holding text, and says whether it has one with working.
func screenShows(t *testing.T, stateDir, id, text string, working bool) {
	t.Helper()
	record, err := host.ReadRecord(stateDir, id)
	if err != nil {
		t.Fatal(err)
	}
	screens, _ := harness.NativeScreens(harness.Claude)
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(20 * time.Millisecond) {
		if rows, err := host.ReadScreen(record); err == nil && slices.ContainsFunc(rows, func(row string) bool { return strings.Contains(row, text) }) && screens.IsWorking(rows) == working {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal %s never showed %q with working %v", id, text, working)
		}
	}
}

// The same through a real terminal: a stand-in Claude Code CFO in native
// terminal cfo draws its footer's update line, which the supervisor reads
// through the terminal's host, and his press waits out the turn the stand-in
// draws before the CFO is restarted, once.
func TestTheCFOsUpdateIsReadFromItsTerminalAndWaitsOutItsTurn(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	machine := newFirstRunMachine(t)
	machine.runs = true
	s := handler.Service
	s.Options.FirstRun = machine.run
	s.Options.ProgramInstalled = func(harness.Kind) (string, time.Time, error) {
		return `C:\Tools\claude.exe`, time.Now().Add(-48 * time.Hour), nil
	}
	cfo := hostTerminal(t, h.State, NativeCFOTerminal)
	cfo.standIn(t)
	nativePrimary(t, h.State)
	cfo.typeLine(t, "update installed")
	screenShows(t, h.State, NativeCFOTerminal, "✓ Update installed · Restart to update", false)

	// Act
	s.lookForHarnessUpdates()
	response := pressCFOUpdate(handler, "{}")
	cfo.typeLine(t, "turn")
	screenShows(t, h.State, NativeCFOTerminal, "Pondering", true)
	s.applyCFOUpdate(t.Context())

	// Assert
	if update := cfoUpdateOf(t, s); response.Code != http.StatusAccepted || update == nil || update.Line != "✓ Update installed · Restart to update" || !update.Pending || machine.restarted != 0 {
		t.Fatalf("press = %d %s, update %+v, restarted %d; want the press waiting out the turn", response.Code, response.Body, update, machine.restarted)
	}

	// Act
	cfo.typeLine(t, "update installed")
	screenShows(t, h.State, NativeCFOTerminal, "✓ Update installed · Restart to update", false)
	s.applyCFOUpdate(t.Context())

	// Assert
	if machine.restarted != 1 {
		t.Fatalf("restarted %d times once the turn ended, want once", machine.restarted)
	}
}

// npmInstalled writes npm's global layout for a harness into directory: the
// script shim npm linked at linked, and the package npm unpacked under
// node_modules at installed.
func npmInstalled(t *testing.T, directory string, kind harness.Kind, pkg string, linked, installed time.Time) {
	t.Helper()
	shim := filepath.Join(directory, string(kind)+".cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(shim, linked, linked); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(directory, "node_modules", filepath.FromSlash(pkg), "package.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(manifest, installed, installed); err != nil {
		t.Fatal(err)
	}
}

// npmPackageManifest is the manifest of the package npm installed in
// directory, which the harness's own updater writes again.
func npmPackageManifest(directory, pkg string) string {
	return filepath.Join(directory, "node_modules", filepath.FromSlash(pkg), "package.json")
}

// npmOnPath puts npm's global folder first on PATH, so a harness is found
// through the shim written there, with the rest of PATH kept for git.
func npmOnPath(t *testing.T, directory string) {
	t.Helper()
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Codex and pi are installed from npm, whose shim npm writes once when it
// links the bin: an install by the harness's own updater (codex update, pi
// update) writes the package under the shim and leaves the shim's own time
// alone. The supervisor reads the package, so the shim npm linked again since
// a goblin started raises no update, and a package its updater wrote does.
func TestACodexOrPiUpdateIsSeenFromItsPackageWhateverInstalledIt(t *testing.T) {
	for kind, pkg := range map[string]string{
		"codex": "@openai/codex",
		"pi":    "@earendil-works/pi-coding-agent",
	} {
		t.Run(kind, func(t *testing.T) {
			// Arrange
			handler, h := startBoard(t, 5*gigabyte, &spawnRecorder{})
			s := handler.Service
			s.Options.ReadScreen = (&standInScreen{}).read
			started := time.Now().Add(-2 * time.Hour).UTC()
			directory := t.TempDir()
			npmInstalled(t, directory, harness.Kind(kind), pkg, started.Add(30*time.Minute), started.Add(-time.Hour))
			npmOnPath(t, directory)
			meta := state.TaskMeta{ID: "goblin", SpawnGen: "s1", Harness: kind, Model: "m", Effort: "high", Backend: "native", Worktree: h.Root, Project: h.Root}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			gitFixture(t, meta.Worktree)
			recordTerminal(t, h.State, meta.ID, started)

			// Act: npm linked the shim again since it started, and the package is older.
			s.lookForHarnessUpdates()

			// Assert
			if card := engineCard(t, s, meta.ID); card.HarnessUpdate != nil {
				t.Fatalf("card update = %+v, want none: only the shim was written since it started", card.HarnessUpdate)
			}

			// Act: its own updater writes the package, and the shim does not move.
			updated := started.Add(time.Hour)
			if err := os.Chtimes(npmPackageManifest(directory, pkg), updated, updated); err != nil {
				t.Fatal(err)
			}
			s.lookForHarnessUpdates()

			// Assert
			if card := engineCard(t, s, meta.ID); card.HarnessUpdate == nil || !card.HarnessUpdate.Installed.Equal(updated) {
				t.Fatalf("card update = %+v, want the install at %s by its own updater", card.HarnessUpdate, updated)
			}
		})
	}
}

// A Codex CFO is read the same way, so its header offers Update on an install
// written since its terminal started and on nothing else.
func TestACodexCFOGetsUpdateWhenItsPackageWasInstalledSinceItStarted(t *testing.T) {
	// Arrange
	handler, h := startBoard(t, 5*gigabyte, &spawnRecorder{})
	s := handler.Service
	s.Options.ReadScreen = (&standInScreen{}).read
	started := time.Now().Add(-2 * time.Hour).UTC()
	directory := t.TempDir()
	npmInstalled(t, directory, harness.Codex, "@openai/codex", started.Add(30*time.Minute), started.Add(-time.Hour))
	npmOnPath(t, directory)
	nativePrimaryAs(t, h.State, "codex")
	recordTerminal(t, h.State, NativeCFOTerminal, started)

	// Act: npm linked the shim again since it started, and the package is older.
	s.lookForHarnessUpdates()

	// Assert
	if update := cfoUpdateOf(t, s); update != nil {
		t.Fatalf("CFO update = %+v, want none: only the shim was written since it started", update)
	}

	// Act: its own updater writes the package.
	updated := started.Add(time.Hour)
	if err := os.Chtimes(npmPackageManifest(directory, "@openai/codex"), updated, updated); err != nil {
		t.Fatal(err)
	}
	s.lookForHarnessUpdates()

	// Assert
	if update := cfoUpdateOf(t, s); update == nil || update.Harness != "codex" || !update.Installed.Equal(updated) {
		t.Fatalf("CFO update = %+v, want the codex install at %s", update, updated)
	}
}

// A pi goblin's Update press restarts it in place with its own harness, model
// and effort, as Claude Code's does.
func TestAPiGoblinsUpdatePressRestartsItInPlace(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	s := handler.Service
	s.Options.FirstRun = newFirstRunMachine(t).run
	started := time.Now().Add(-2 * time.Hour)
	s.Options.ReadScreen = (&standInScreen{}).read
	s.Options.ProgramInstalled = func(harness.Kind) (string, time.Time, error) {
		return `C:\Tools\pi.cmd`, started.Add(time.Hour), nil
	}
	s.Options.Dispatch.Idle = func(context.Context, state.TaskMeta) (bool, error) { return true, nil }
	s.Options.Gate = &engineGate{}
	meta := state.TaskMeta{ID: "goblin", SpawnGen: "s1", Harness: "pi", Model: "pi-model", Effort: "high", Backend: "native", Worktree: h.Root, Project: h.Root}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, meta.Worktree)
	recordTerminal(t, h.State, meta.ID, started)
	s.lookForHarnessUpdates()
	if card := engineCard(t, s, meta.ID); card.HarnessUpdate == nil {
		t.Fatal("no update offered on a pi goblin installed since it started")
	}

	// Act
	response := taskControlRequest(handler, "/api/tasks/engine", map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "when": "update"})
	now := time.Now().UTC()
	if err := s.applyEngineChoices(t.Context(), now); err != nil {
		t.Fatal(err)
	}

	// Assert
	choice, err := state.ReadEngineChoice(h.State, meta.ID)
	if response.Code != http.StatusAccepted || err != nil || choice.When != "update" || choice.Harness != "pi" || choice.Model != "pi-model" || choice.Effort != "high" || len(spawner.recorded()) != 0 {
		t.Fatalf("press = %d %s, choice %+v %v, calls %+v; want its own values waiting for the end of its turn", response.Code, response.Body, choice, err, spawner.recorded())
	}

	// Act: its turn has ended.
	if err := s.applyEngineChoices(t.Context(), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	waitEngineChange(t, s, meta.ID)

	// Assert
	want := []string{"switch", meta.ID, "--generation", meta.SpawnGen, "--restart"}
	if calls := spawner.recorded(); len(calls) != 1 || !slices.Equal(calls[0], want) {
		t.Fatalf("calls %+v; want one %q", calls, want)
	}
}
