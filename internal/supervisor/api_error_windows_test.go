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

// A goblin whose prompt takes no typing once its turn ended on an API error
// is restarted in place, on its own harness, model, effort and conversation,
// once its API answers again (#2863).
func TestAGoblinWhosePromptTakesNoTypingAfterAnAPIErrorIsRestartedInPlace(t *testing.T) {
	for name, refusal := range map[string]error{
		"words never sent":      fmt.Errorf("%w: nothing was typed", spawn.ErrUnsentWords),
		"a terminal not taking": fmt.Errorf("%w: its terminal is not ready", fleet.ErrNotTyped),
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			board := newAPIErrorBoard(t, "claude", "opus", claudeAtAPIError)
			board.typing = refusal

			// Act
			board.service.carryOnAfterAPIErrors(t.Context(), time.Now())
			waitEngineChange(t, board.service, board.meta.ID)

			// Assert
			want := [][]string{{"switch", "goblin", "--generation", "s1", "--restart"}}
			if calls := board.spawner.recorded(); len(calls) != 1 || !slices.Equal(calls[0], want[0]) {
				t.Errorf("spawned %q, want %q", calls, want)
			}
		})
	}
}

// Nothing carries on a goblin at an ordinary prompt, nor one whose harness
// gave up on a refusal routing names, as a rate limit, which the watcher
// routes (#2863).
func TestNoGoblinIsCarriedOnWhoseTurnDidNotEndOnAnAPIError(t *testing.T) {
	piRule := strings.Repeat("─", 120)
	for name, test := range map[string]struct {
		kind   string
		model  string
		screen []string
	}{
		"at an ordinary prompt": {"claude", "opus", []string{"● Pushed the branch.", "", updateRule, "❯ ", updateRule, "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}},
		"refused on quota":      {"pi", "openrouter/deepseek/deepseek-v4.1-flash", []string{" Reading the failing test first.", "", " Error: Retry failed after 3 attempts: 429 Too Many Requests: rate limit exceeded", piRule, "", piRule, "↑8.4k ↓357 R15k CH91.8% $0.001 0.8%/1.0M (auto)"}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			board := newAPIErrorBoard(t, test.kind, test.model, test.screen)

			// Act
			board.service.carryOnAfterAPIErrors(t.Context(), time.Now())

			// Assert
			if len(board.checked) != 0 || len(board.typed) != 0 || len(board.spawner.recorded()) != 0 {
				t.Errorf("checked %q, typed %q, spawned %q; want nothing", board.checked, board.typed, board.spawner.recorded())
			}
		})
	}
}
