package supervisor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
	"github.com/fpresta0607/code-goblins/internal/state"
)

// standInScreen is what the stand-in terminals show, which a test changes
// between looks.
type standInScreen struct {
	mu   sync.Mutex
	rows []string
}

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
	data, err := json.Marshal(host.Record{ID: id, Pipe: `\\.\pipe\stand-in-` + id, Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: os.Getpid(), ChildStart: childStart, Started: time.Now().UTC()})
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
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(20 * time.Millisecond) {
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
