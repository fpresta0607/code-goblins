package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

var (
	idleCodexCFO = []string{"• Drained the queue; nothing waits.", "› Ask Codex to do anything", "  gpt-6-astra high · work"}
	cfoRule      = strings.Repeat("─", 60)
	idlePiCFO    = []string{"Drained the queue; nothing waits.", cfoRule, "  ", cfoRule, "↑7.8k ↓895 R31k CH94.9% $0.003 0.8%/1.0M (auto)"}
)

// typedWakeCFO registers this test process as a CFO running agent in native
// terminal cfo, whose screen reads as screens in turn, the last one from then
// on, and records every line typed into it.
func typedWakeCFO(t *testing.T, agent string, screens ...[]string) (*Service, string, *[]string, *error) {
	t.Helper()
	stateDir := t.TempDir()
	process, err := lock.Acquire(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(stateDir) })
	data, err := json.Marshal(primaryRegistration{Host: "cfo", Agent: agent, Process: *process})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	recordHost(t, stateDir, os.Getpid())
	var typed []string
	var deliverErr error
	read := 0
	connection := &CFOConnection{
		State: stateDir,
		ReadScreen: func(host.Record) ([]string, error) {
			screen := screens[min(read, len(screens)-1)]
			read++
			return screen, nil
		},
		Deliver: func(_ context.Context, terminal state.TaskMeta, text string) error {
			typed = append(typed, terminal.ID+"|"+terminal.Harness+"|"+text)
			return deliverErr
		},
	}
	return &Service{Options: Options{CFO: connection}}, stateDir, &typed, &deliverErr
}

func queueWake(t *testing.T, stateDir, kind, key, detail string) {
	t.Helper()
	if _, err := wake.Append(stateDir, kind, key, detail); err != nil {
		t.Fatal(err)
	}
}

// A Codex or pi CFO has no Stop hook to wake it, so the supervisor types one
// wake line into its terminal while it sits idle at an empty composer: once
// for the records it covers, and not again within the gap.
func TestAWakeIsTypedIntoAnIdleCodexOrPiCFOOnce(t *testing.T) {
	for agent, screen := range map[string][]string{"codex": idleCodexCFO, "pi": idlePiCFO} {
		t.Run(agent, func(t *testing.T) {
			s, stateDir, typed, _ := typedWakeCFO(t, agent, screen)
			now := time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)
			queueWake(t, stateDir, "notify", "cg-wakes", "done: PR https://example.test/pull/209")

			if err := s.wakeCFO(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			if len(*typed) != 1 || !strings.HasPrefix((*typed)[0], "cfo|"+agent+"|cfo watcher wake: 1 queued wake (notify cg-wakes); run cfo drain") || strings.ContainsAny((*typed)[0], "\r\n") {
				t.Fatalf("typed = %q, want one wake line naming the notify", *typed)
			}
			if err := s.wakeCFO(context.Background(), now.Add(10*time.Second)); err != nil || len(*typed) != 1 {
				t.Fatalf("a covered record was typed about again: %q, %v", *typed, err)
			}
			queueWake(t, stateDir, "stale", "g2", "goblin_idle: at its prompt for 3m")
			if err := s.wakeCFO(context.Background(), now.Add(20*time.Second)); err != nil || len(*typed) != 1 {
				t.Fatalf("a second line was typed within the gap: %q, %v", *typed, err)
			}
			if err := s.wakeCFO(context.Background(), now.Add(40*time.Second)); err != nil {
				t.Fatal(err)
			}
			if len(*typed) != 2 || !strings.Contains((*typed)[1], "1 queued wake (stale g2)") {
				t.Fatalf("typed = %q, want a second line naming only the new record", *typed)
			}
		})
	}
}

// Nothing is typed while the CFO is in a turn, at a dialog, or holding text
// in its composer that somebody left unsent, nor when the second reading
// finds it busy again.
func TestNoWakeIsTypedMidTurnOrOverUnsentText(t *testing.T) {
	for name, test := range map[string]struct {
		agent   string
		screens [][]string
	}{
		"codex in a turn":        {"codex", [][]string{{"• Working (5s • esc to interrupt)", "› Ask Codex to do anything"}}},
		"codex holding text":     {"codex", [][]string{{"• ok", "› run the full test suite", "  gpt-6-astra high · work"}}},
		"codex at a dialog":      {"codex", [][]string{{"  Hooks need review", "› 1. Review hooks", "  3. Continue without trusting (hooks won't run)", "› Ask Codex to do anything"}}},
		"pi holding text":        {"pi", [][]string{{"Done.", cfoRule, " merge it ", cfoRule, "0.8%/1.0M (auto)"}}},
		"pi in a turn":           {"pi", [][]string{{"── ⠸ Working ──", "  ", cfoRule, "0.8%/1.0M (auto)"}}},
		"busy on second reading": {"codex", [][]string{idleCodexCFO, {"• Working (0s • esc to interrupt)", "› Ask Codex to do anything"}}},
	} {
		t.Run(name, func(t *testing.T) {
			s, stateDir, typed, _ := typedWakeCFO(t, test.agent, test.screens...)
			queueWake(t, stateDir, "stale", "g1", "goblin_idle: at its prompt for 3m")

			if err := s.wakeCFO(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			if len(*typed) != 0 {
				t.Fatalf("typed %q into a CFO that was not idle at an empty composer", *typed)
			}
			if _, err := os.Stat(filepath.Join(stateDir, cfoWokenFile)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("records were marked covered though nothing was typed: %v", err)
			}
		})
	}
}

// A Claude Code CFO is woken by its own Stop hook, so nothing is typed into
// it, idle or not.
func TestAClaudeCodeCFOIsLeftToItsStopHook(t *testing.T) {
	s, stateDir, typed, _ := typedWakeCFO(t, "claude", []string{"❯ ", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"})
	queueWake(t, stateDir, "notify", "cg-wakes", "done: PR https://example.test/pull/209")

	if err := s.wakeCFO(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(*typed) != 0 {
		t.Fatalf("typed %q into a Claude Code CFO", *typed)
	}
}

// A line typed that nothing proves taken is reported and still marks what it
// covered, since typing it twice is the failure cfo send never risks.
func TestAnUnprovenWakeLineIsReportedAndNeverTypedTwice(t *testing.T) {
	s, stateDir, typed, deliverErr := typedWakeCFO(t, "codex", idleCodexCFO)
	*deliverErr = errors.New("its screen never showed it working")
	queueWake(t, stateDir, "notify", "cg-wakes", "blocked: merge or hold?")
	now := time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)

	err := s.wakeCFO(context.Background(), now)
	if err == nil || !strings.Contains(err.Error(), "nothing proves it taken") {
		t.Fatalf("err = %v, want the unproven line reported", err)
	}
	if err := s.wakeCFO(context.Background(), now.Add(time.Minute)); err != nil || len(*typed) != 1 {
		t.Fatalf("the unproven line was typed again: %q, %v", *typed, err)
	}
}

// A line nothing proves taken reaches the board with the next recovery cycle,
// though every look after it finds nothing left to type, and its records were
// marked covered before it was typed.
func TestAnUnprovenWakeLineReachesTheBoardOnce(t *testing.T) {
	s, stateDir, _, _ := typedWakeCFO(t, "codex", idleCodexCFO)
	queueWake(t, stateDir, "notify", "cg-wakes", "blocked: merge or hold?")
	delivered := make(chan int, 2)
	s.Options.CFO.Deliver = func(context.Context, state.TaskMeta, string) error {
		covered, _, err := readCFOWoken(stateDir)
		if err != nil {
			t.Error(err)
		}
		delivered <- covered
		return errors.New("its screen never showed it working")
	}
	store, _ := testStore(t)
	s.Store, s.work, s.subscribers = store, make(chan struct{}, 1), map[chan struct{}]struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	awake := make(chan struct{})
	go func() {
		defer close(awake)
		s.keepCFOAwake(ctx, 10*time.Millisecond)
	}()

	select {
	case covered := <-delivered:
		if covered != 1 {
			t.Errorf("the line was typed with records covered up to %d, want 1", covered)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no wake line was typed")
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-awake
	s.cycle(context.Background(), true)
	reported := s.lastError
	s.cycle(context.Background(), true)

	if len(delivered) != 0 {
		t.Errorf("the unproven line was typed again")
	}
	if !strings.Contains(reported, "nothing proves it taken") {
		t.Errorf("the board showed %q, want the unproven line reported", reported)
	}
	if strings.Contains(s.lastError, "nothing proves it taken") {
		t.Errorf("the next recovery cycle reported the line again: %q", s.lastError)
	}
}

// A failure every look meets reaches the board once with the next recovery
// cycle, not once for every look since the last one.
func TestAWakeFailureEveryLookMeetsReachesTheBoardOnce(t *testing.T) {
	s, stateDir, _, _ := typedWakeCFO(t, "codex", idleCodexCFO)
	if err := os.WriteFile(filepath.Join(stateDir, cfoWokenFile), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, _ := testStore(t)
	s.Store, s.work, s.subscribers = store, make(chan struct{}, 1), map[chan struct{}]struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	awake := make(chan struct{})
	go func() {
		defer close(awake)
		s.keepCFOAwake(ctx, 5*time.Millisecond)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-awake
	s.cycle(context.Background(), true)

	if got := strings.Count(s.lastError, "unreadable "+cfoWokenFile); got != 1 {
		t.Errorf("the board showed the failure %d times, want once: %q", got, s.lastError)
	}
}

// One place says how a CFO of each harness is woken, for the typed delivery
// and for whatever tells a user which harnesses a CFO can run in: Claude Code
// by its Stop hook, Codex and pi by a typed line, and a harness nothing
// delivers to by neither.
func TestEveryCFOHarnessHasOneWayItIsWoken(t *testing.T) {
	for agent, want := range map[string]CFOWake{
		"claude": CFOWakeStopHook,
		"codex":  CFOWakeTyped,
		"pi":     CFOWakeTyped,
		"kimi":   CFOWakeNone,
		"":       CFOWakeNone,
	} {
		if got := CFOWakeFor(agent); got != want {
			t.Errorf("CFOWakeFor(%q) = %q, want %q", agent, got, want)
		}
	}
}

// A Codex or pi CFO is woken by the typed line alone, so while AFK mode is on
// that line says so: every wake tells the CFO, whatever harness it runs.
func TestTheTypedWakeLineSaysAFKModeIsOn(t *testing.T) {
	// Arrange
	s, stateDir, typed, _ := typedWakeCFO(t, "codex", idleCodexCFO)
	if _, _, err := afk.TurnOn(stateDir, "the board", nil, time.Date(2026, 10, 2, 2, 10, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	queueWake(t, stateDir, "notify", "cg-wakes", "done: PR https://example.test/pull/209")

	// Act
	err := s.wakeCFO(context.Background(), time.Date(2026, 10, 2, 3, 0, 0, 0, time.UTC))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(*typed) != 1 || !strings.Contains((*typed)[0], "cfo watcher wake: 1 queued wake (notify cg-wakes)") || !strings.Contains((*typed)[0], "AFK mode is on since 2026-10-02 02:10 UTC") || strings.ContainsAny((*typed)[0], "\r\n") {
		t.Fatalf("typed = %q, want one line naming the wake and saying AFK mode is on", *typed)
	}
}
