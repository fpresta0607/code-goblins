package supervisor

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
)

func TestNativeCFOLeavesAnUnreadyComposerUntouched(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		harness string
		screen  []string
		readErr error
	}{
		{"codex draft", "codex", []string{"› my unfinished thought", "80% context left"}, nil},
		{"codex trust dialog", "codex", []string{"Do you trust the contents of this directory?", "› 1. Yes, continue", "› Ask Codex to do anything"}, nil},
		{"codex unreadable", "codex", nil, errors.New("screen unavailable")},
		{"claude draft", "claude", []string{"❯ my unfinished thought", "⏵⏵ bypass permissions on (shift+tab to cycle)"}, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			terminal, _ := shimTerminal(t, stateDir, scenario.harness)
			terminal.typeLine(t, "register by program")
			if lines := terminal.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
				t.Fatalf("registration = %q", lines)
			}
			connection := &CFOConnection{State: stateDir, ReadScreen: func(host.Record) ([]string, error) {
				return scenario.screen, scenario.readErr
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			// Act
			result, err := connection.Send(ctx, registrationIdentity(t, stateDir), "Reply received")

			// Assert
			if !errors.Is(err, ErrDeferred) || result.Awaiting != nil {
				t.Errorf("Send = %+v, %v; want queued without typing or claiming Sent", result, err)
			}
			if lines := terminal.lines(t); len(lines) != 1 {
				t.Errorf("terminal input = %q; want only registration, with no answer or dialog choice", lines)
			}
		})
	}
}

func TestNativeCFOAnswerPathsPreserveRepliesWhileItIsTyping(t *testing.T) {
	for _, harness := range []string{"codex", "claude"} {
		for _, kind := range []string{"cfo_answer", "review_answer", "question_clear", "run_result"} {
			t.Run(harness+"/"+kind, func(t *testing.T) {
				// Arrange
				store, home := testStore(t)
				mode := "ready"
				if harness == "claude" {
					mode = "claude-ready"
				}
				terminal := nativeCFOComposer(t, home.State, mode)
				identity := registrationIdentity(t, home.State)
				connection := &CFOConnection{State: home.State, ReadScreen: func(host.Record) ([]string, error) {
					if harness == "claude" {
						rule := strings.Repeat("─", 80)
						return []string{rule, "❯ unfinished thought", rule, "⏵⏵ bypass permissions on (shift+tab to cycle)"}, nil
					}
					return []string{"› unfinished thought", "100% context left"}, nil
				}}
				service := &Service{Store: store, Options: Options{CFO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
				switch kind {
				case "cfo_answer", "question_clear":
					question := Question{ID: "held-question", Identity: identity, Text: "Choose", Options: []string{"One"}, CreatedAt: time.Now().UTC()}
					if err := store.acceptQuestion(question); err != nil {
						t.Fatal(err)
					}
					action := Action{ID: "held-action", Kind: kind, Generation: identity, QuestionID: question.ID}
					if kind == "cfo_answer" {
						action.Text, action.AnswerKind = "One", "option"
					}
					if _, err := store.Queue(action); err != nil {
						t.Fatal(err)
					}
				case "review_answer":
					review := Review{ID: "held-review", Identity: identity, Title: "Check the proof", State: "open", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
					if err := store.acceptReview(review); err != nil {
						t.Fatal(err)
					}
					if _, err := store.Queue(Action{ID: "held-action", Kind: kind, Generation: identity, ReviewID: review.ID, Text: "One"}); err != nil {
						t.Fatal(err)
					}
				case "run_result":
					finishedRun(t, service, identity, "held-run")
				}

				// Act
				if kind == "run_result" {
					if err := service.finishRuns(context.Background()); err != nil {
						t.Fatal(err)
					}
				} else if err := store.ProcessOne(context.Background(), service.execute); !errors.Is(err, ErrDeferred) {
					t.Fatalf("held send = %v; want an untyped queued reply", err)
				}
				reopened, err := Open(home)
				if err != nil {
					t.Fatal(err)
				}
				service.Store = reopened

				// Assert
				if lines := terminal.lines(t); len(lines) != 1 {
					t.Errorf("input while typing = %q; want only registration", lines)
				}
				if kind == "review_answer" && reopened.Snapshot().Reviews[0].Delivered {
					t.Fatal("untyped review reply was marked delivered")
				}
				if kind == "run_result" && !strings.Contains(reopened.Snapshot().Runs[0].Untold, "Run item held-run") {
					t.Fatal("the untyped run result was lost", reopened.Snapshot().Runs[0])
				}
				if kind == "run_result" {
					if err := service.retellRuns(context.Background()); err != nil {
						t.Fatalf("retry while typing = %v; want normal waiting without losing the result", err)
					}
					if reopened.Snapshot().Runs[0].Untold == "" {
						t.Fatal("retry while typing discarded the run result")
					}
				}
				connection.ReadScreen = nil
				if kind == "run_result" {
					if err := service.retellRuns(context.Background()); err != nil {
						t.Fatal(err)
					}
					if reopened.Snapshot().Runs[0].Untold != "" {
						t.Error("accepted run result still waits to be told")
					}
				} else if err := reopened.ProcessOne(context.Background(), service.execute); err != nil {
					t.Fatal(err)
				}
				if lines := terminal.lines(t); len(lines) != 2 || !strings.Contains(lines[1], "One") && !strings.Contains(lines[1], "held-run") && !strings.Contains(lines[1], "dismissed your question held-question") {
					t.Errorf("resumed delivery input = %q; want the preserved reply accepted once", lines)
				}
				if kind == "cfo_answer" && reopened.Snapshot().Questions[0].AnsweredBy != "overlord" {
					t.Error("accepted question answer lacks its history author")
				}
				if kind == "review_answer" && !reopened.Snapshot().Reviews[0].Delivered {
					t.Error("accepted review reply remains undelivered")
				}
			})
		}
	}
}

func TestNativeCFOBusyAnswersBecomeDeliveredOnlyWhenTheQueuedPromptIsTaken(t *testing.T) {
	for _, harness := range []string{"codex", "claude"} {
		for _, kind := range []string{"cfo_answer", "review_answer"} {
			t.Run(harness+"/"+kind, func(t *testing.T) {
				// Arrange
				store, home := testStore(t)
				mode := "busy"
				if harness == "claude" {
					mode = "claude-busy"
				}
				terminal := nativeCFOComposer(t, home.State, mode)
				identity := registrationIdentity(t, home.State)
				service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: home.State}}}
				action := Action{ID: "busy-answer", Kind: kind, Generation: identity, Text: "Reply received"}
				if kind == "cfo_answer" {
					question := Question{ID: "busy-question", Identity: identity, Text: "Choose", CreatedAt: time.Now().UTC()}
					if err := store.acceptQuestion(question); err != nil {
						t.Fatal(err)
					}
					action.QuestionID = question.ID
				} else {
					review := Review{ID: "busy-review", Identity: identity, Title: "Check the proof", State: "open", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
					if err := store.acceptReview(review); err != nil {
						t.Fatal(err)
					}
					action.ReviewID = review.ID
				}
				if _, err := store.Queue(action); err != nil {
					t.Fatal(err)
				}

				// Act
				if err := store.ProcessOne(context.Background(), service.execute); err != nil {
					t.Fatal(err)
				}
				reopened, err := Open(home)
				if err != nil {
					t.Fatal(err)
				}
				sent := reopened.Snapshot().Actions[0]

				// Assert
				if sent.Status != "running" || sent.Awaiting == nil || sent.Message != sentToCFO {
					t.Fatalf("busy send = %+v; want submitted and awaiting actual acceptance", sent)
				}
				if kind == "review_answer" && reopened.Snapshot().Reviews[0].Delivered {
					t.Fatal("busy review was marked delivered before it was taken")
				}
				if err := os.WriteFile(terminal.typed+".accept", nil, 0o600); err != nil {
					t.Fatal(err)
				}
				terminal.waitForLines(t, 3)
				if err := reopened.Ingest(); err != nil {
					t.Fatal(err)
				}
				if err := reopened.settleDeliveries(time.Now().UTC(), working); err != nil {
					t.Fatal(err)
				}
				delivered := reopened.Snapshot().Actions[0]
				if delivered.Status != "succeeded" || delivered.Awaiting != nil || !strings.Contains(delivered.Message, "hook reported") {
					t.Errorf("late acceptance = %+v; want delivered with the native prompt receipt", delivered)
				}
				if input := strings.Join(terminal.lines(t), "\n"); strings.Count(input, "queued Overlord:") != 1 || strings.Count(input, "accepted Overlord:") != 1 {
					t.Errorf("busy input = %q; want one submission and one later acceptance", input)
				}
				if kind == "review_answer" && !reopened.Snapshot().Reviews[0].Delivered {
					t.Error("the accepted review still reads undelivered")
				}
				if kind == "cfo_answer" && reopened.Snapshot().Questions[0].AnsweredBy != "overlord" {
					t.Error("the accepted question lacks its history author")
				}
			})
		}
	}
}

func TestAnUntypedCFOAnswerStaysQueuedAcrossReopen(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	_, identity, _, _ := primaryFixture(t, store)
	question := Question{ID: "held-answer", Identity: identity, Text: "Choose", Options: []string{"One"}, CreatedAt: time.Now().UTC()}
	if err := store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	answer := Action{ID: "held-action", Kind: "cfo_answer", Generation: identity, QuestionID: question.ID, Text: "One"}
	if _, err := store.Queue(answer); err != nil {
		t.Fatal(err)
	}

	// Act
	err := store.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
		return Evaluation{}, errors.Join(ErrDeferred, errors.New("The CFO is typing; your answer is queued and nothing was sent."))
	})
	reopened, openErr := Open(home)

	// Assert
	if !errors.Is(err, ErrDeferred) || openErr != nil {
		t.Fatalf("deferred action/reopen = %v, %v", err, openErr)
	}
	queued, asked := outcome(reopened)
	if queued.Status != "queued" || queued.Awaiting != nil || asked.Status != "queued" || queued.Text != "One" {
		t.Fatalf("reopened answer = %+v, question = %+v; want the unsent answer preserved as queued", queued, asked)
	}
	if err := reopened.ProcessOne(context.Background(), func(_ context.Context, action Action) (Evaluation, error) {
		if action.ID != answer.ID || action.Text != answer.Text {
			t.Errorf("resumed answer = %+v; want the same answer", action)
		}
		return Evaluation{Reason: "Taken by the CFO."}, nil
	}); err != nil {
		t.Fatal(err)
	}
	delivered, history := outcome(reopened)
	if delivered.Status != "succeeded" || history.AnsweredBy != "overlord" || history.AnsweredOption != "One" {
		t.Errorf("delivery/history = %+v, %+v; want the preserved answer delivered with its author and choice", delivered, history)
	}
}

func TestADeferredDeliveryDoesNotHoldOtherRunnableActions(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, identity, _, _ := primaryFixture(t, store)
	question := Question{ID: "held-answer", Identity: identity, Text: "Choose", CreatedAt: time.Now().UTC()}
	if err := store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	for _, action := range []Action{
		{ID: "held-action", Kind: "cfo_answer", Generation: identity, QuestionID: question.ID, Text: "One"},
		{ID: "other-action", Kind: "evaluate", TaskID: "task-1"},
	} {
		if _, err := store.Queue(action); err != nil {
			t.Fatal(err)
		}
	}
	var attempted []string
	execute := func(_ context.Context, action Action) (Evaluation, error) {
		attempted = append(attempted, action.ID)
		if action.ID == "held-action" {
			return Evaluation{}, ErrDeferred
		}
		return Evaluation{Reason: "Checked."}, nil
	}

	// Act
	first := store.ProcessOne(context.Background(), execute)
	second := store.ProcessOne(context.Background(), execute)

	// Assert
	if !errors.Is(first, ErrDeferred) || second != nil || strings.Join(attempted, ",") != "held-action,other-action" {
		t.Errorf("attempts = %q, outcomes = %v, %v; want one deferred send and the independent action", attempted, first, second)
	}
	if store.HasRunnable() {
		t.Error("the deferred send can run again immediately; want a bounded retry gap")
	}
}

func TestNativeCFOProvesAcceptanceAcrossTheDialogAndPasteBoundary(t *testing.T) {
	for _, mode := range []string{"daybreak", "paste", "late-modal", "daybreak-stuck"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			terminal := nativeCFOComposer(t, stateDir, mode)
			// A dialog's first key waits two seconds for it to settle, and a
			// stuck offer is left only when this context ends, so the budget
			// leaves a loaded machine time to send its one Escape.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Act
			result, err := (&CFOConnection{State: stateDir}).Send(ctx, registrationIdentity(t, stateDir), "Reply received")
			input := strings.Join(terminal.lines(t), "\n")

			// Assert
			if strings.Contains(input, "dialog-enter") || strings.Contains(input, "typed-under-dialog") {
				t.Errorf("unsafe input: %s", input)
			}
			switch mode {
			case "daybreak", "paste":
				if err != nil || result.Awaiting != nil || strings.Count(input, "accepted Overlord: Reply received") != 1 {
					t.Errorf("Send = %+v, %v, input %q; want one accepted answer with no unproved Sent state", result, err, input)
				}
			case "late-modal":
				if err == nil || errors.Is(err, ErrDeferred) || strings.Contains(input, "accepted ") {
					t.Errorf("Send = %+v, %v, input %q; want the typed answer held unsubmitted, without replay", result, err, input)
				}
			case "daybreak-stuck":
				if !errors.Is(err, ErrDeferred) || result.Awaiting != nil || strings.Count(input, "escape") != 1 {
					t.Errorf("Send = %+v, %v, input %q; want one dismissal attempt and the untyped answer queued", result, err, input)
				}
			}
		})
	}
}

func TestNativeCFOCannotCallAnAnswerSentWhileItRemainsInTheIdleComposer(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	terminal := nativeCFOComposer(t, stateDir, "ignored-enter")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Act
	result, err := (&CFOConnection{State: stateDir}).Send(ctx, registrationIdentity(t, stateDir), "Reply received")

	// Assert
	if err == nil || errors.Is(err, ErrDeferred) || result.Awaiting != nil {
		t.Errorf("idle unsent input = %+v, %v; want unconfirmed typed input, without Sent or automatic replay", result, err)
	}
	if input := strings.Join(terminal.lines(t), "\n"); strings.Contains(input, "accepted ") || strings.Contains(input, "queued ") {
		t.Errorf("idle input = %q; want no accepted or queued prompt", input)
	}
}
