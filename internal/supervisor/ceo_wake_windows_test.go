package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

var (
	idleCodexCEO = []string{"• Drained the queue; nothing waits.", "› Ask Codex to do anything", "  gpt-6-astra high · work"}
	ceoRule      = strings.Repeat("─", 60)
	idlePiCEO    = []string{"Drained the queue; nothing waits.", ceoRule, "  ", ceoRule, "↑7.8k ↓895 R31k CH94.9% $0.003 0.8%/1.0M (auto)"}
)

// typedWakeCEO registers this test process as a CEO running agent in native
// terminal ceo, whose screen reads as screens in turn, the last one from then
// on, and records every line typed into it.
func typedWakeCEO(t *testing.T, agent string, screens ...[]string) (*Service, string, *[]string, *error) {
	t.Helper()
	stateDir := t.TempDir()
	process, err := lock.Acquire(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(stateDir) })
	data, err := json.Marshal(primaryRegistration{Host: "ceo", Agent: agent, Process: *process})
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
	connection := &CEOConnection{
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
	store, err := Open(home.Home{Root: filepath.Dir(stateDir), State: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Store: store, Options: Options{CEO: connection}}, stateDir, &typed, &deliverErr
}

func queueWake(t *testing.T, stateDir, kind, key, detail string) {
	t.Helper()
	if _, err := wake.Append(stateDir, kind, key, detail); err != nil {
		t.Fatal(err)
	}
}

func TestCapturedIdleCodexCEORecoversPendingWakesAfterRestart(t *testing.T) {
	// Arrange
	screen := []string{
		"  Worked for 1m 48s • 7:58 AM", "", "", "› Ask Codex to do anything", "",
		"  GPT-6.1 Terra xhigh · C:\\dev\\code-goblins · Register CEO and handle …",
		"  ? for shortcuts                            ⚠ 8 warnings · f2 to view", "",
	}
	service, stateDir, typed, _ := typedWakeCEO(t, "codex", screen)
	now := time.Now().UTC()
	for index := range 7 {
		queueWake(t, stateDir, "notify", fmt.Sprintf("task-%d", index), "working")
	}
	pending, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, offset := range []time.Duration{0, ceoWakeStuckAfter + ceoWakeSteady} {
		if err := service.untoldWakes(pending, ceoWakeSkip{Why: "its screen does not show it waiting at an empty prompt"}, now.Add(offset)); err != nil {
			t.Fatal(err)
		}
	}
	if items := service.Store.Snapshot().Runs; len(items) != 1 || items[0].State != "ready" {
		t.Fatalf("restart warning = %+v; want one actionable warning before recovery", items)
	}

	// Act
	err = service.wakeCEO(context.Background(), now.Add(10*time.Minute))
	service.Options.CEO = &CEOConnection{State: stateDir, ReadScreen: service.Options.CEO.ReadScreen, Deliver: service.Options.CEO.Deliver}
	retryErr := service.wakeCEO(context.Background(), now.Add(11*time.Minute))

	// Assert
	if err != nil || retryErr != nil || len(*typed) != 1 {
		t.Fatalf("wake/restart = %v, %v, typed %q; want all seven pending wakes covered once", err, retryErr, *typed)
	}
	pending, err = wake.Pending(stateDir)
	if err != nil || !strings.Contains((*typed)[0], "7 queued wakes") || len(pending) != 7 {
		t.Fatalf("wake = %q; want seven covered records kept pending until the CEO acknowledges them", *typed)
	}
	if items := service.Store.Snapshot().Runs; len(items) != 1 || items[0].State != "withdrawn" {
		t.Fatalf("restart warning = %+v; want its history retained and action withdrawn after delivery", items)
	}
}

func TestNativeCodexWakeRecoversTheStartupQueueOnceAcrossCEOAndSupervisorRestart(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
	for index := range 7 {
		queueWake(t, home.State, "notify", fmt.Sprintf("task-%d", index), "working")
	}
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := service.wakeCEO(ctx, now); err != nil {
		t.Fatal(err)
	}
	if covered, _, err := readCEOWoken(home.State); err != nil || covered != 0 {
		t.Fatalf("before startup covered = %d, %v; want nothing covered", covered, err)
	}
	terminal := nativeCEOComposer(t, home.State, "display-model")

	// Act
	err := service.wakeCEO(ctx, now.Add(time.Minute))
	record, readErr := host.ReadRecord(home.State, "ceo")
	if readErr != nil {
		t.Fatal(readErr)
	}
	client, dialErr := host.Dial(record)
	if dialErr != nil {
		t.Fatal(dialErr)
	}
	if closeErr := errors.Join(client.CloseTerminal(), client.Close()); closeErr != nil {
		t.Fatal(closeErr)
	}
	select {
	case <-terminal.ended:
	case <-ctx.Done():
		t.Fatal("the original CEO terminal did not end")
	}
	replacement := nativeCEOComposer(t, home.State, "display-model")
	reopened, openErr := Open(home)
	if openErr != nil {
		t.Fatal(openErr)
	}
	restarted := &Service{Store: reopened, Options: Options{CEO: &CEOConnection{State: home.State}}}
	retryErr := restarted.wakeCEO(ctx, now.Add(2*time.Minute))

	// Assert
	input := terminal.lines(t)
	if err != nil || retryErr != nil || len(input) != 2 || !strings.HasPrefix(input[1], "accepted ceo watcher wake: 7 queued wakes") {
		t.Fatalf("wake/restart = %v, %v, input %q; want one accepted wake covering the startup queue", err, retryErr, input)
	}
	if replacementInput := replacement.lines(t); len(replacementInput) != 1 || !strings.HasPrefix(replacementInput[0], "registered ") {
		t.Fatalf("replacement CEO input = %q; want registration alone without replaying the wake", replacementInput)
	}
	pending, err := wake.Pending(home.State)
	if err != nil || len(pending) != 7 {
		t.Fatalf("pending = %d, %v; want all seven records retained until acknowledged", len(pending), err)
	}
	covered, _, err := readCEOWoken(home.State)
	if err != nil || covered != pending[len(pending)-1].Seq {
		t.Fatalf("covered = %d, %v; want exactly the accepted batch", covered, err)
	}
}

// A Codex or pi CEO has no Stop hook to wake it, so the supervisor types one
// wake line into its terminal while it sits idle at an empty composer: once
// for the records it covers, and not again within the gap.
func TestAWakeIsTypedIntoAnIdleCodexOrPiCEOOnce(t *testing.T) {
	for _, kind := range []string{"notify", "pr"} {
		t.Run(kind, func(t *testing.T) {
			for agent, screen := range map[string][]string{"codex": idleCodexCEO, "pi": idlePiCEO} {
				t.Run(agent, func(t *testing.T) {
					s, stateDir, typed, _ := typedWakeCEO(t, agent, screen)
					now := time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)
					queueWake(t, stateDir, kind, "cg-wakes", "done: PR https://example.test/pull/209")

					if err := s.wakeCEO(context.Background(), now); err != nil {
						t.Fatal(err)
					}
					if len(*typed) != 1 || !strings.HasPrefix((*typed)[0], "ceo|"+agent+"|ceo watcher wake: 1 queued wake ("+kind+" cg-wakes); run ceo drain") || strings.ContainsAny((*typed)[0], "\r\n") {
						t.Fatalf("typed = %q, want one wake line naming %s", *typed, kind)
					}
					if err := s.wakeCEO(context.Background(), now.Add(10*time.Second)); err != nil || len(*typed) != 1 {
						t.Fatalf("a covered record was typed about again: %q, %v", *typed, err)
					}
					queueWake(t, stateDir, "stale", "g2", "goblin_idle: at its prompt for 3m")
					if err := s.wakeCEO(context.Background(), now.Add(20*time.Second)); err != nil || len(*typed) != 1 {
						t.Fatalf("a second line was typed within the gap: %q, %v", *typed, err)
					}
					if err := s.wakeCEO(context.Background(), now.Add(40*time.Second)); err != nil {
						t.Fatal(err)
					}
					if len(*typed) != 2 || !strings.Contains((*typed)[1], "1 queued wake (stale g2)") {
						t.Fatalf("typed = %q, want a second line naming only the new record", *typed)
					}
				})
			}
		})
	}
}

// Nothing is typed while the CEO is in a turn, at a dialog, or holding text
// in its composer that somebody left unsent, nor when the second reading
// finds it busy again.
func TestNoWakeIsTypedMidTurnOrOverUnsentText(t *testing.T) {
	for _, kind := range []string{"stale", "pr"} {
		t.Run(kind, func(t *testing.T) {
			for name, test := range map[string]struct {
				agent   string
				screens [][]string
			}{
				"codex in a turn":                     {"codex", [][]string{{"• Working (5s • esc to interrupt)", "› Ask Codex to do anything"}}},
				"codex holding text":                  {"codex", [][]string{{"• ok", "› run the full test suite", "  gpt-6-astra high · work"}}},
				"codex display model draft":           {"codex", [][]string{{"› unfinished draft", "", "  GPT-6.1 Terra xhigh · C:\\work", "  ? for shortcuts"}}},
				"codex display model multiline draft": {"codex", [][]string{{"› Ask Codex to do anything", "", "continued draft", "", "  GPT-6.1 Terra xhigh · C:\\work", "  ? for shortcuts"}}},
				"codex display model in a turn":       {"codex", [][]string{{"• Working (5s • esc to interrupt)", "› Ask Codex to do anything", "", "  GPT-6.1 Terra xhigh · C:\\work", "  ? for shortcuts"}}},
				"codex at a dialog":                   {"codex", [][]string{{"  Hooks need review", "› 1. Review hooks", "  3. Continue without trusting (hooks won't run)", "› Ask Codex to do anything"}}},
				"pi holding text":                     {"pi", [][]string{{"Done.", ceoRule, " merge it ", ceoRule, "0.8%/1.0M (auto)"}}},
				"pi in a turn":                        {"pi", [][]string{{"── ⠸ Working ──", "  ", ceoRule, "0.8%/1.0M (auto)"}}},
				"busy on second reading":              {"codex", [][]string{idleCodexCEO, {"• Working (0s • esc to interrupt)", "› Ask Codex to do anything"}}},
			} {
				t.Run(name, func(t *testing.T) {
					s, stateDir, typed, _ := typedWakeCEO(t, test.agent, test.screens...)
					queueWake(t, stateDir, kind, "g1", "goblin_idle: at its prompt for 3m")

					if err := s.wakeCEO(context.Background(), time.Now()); err != nil {
						t.Fatal(err)
					}
					if len(*typed) != 0 {
						t.Fatalf("typed %q into a CEO that was not idle at an empty composer", *typed)
					}
					if _, err := os.Stat(filepath.Join(stateDir, ceoWokenFile)); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("records were marked covered though nothing was typed: %v", err)
					}
				})
			}
		})
	}
}

// A CEO that starts a turn after the wake's two idle readings, and shows it
// on the reading its delivery takes just before typing, is typed nothing and
// keeps its records for the next look: the line was once submitted into the
// turn, and every such line reached the board as one nothing proves taken
// (#1468).
func TestNoWakeIsTypedWhenTheDeliveryFindsTheCEOInATurn(t *testing.T) {
	inTurn := []string{"• Working (0s • esc to interrupt)", "› Ask Codex to do anything", "  gpt-6-astra high · work"}
	s, stateDir, _, _ := typedWakeCEO(t, "codex", idleCodexCEO, idleCodexCEO, inTurn)
	s.Options.CEO.Deliver = nil
	queueWake(t, stateDir, "notify", "cg-wakes", "blocked: merge or hold?")

	err := s.wakeCEO(context.Background(), time.Now())

	if err != nil {
		t.Fatalf("wake = %v, want nothing typed and nothing reported", err)
	}
	if covered, _, err := readCEOWoken(stateDir); err != nil || covered != 0 {
		t.Fatalf("records covered up to %d, %v; want none covered, since nothing was typed", covered, err)
	}
	skips, err := fsx.ReadLines(filepath.Join(stateDir, ceoWakeSkipsFile))
	if err != nil || len(skips) != 1 || !strings.Contains(skips[0], whyInTurn) {
		t.Fatalf("skips = %q, %v; want the wake recorded as left for the turn's end", skips, err)
	}
}

func TestNoPRWakeIsTypedIntoAReplacedNativeRecipient(t *testing.T) {
	for agent, screen := range map[string][]string{"codex": idleCodexCEO, "pi": idlePiCEO} {
		t.Run(agent, func(t *testing.T) {
			s, stateDir, typed, _ := typedWakeCEO(t, agent, screen)
			queueWake(t, stateDir, "pr", "cg-wakes", "PR 209 needs review")
			recordHost(t, stateDir, 0)
			s.Options.CEO.ReadScreen = func(host.Record) ([]string, error) {
				t.Error("a replaced recipient's screen was read")
				return screen, nil
			}

			err := s.wakeCEO(context.Background(), time.Now())

			if err == nil || !strings.Contains(err.Error(), "ended or runs another program") {
				t.Fatalf("wake = %v, want the changed terminal program reported", err)
			}
			if len(*typed) != 0 {
				t.Fatalf("typed %q into a replaced recipient", *typed)
			}
			if _, err := os.Stat(filepath.Join(stateDir, ceoWokenFile)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the refused PR record was marked covered: %v", err)
			}
		})
	}
}

// A Claude Code CEO is woken by its own Stop hook, so nothing is typed into
// it, idle or not.
func TestAClaudeCodeCEOIsLeftToItsStopHook(t *testing.T) {
	s, stateDir, typed, _ := typedWakeCEO(t, "claude", []string{"❯ ", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"})
	queueWake(t, stateDir, "notify", "cg-wakes", "done: PR https://example.test/pull/209")

	if err := s.wakeCEO(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(*typed) != 0 {
		t.Fatalf("typed %q into a Claude Code CEO", *typed)
	}
}

// A line typed that nothing proves taken is reported and still marks what it
// covered, since typing it twice is the failure ceo send never risks.
func TestAnUnprovenWakeLineIsReportedAndNeverTypedTwice(t *testing.T) {
	s, stateDir, typed, deliverErr := typedWakeCEO(t, "codex", idleCodexCEO)
	*deliverErr = errors.New("its screen never showed it working")
	queueWake(t, stateDir, "notify", "cg-wakes", "blocked: merge or hold?")
	now := time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)

	err := s.wakeCEO(context.Background(), now)
	if err == nil || !strings.Contains(err.Error(), "nothing proves it taken") {
		t.Fatalf("err = %v, want the unproven line reported", err)
	}
	if err := s.wakeCEO(context.Background(), now.Add(time.Minute)); err != nil || len(*typed) != 1 {
		t.Fatalf("the unproven line was typed again: %q, %v", *typed, err)
	}
}

// A line nothing proves taken reaches the board with the next recovery cycle,
// though every look after it finds nothing left to type, and its records were
// marked covered before it was typed.
func TestAnUnprovenWakeLineReachesTheBoardOnce(t *testing.T) {
	s, stateDir, _, _ := typedWakeCEO(t, "codex", idleCodexCEO)
	queueWake(t, stateDir, "notify", "cg-wakes", "blocked: merge or hold?")
	delivered := make(chan int, 2)
	s.Options.CEO.Deliver = func(context.Context, state.TaskMeta, string) error {
		covered, _, err := readCEOWoken(stateDir)
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
		s.keepCEOAwake(ctx, 10*time.Millisecond)
	}()

	select {
	case covered := <-delivered:
		if covered != 1 {
			t.Errorf("the line was typed with records covered up to %d, want 1", covered)
		}
	case <-time.After(time.Minute):
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
	s, stateDir, _, _ := typedWakeCEO(t, "codex", idleCodexCEO)
	if err := os.WriteFile(filepath.Join(stateDir, ceoWokenFile), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, _ := testStore(t)
	s.Store, s.work, s.subscribers = store, make(chan struct{}, 1), map[chan struct{}]struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	awake := make(chan struct{})
	go func() {
		defer close(awake)
		s.keepCEOAwake(ctx, 5*time.Millisecond)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-awake
	s.cycle(context.Background(), true)

	if got := strings.Count(s.lastError, "unreadable "+ceoWokenFile); got != 1 {
		t.Errorf("the board showed the failure %d times, want once: %q", got, s.lastError)
	}
}

// One place says how a CEO of each harness is woken, for the typed delivery
// and for whatever tells a user which harnesses a CEO can run in: Claude Code
// by its Stop hook, Codex and pi by a typed line, and a harness nothing
// delivers to by neither.
func TestEveryCEOHarnessHasOneWayItIsWoken(t *testing.T) {
	for agent, want := range map[string]CEOWake{
		"claude": CEOWakeStopHook,
		"codex":  CEOWakeTyped,
		"pi":     CEOWakeTyped,
		"grok":   CEOWakeNone,
		"":       CEOWakeNone,
	} {
		if got := CEOWakeFor(agent); got != want {
			t.Errorf("CEOWakeFor(%q) = %q, want %q", agent, got, want)
		}
	}
}

// A Codex or pi CEO is woken by the typed line alone, so while AFK mode is on
// that line says so: every wake tells the CEO, whatever harness it runs.
func TestTheTypedWakeLineSaysAFKModeIsOn(t *testing.T) {
	// Arrange
	s, stateDir, typed, _ := typedWakeCEO(t, "codex", idleCodexCEO)
	if _, _, err := afk.TurnOn(stateDir, "the board", nil, time.Date(2026, 10, 2, 2, 10, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	queueWake(t, stateDir, "notify", "cg-wakes", "done: PR https://example.test/pull/209")

	// Act
	err := s.wakeCEO(context.Background(), time.Date(2026, 10, 2, 3, 0, 0, 0, time.UTC))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(*typed) != 1 || !strings.Contains((*typed)[0], "ceo watcher wake: 1 queued wake (notify cg-wakes)") || !strings.Contains((*typed)[0], "AFK mode is on since 2026-10-02 02:10 UTC") || strings.ContainsAny((*typed)[0], "\r\n") {
		t.Fatalf("typed = %q, want one line naming the wake and saying AFK mode is on", *typed)
	}
}

// idleWithWorkRunning is a Codex CEO idle at its prompt while work it started
// still runs, the shape suspected of 2026-10-13 01:17Z to 01:33Z.
var idleWithWorkRunning = []string{"• Ran ceo drain", "  Running… (12m)", "› Ask Codex to do anything", "  gpt-6-astra high · work"}

// skips reads the record of the wakes the supervisor did not type.
func skips(t *testing.T, stateDir string) []ceoWakeSkip {
	t.Helper()
	lines, err := fsx.ReadLines(filepath.Join(stateDir, ceoWakeSkipsFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	var kept []ceoWakeSkip
	for _, line := range lines {
		var skip ceoWakeSkip
		if err := json.Unmarshal([]byte(line), &skip); err != nil {
			t.Fatalf("unreadable skip %q: %v", line, err)
		}
		kept = append(kept, skip)
	}
	return kept
}

// On 2026-10-13 from 01:17Z to 01:33Z wakes waited untyped for an idle Codex
// CEO and nothing said why. A wake the supervisor does not type is recorded
// with why, in plain words, and the last rows of the CEO's screen, once for
// each new wake and each new reason, not at every look.
func TestAnUntypedWakeIsRecordedWithWhyAndTheCEOsScreen(t *testing.T) {
	for name, test := range map[string]struct {
		screen []string
		why    string
	}{
		"in a turn":                       {[]string{"• Working (5s • esc to interrupt)", "› Ask Codex to do anything"}, "its screen shows it in the middle of a turn"},
		"a turn's own heading":            {[]string{"• Investigating the CEO's wake path (2m 05s • esc to interrupt)", "› Ask Codex to do anything"}, "its screen shows it in the middle of a turn"},
		"a dialog's words read in a turn": {[]string{"  Hooks need review", "• Working (12s • esc to interrupt)", "› Ask Codex to do anything"}, "its screen shows it in the middle of a turn"},
		"its agents run":                  {[]string{"• Waiting for 3 background agents (12m • esc to interrupt)", "› Ask Codex to do anything", "  gpt-6-astra high · work"}, "its screen shows it in the middle of a turn"},
		"at a dialog":                     {[]string{"Trust this folder?", "› 1. Trust and continue", "  2. Quit", "", "enter continue · esc quit"}, "its screen shows the folder trust prompt"},
		"holding text":                    {[]string{"• ok", "› run the full test suite", "  gpt-6-astra high · work"}, "its screen does not show it waiting at an empty prompt"},
		"no prompt":                       {[]string{"Starting Codex..."}, "its screen does not show it waiting at an empty prompt"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, stateDir, typed, _ := typedWakeCEO(t, "codex", test.screen)
			queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")
			now := time.Now().UTC()

			// Act
			for look := range 3 {
				if err := s.wakeCEO(context.Background(), now.Add(time.Duration(look)*ceoWakeEvery)); err != nil {
					t.Fatal(err)
				}
			}

			// Assert
			kept := skips(t, stateDir)
			if len(*typed) != 0 || len(kept) != 1 {
				t.Fatalf("typed %q and recorded %+v; want nothing typed and one skip", *typed, kept)
			}
			if kept[0].Why != test.why || kept[0].Wakes != 1 || !strings.Contains(kept[0].Screen, strings.TrimSpace(test.screen[0])) {
				t.Errorf("skip = %+v; want why %q, 1 wake and the screen's rows", kept[0], test.why)
			}
		})
	}
}

// Codex draws its optional Daybreak offer above its composer, and the rows of
// an offer an earlier redraw dismissed stay on screen as history no key
// reaches: a wake is typed into the CEO idle at the empty composer under them,
// whose delivery dismisses a live offer before it types, and no reason is
// recorded for a skip that never happened (#1556).
func TestAWakeIsTypedIntoACEOIdleUnderAnInlineOffersRows(t *testing.T) {
	// Arrange
	screen := append([]string{"  Set up security for Daybreak mode", "› 1. Set up security", "  Press a number to choose · esc to dismiss · type to continue", ""}, idleCodexCEO...)
	s, stateDir, typed, _ := typedWakeCEO(t, "codex", screen)
	queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")

	// Act
	err := s.wakeCEO(context.Background(), time.Now().UTC())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if kept := skips(t, stateDir); len(*typed) != 1 || len(kept) != 0 {
		t.Fatalf("typed %q and recorded %+v; want the wake typed once and no skip", *typed, kept)
	}
}

// A wake left untyped past ceoWakeStuckAfter means the CEO cannot be told of
// anything, so the Overlord is: one Command Center item in plain words that
// restarts the CEO, made once, and taken back once the CEO is told.
func TestTheOverlordIsToldOnceWhenTheCEOCannotBeWoken(t *testing.T) {
	// Arrange
	busy := idleWithWorkRunning
	s, stateDir, typed, _ := typedWakeCEO(t, "codex", busy)
	queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")
	queueWake(t, stateDir, "notify", "g2", "blocked: which one?")
	now := time.Now().UTC()
	untold := func() []Run {
		var kept []Run
		for _, r := range s.Store.Snapshot().Runs {
			if r.CEOUnwoken != 0 {
				kept = append(kept, r)
			}
		}
		return kept
	}

	// Act and assert: nothing before the wait is long enough.
	if err := s.wakeCEO(context.Background(), now.Add(ceoWakeStuckAfter-ceoWakeSteady)); err != nil {
		t.Fatal(err)
	}
	if items := untold(); len(items) != 0 {
		t.Fatalf("items = %+v before the wakes waited %s", items, ceoWakeStuckAfter)
	}

	// Act and assert: one item once it is.
	for look := range 2 {
		if err := s.wakeCEO(context.Background(), now.Add(ceoWakeStuckAfter+time.Duration(look)*time.Minute+time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	items := untold()
	if len(items) != 1 || items[0].State != "ready" {
		t.Fatalf("items = %+v; want one item waiting on the Overlord", items)
	}
	want := "The CEO has not been told of 2 fleet updates for 5 minutes, because its screen shows work it started still running. Restart the CEO so it reads them?"
	if items[0].Title != want || !strings.HasPrefix(items[0].Command, "# ") {
		t.Errorf("item = %q with command %q; want %q, with no command to run", items[0].Title, items[0].Command, want)
	}

	// Act and assert: the CEO takes the wakes, and the item goes.
	s.Options.CEO.ReadScreen = func(host.Record) ([]string, error) { return idleCodexCEO, nil }
	if err := s.wakeCEO(context.Background(), now.Add(ceoWakeStuckAfter+3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if items := untold(); len(*typed) != 1 || len(items) != 1 || items[0].State != "withdrawn" {
		t.Errorf("typed %q, items %+v; want the wakes typed and the item withdrawn", *typed, items)
	}
}

// The CEO may read its wakes without being told, as a Codex CEO in a long
// turn runs ceo drain itself: once none waits, the item goes as well.
func TestTheOverlordsItemGoesOnceTheCEODrainsItsWakes(t *testing.T) {
	// Arrange
	busy := idleWithWorkRunning
	s, stateDir, _, _ := typedWakeCEO(t, "codex", busy)
	queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")
	now := time.Now().UTC()
	raiseItem(t, s, now)
	pending, err := wake.Pending(stateDir)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}

	// Act
	if err := wake.AckThrough(stateDir, pending[0].Seq); err != nil {
		t.Fatal(err)
	}
	if err := s.wakeCEO(context.Background(), now.Add(ceoWakeStuckAfter+time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Assert
	var states []string
	for _, r := range s.Store.Snapshot().Runs {
		if r.CEOUnwoken != 0 {
			states = append(states, r.State)
		}
	}
	if len(states) != 1 || states[0] != "withdrawn" {
		t.Errorf("items = %q; want the one item withdrawn", states)
	}
}

// A row of work still running redraws its clock between looks. It is kept
// in the screen beside the reason and never in the reason, so the record is
// written once and the Overlord's item carries no raw screen text.
func TestWorkStillRunningIsRecordedOnceWhateverItsClockSays(t *testing.T) {
	// Arrange
	ticking := func(seconds int) []string {
		return []string{"• Ran go test ./...", fmt.Sprintf("  Running… (%ds)", seconds), "› Ask Codex to do anything", "  gpt-6-astra high · work"}
	}
	s, stateDir, typed, _ := typedWakeCEO(t, "codex", ticking(1), ticking(2), ticking(3), ticking(4), ticking(5), ticking(6))
	queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")
	now := time.Now().UTC()

	// Act
	for look := range 3 {
		if err := s.wakeCEO(context.Background(), now.Add(time.Duration(look)*ceoWakeEvery)); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	kept := skips(t, stateDir)
	if len(*typed) != 0 || len(kept) != 1 || kept[0].Why != "its screen shows work it started still running" || !strings.Contains(kept[0].Screen, "Running…") {
		t.Errorf("typed %q, recorded %+v; want one record with a fixed reason and the row in its screen", *typed, kept)
	}
}

// A Claude Code CEO takes its wakes through its Stop hook, and a typed line
// that nothing proves taken is reported as it was: neither is a wake the
// supervisor skipped, so neither is recorded or put before the Overlord.
func TestNoSkipIsRecordedForAStopHookCEOOrAnUnprovenLine(t *testing.T) {
	for name, test := range map[string]struct {
		agent      string
		screen     []string
		deliverErr error
	}{
		"a Claude Code CEO": {"claude", []string{"❯ ", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"}, nil},
		"an unproven line":  {"codex", idleCodexCEO, errors.New("nothing on its screen shows it working")},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, stateDir, _, deliverErr := typedWakeCEO(t, test.agent, test.screen)
			*deliverErr = test.deliverErr
			queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")

			// Act
			err := s.wakeCEO(context.Background(), time.Now().UTC().Add(ceoWakeStuckAfter+time.Minute))
			if (err != nil) != (test.deliverErr != nil) {
				t.Errorf("wake = %v; want the unproven line reported, and nothing else", err)
			}

			// Assert
			if kept := skips(t, stateDir); len(kept) != 0 {
				t.Errorf("recorded %+v; want nothing", kept)
			}
			for _, r := range s.Store.Snapshot().Runs {
				if r.CEOUnwoken != 0 {
					t.Errorf("offered %q; want no item", r.Title)
				}
			}
		})
	}
}

// A CEO switched to Claude Code takes its wakes through its Stop hook, so an
// item offering the Codex CEO's restart is taken back.
func TestTheOverlordsItemGoesOnceTheCEOTakesItsWakesItself(t *testing.T) {
	// Arrange
	busy := idleWithWorkRunning
	s, stateDir, _, _ := typedWakeCEO(t, "codex", busy)
	queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")
	now := time.Now().UTC()
	raiseItem(t, s, now)
	primary, _, err := readPrimary(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	primary.Agent = "claude"
	data, err := json.Marshal(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	if err := s.wakeCEO(context.Background(), now.Add(ceoWakeStuckAfter+time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Assert
	var states []string
	for _, r := range s.Store.Snapshot().Runs {
		if r.CEOUnwoken != 0 {
			states = append(states, r.State)
		}
	}
	if len(states) != 1 || states[0] != "withdrawn" {
		t.Errorf("items = %q; want the one item withdrawn", states)
	}
}

// A CEO in the middle of a turn takes its wakes as the turn ends, and a
// restart would end it at work, so its untyped wakes are recorded and never
// put before the Overlord, however long they wait.
func TestACEOInATurnIsNeverOfferedARestart(t *testing.T) {
	// Arrange
	s, stateDir, _, _ := typedWakeCEO(t, "codex", []string{"• Working (3h 01m • esc to interrupt)", "› Ask Codex to do anything"})
	queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")
	now := time.Now().UTC()

	// Act
	for _, after := range []time.Duration{ceoWakeStuckAfter + time.Second, time.Hour, 3 * time.Hour} {
		if err := s.wakeCEO(context.Background(), now.Add(after)); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	for _, r := range s.Store.Snapshot().Runs {
		if r.CEOUnwoken != 0 {
			t.Errorf("offered %q to a CEO in a turn", r.Title)
		}
	}
	if kept := skips(t, stateDir); len(kept) != 1 || kept[0].Why != whyInTurn {
		t.Errorf("recorded %+v; want the one untyped wake with its reason", kept)
	}
}

// The item restarts the CEO only through the board's Restart the CEO, which
// the supervisor does itself: pressed as a command it runs nothing, since a
// CEO a run terminal started is not proven to outlive that terminal.
func TestTheRestartItemIsNeverRunAsACommand(t *testing.T) {
	// Arrange
	s, stateDir, _, _ := typedWakeCEO(t, "codex", idleWithWorkRunning)
	queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")
	raiseItem(t, s, time.Now().UTC())
	var item Run
	for _, r := range s.Store.Snapshot().Runs {
		if r.CEOUnwoken != 0 {
			item = r
		}
	}
	if item.ID == "" {
		t.Fatal("no item was offered")
	}

	// Act
	_, err := s.Store.Queue(Action{ID: "action", Kind: "run", RunID: item.ID, Generation: item.Identity})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "Restart the CEO") {
		t.Errorf("queue = %v; want it refused", err)
	}
	for _, r := range s.Store.Snapshot().Runs {
		if r.ID == item.ID && r.State != "ready" {
			t.Errorf("the item is %s; want it still ready", r.State)
		}
	}
}

// countingRuns counts the run terminals a test's supervisor launched.
type countingRuns struct{ launched *int }

func (c *countingRuns) Launch(context.Context, RunLaunch) (RunStarted, error) {
	*c.launched++
	return RunStarted{}, nil
}

// raiseItem looks at the CEO a minute before its wakes have waited
// ceoWakeStuckAfter and again once they have, which raises the Overlord's
// item for a CEO whose screen keeps one reason.
func raiseItem(t *testing.T, s *Service, now time.Time) {
	t.Helper()
	for _, at := range []time.Duration{ceoWakeStuckAfter - ceoWakeSteady, ceoWakeStuckAfter + time.Second} {
		if err := s.wakeCEO(context.Background(), now.Add(at)); err != nil {
			t.Fatal(err)
		}
	}
}

// ceoUnwokenItems are the states of the items that offer the CEO's restart.
func ceoUnwokenItems(s *Service) []string {
	var states []string
	for _, r := range s.Store.Snapshot().Runs {
		if r.CEOUnwoken != 0 {
			states = append(states, r.State)
		}
	}
	return states
}

// One frame is not a CEO stuck: a CEO between two steps of a turn can show
// one that reads otherwise, so a reason must hold a minute of looks first.
func TestOneFrameNeverRaisesTheRestartItem(t *testing.T) {
	// Arrange
	s, stateDir, _, _ := typedWakeCEO(t, "codex", idleWithWorkRunning)
	queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")
	now := time.Now().UTC()

	// Act
	if err := s.wakeCEO(context.Background(), now.Add(ceoWakeStuckAfter+time.Second)); err != nil {
		t.Fatal(err)
	}

	// Assert
	if states := ceoUnwokenItems(s); len(states) != 0 {
		t.Errorf("items = %q after one look; want none", states)
	}
}

// An item raised for a CEO that then goes back to work is taken back: it
// takes its wakes as that turn ends.
func TestTheRestartItemGoesOnceTheCEOIsAtWorkAgain(t *testing.T) {
	// Arrange
	s, stateDir, _, _ := typedWakeCEO(t, "codex", idleWithWorkRunning)
	queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")
	now := time.Now().UTC()
	raiseItem(t, s, now)
	s.Options.CEO.ReadScreen = func(host.Record) ([]string, error) {
		return []string{"• Working (3s • esc to interrupt)", "› Ask Codex to do anything"}, nil
	}

	// Act
	if err := s.wakeCEO(context.Background(), now.Add(ceoWakeStuckAfter+time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Assert
	if states := ceoUnwokenItems(s); len(states) != 1 || states[0] != "withdrawn" {
		t.Errorf("items = %q; want the one item withdrawn", states)
	}
}

// A CEO that no longer runs has no Restart the CEO to offer, so its untyped
// wakes are recorded and the Overlord is given no item he cannot act on.
func TestACEOThatNoLongerRunsIsOfferedNothing(t *testing.T) {
	// Arrange
	s, stateDir, _, _ := typedWakeCEO(t, "codex", idleWithWorkRunning)
	queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")
	primary, _, err := readPrimary(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	primary.Process.Start = primary.Process.Start.Add(-time.Hour)
	data, err := json.Marshal(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	raiseItem(t, s, time.Now().UTC())

	// Assert
	if states := ceoUnwokenItems(s); len(states) != 0 {
		t.Errorf("items = %q; want none", states)
	}
	if kept := skips(t, stateDir); len(kept) != 1 || kept[0].Why != whyEnded {
		t.Errorf("recorded %+v; want the wake with why", kept)
	}
}

// A reason that held through an earlier stretch of untyped wakes does not
// carry over: once the CEO was told, the next stretch needs its own minute.
func TestAnEarlierStretchDoesNotShortenTheSteadyMinute(t *testing.T) {
	// Arrange: a first stretch, long enough to be steady, then the CEO is told.
	s, stateDir, _, _ := typedWakeCEO(t, "codex", idleWithWorkRunning)
	queueWake(t, stateDir, "notify", "g1", "done: PR https://example.test/pull/1")
	now := time.Now().UTC()
	for _, at := range []time.Duration{time.Second, 2 * time.Minute} {
		if err := s.wakeCEO(context.Background(), now.Add(at)); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := wake.AckThrough(stateDir, pending[len(pending)-1].Seq); err != nil {
		t.Fatal(err)
	}
	if err := s.wakeCEO(context.Background(), now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	queueWake(t, stateDir, "notify", "g2", "blocked: which one?")

	// Act: one frame once the new wake has waited long enough.
	if err := s.wakeCEO(context.Background(), now.Add(ceoWakeStuckAfter+time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Assert
	if states := ceoUnwokenItems(s); len(states) != 0 {
		t.Errorf("items = %q after one look at the new stretch; want none", states)
	}
}
