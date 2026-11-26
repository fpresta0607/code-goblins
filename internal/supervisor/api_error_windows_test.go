package supervisor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// claudeAtAPIError is the screen a worker's Claude Code 2.1.331 terminal
// showed on 2026-11-24 once its turn ended on an API error, and
// claudeAtRequestTimeout the one another showed once its turn ended on a
// request that timed out.
var (
	claudeAtAPIError       = []string{"● Reading the tests to place a red regression test.", "  Read 5 files, ran 5 shell commands", "", "● API Error: Can't reach the API server — check your internet or DNS (ENOTFOUND)", "", strings.Repeat(" ", 80) + "× Auto-update failed · Run claude doctor", updateRule, "> Resend your message to try again", updateRule, "  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← 2 agents"}
	claudeAtRequestTimeout = []string{"> CEO: Your run ended with exit 0 after 2h42m23s; read its output and carry on from it.", "", "● Request timed out. If the API is slow to answer, or a proxy or gateway holds responses, set API_TIMEOUT_MS", "  above the limit in force (600000 by default).", "", updateRule, "> Resend your message to try again", updateRule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}
)

// apiErrorBoard is a board whose one goblin, stood in for by this test
// process, shows screen, whose API check answers with what answer holds, and
// whose deliveries type answers with typing's outcome, each recorded.
type apiErrorBoard struct {
	service *Service
	spawner *spawnRecorder
	meta    state.TaskMeta
	answer  error
	checked []string
	typing  error
	typed   []string
}

func newAPIErrorBoard(t *testing.T, kind, model string, screen []string) *apiErrorBoard {
	t.Helper()
	board := &apiErrorBoard{spawner: &spawnRecorder{}}
	handler, h := startBoard(t, 5*gigabyte, board.spawner)
	board.service = handler.Service
	standIn := &standInScreen{}
	standIn.show(screen)
	board.service.Options.ReadScreen = standIn.read
	board.service.Options.APIAnswers = func(_ context.Context, origin string) error {
		board.checked = append(board.checked, origin)
		return board.answer
	}
	delivery := typeIntoGoblin
	t.Cleanup(func() { typeIntoGoblin = delivery })
	typeIntoGoblin = func(_ context.Context, _ string, meta state.TaskMeta, text string, shouldRefuseTurn, shouldSendNow, his bool) error {
		board.typed = append(board.typed, fmt.Sprintf("%s holds %v, now %v, his %v: %s", meta.ID, shouldRefuseTurn, shouldSendNow, his, text))
		return board.typing
	}
	board.meta = state.TaskMeta{ID: "goblin", SpawnGen: "s1", Harness: kind, Model: model, Effort: "xhigh", Backend: "native", Worktree: h.Root, Project: h.Root}
	if err := state.WriteTaskMeta(h.State, board.meta); err != nil {
		t.Fatal(err)
	}
	recordTerminal(t, h.State, board.meta.ID, time.Now().Add(-time.Hour))
	return board
}

// A goblin whose turn ended on an API error, a connection that failed or a
// request that timed out, is carried on with one note once its API's check
// passes, and with none while the check fails; the note holds for a turn, and
// the goblin still showing the error is not carried on twice (#2863).
func TestAGoblinWhoseTurnEndedOnAnAPIErrorIsCarriedOnOnceTheAPIAnswersAgain(t *testing.T) {
	for name, screen := range map[string][]string{
		"unreachable": claudeAtAPIError,
		"timed out":   claudeAtRequestTimeout,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			board := newAPIErrorBoard(t, "claude", "opus", screen)
			board.answer = errors.New("dial tcp: lookup api.anthropic.com: no such host")
			now := time.Now()

			// Act: the API does not answer.
			board.service.carryOnAfterAPIErrors(t.Context(), now)
			board.service.carryOnAfterAPIErrors(t.Context(), now.Add(apiErrorCarryOnEvery))

			// Assert
			if len(board.typed) != 0 || !slices.Equal(board.checked, []string{"https://api.anthropic.com", "https://api.anthropic.com"}) {
				t.Fatalf("typed %q after checks %q; want nothing typed while each check fails", board.typed, board.checked)
			}

			// Act: it answers again.
			board.answer = nil
			board.service.carryOnAfterAPIErrors(t.Context(), now.Add(2*apiErrorCarryOnEvery))
			board.service.carryOnAfterAPIErrors(t.Context(), now.Add(3*apiErrorCarryOnEvery))

			// Assert
			want := []string{"goblin holds true, now false, his false: " + goblinLine(apiErrorNote)}
			if !slices.Equal(board.typed, want) || len(board.spawner.recorded()) != 0 {
				t.Errorf("typed %q, spawned %q; want %q and no restart", board.typed, board.spawner.recorded(), want)
			}
		})
	}
}

// The running supervisor carries such a goblin on by itself: its loop starts
// with the service and looks at once (#2863).
func TestTheRunningSupervisorCarriesOnAGoblinWhoseTurnEndedOnAnAPIError(t *testing.T) {
	// Arrange
	_, h := testStore(t)
	meta := state.TaskMeta{ID: "goblin", SpawnGen: "s1", Harness: "claude", Model: "opus", Backend: "native", Worktree: h.Root, Project: h.Root}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	recordTerminal(t, h.State, meta.ID, time.Now().Add(-time.Hour))
	standIn := &standInScreen{}
	standIn.show(claudeAtAPIError)
	typed := make(chan string, 16)
	delivery := typeIntoGoblin
	t.Cleanup(func() { typeIntoGoblin = delivery })
	typeIntoGoblin = func(_ context.Context, _ string, meta state.TaskMeta, text string, shouldRefuseTurn, _, _ bool) error {
		typed <- fmt.Sprintf("%s holds %v: %s", meta.ID, shouldRefuseTurn, text)
		return nil
	}
	options := Options{ReadScreen: standIn.read, APIAnswers: func(context.Context, string) error { return nil }}

	// Act
	s, err := Start(t.Context(), h, options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Assert
	want := "goblin holds true: " + goblinLine(apiErrorNote)
	deadline := time.After(time.Minute)
	for {
		select {
		case got := <-typed:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("the running supervisor typed no %q", want)
		}
	}
}

// A goblin whose terminal takes no typing once its turn ended on an API
// error, for a reason other than someone at work in it, as a terminal that
// cannot be read or a startup dialog, is restarted in place on its own
// harness, model, effort and conversation once its API answers again, and
// told the supervisor restarted it after its API error (#2863).
func TestAGoblinWhoseTerminalTakesNoTypingAfterAnAPIErrorIsRestartedInPlace(t *testing.T) {
	for name, refusal := range map[string]error{
		"an unreadable terminal": fmt.Errorf("%w: read native screen: the pipe has been ended", fleet.ErrNotTyped),
		"a startup dialog":       fmt.Errorf("%w: %w", fleet.ErrNotTyped, errors.New("a startup dialog interrupted instruction delivery")),
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			board := newAPIErrorBoard(t, "claude", "opus", claudeAtAPIError)
			board.typing = refusal

			// Act
			board.service.carryOnAfterAPIErrors(t.Context(), time.Now())
			waitEngineChange(t, board.service, board.meta.ID)

			// Assert
			want := [][]string{{"switch", "goblin", "--generation", "s1", "--restart", "--after-api-error"}}
			if calls := board.spawner.recorded(); len(calls) != 1 || !slices.Equal(calls[0], want[0]) {
				t.Errorf("spawned %q, want %q", calls, want)
			}
		})
	}
}

// A goblin whose prompt line holds words never sent, whose harness shows a
// screen of its own, as the picker /rewind opens, or whose turn started before
// the note was typed, has someone at work in its terminal: it is not
// restarted, which would lose what they did there, nor typed into again
// within apiErrorCarryOnGap (#2863). Each refusal is the one spawn's delivery
// gives.
func TestAGoblinAtWorkInItsTerminalIsNotRestartedAfterAnAPIError(t *testing.T) {
	for name, refusal := range map[string]error{
		"words never sent":    fmt.Errorf("spawn: native terminal goblin: %w: %w", fleet.ErrNotTyped, spawn.ErrUnsentWords),
		"a screen of its own": fmt.Errorf("%w: native terminal goblin is %w", fleet.ErrNotTyped, spawn.ErrOwnScreen),
		"a turn under way":    fmt.Errorf("spawn: native terminal goblin: %w: %w", fleet.ErrNotTyped, spawn.ErrInTurn),
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			board := newAPIErrorBoard(t, "claude", "opus", claudeAtAPIError)
			board.typing = refusal
			now := time.Now()

			// Act
			board.service.carryOnAfterAPIErrors(t.Context(), now)
			board.service.carryOnAfterAPIErrors(t.Context(), now.Add(apiErrorCarryOnEvery))
			waitEngineChange(t, board.service, board.meta.ID)

			// Assert
			if calls := board.spawner.recorded(); len(calls) != 0 || len(board.typed) != 1 {
				t.Errorf("typed %q, spawned %q; want one note tried and no restart", board.typed, calls)
			}
		})
	}
}

// A goblin whose terminal a view waits to press a typed message's Enter in has
// someone at work there too, whose keys it may not have read yet, so no note
// is typed behind them, and it is carried on once the Enter is answered and
// its screen still shows the error (#2848's review).
func TestAGoblinWhoseTypedMessagesEnterAViewAwaitsIsCarriedOnOnlyOnceItIsAnswered(t *testing.T) {
	// Arrange
	board := newAPIErrorBoard(t, "claude", "opus", claudeAtAPIError)
	board.service.Options.CEO = &CEOConnection{State: board.service.Store.Home.State}
	answered := board.service.Options.CEO.awaitEnter(board.meta.ID)
	now := time.Now()

	// Act
	board.service.carryOnAfterAPIErrors(t.Context(), now)

	// Assert
	if len(board.typed) != 0 || len(board.spawner.recorded()) != 0 {
		t.Fatalf("typed %q, spawned %q; want nothing while the Enter waits", board.typed, board.spawner.recorded())
	}

	// Act: the relay answers the Enter.
	answered()
	board.service.carryOnAfterAPIErrors(t.Context(), now.Add(apiErrorCarryOnEvery))

	// Assert
	want := []string{"goblin holds true, now false, his false: " + goblinLine(apiErrorNote)}
	if !slices.Equal(board.typed, want) || len(board.spawner.recorded()) != 0 {
		t.Errorf("typed %q, spawned %q; want %q and no restart", board.typed, board.spawner.recorded(), want)
	}
}

// Nothing carries on a goblin at an ordinary prompt, nor one whose harness
// gave up on a refusal routing names, as a rate limit, which the watcher
// routes, nor one on an API the supervisor does not know, as a pi model that
// names no provider (docs/project-runtime.md), nor one paused or pausing
// (#2863).
func TestNoGoblinOutsideTheSupervisorsCarryOnIsCarriedOnAfterAnAPIError(t *testing.T) {
	piRule := strings.Repeat("─", 120)
	piFooter := "↑8.4k ↓357 R15k CH91.8% $0.001 0.8%/1.0M (auto)"
	for name, test := range map[string]struct {
		kind   string
		model  string
		screen []string
		phase  string
	}{
		"at an ordinary prompt":      {"claude", "opus", []string{"● Pushed the branch.", "", updateRule, "❯ ", updateRule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}, ""},
		"refused on quota":           {"pi", "openrouter/deepseek/deepseek-v4.1-flash", []string{" Reading the failing test first.", "", " Error: Retry failed after 3 attempts: 429 Too Many Requests: rate limit exceeded", piRule, "", piRule, piFooter}, ""},
		"on an API it does not know": {"pi", "your-open-model", []string{" Reading the failing test first.", "", " Error: Retry failed after 3 attempts: fetch failed", piRule, "", piRule, piFooter}, ""},
		"pausing":                    {"claude", "opus", claudeAtAPIError, "pausing"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			board := newAPIErrorBoard(t, test.kind, test.model, test.screen)
			if test.phase != "" {
				if err := state.WriteLifecycle(board.service.Store.Home.State, state.Lifecycle{ID: board.meta.ID, Generation: board.meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: test.phase}); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			board.service.carryOnAfterAPIErrors(t.Context(), time.Now())

			// Assert
			if len(board.checked) != 0 || len(board.typed) != 0 || len(board.spawner.recorded()) != 0 {
				t.Errorf("checked %q, typed %q, spawned %q; want nothing", board.checked, board.typed, board.spawner.recorded())
			}
		})
	}
}
