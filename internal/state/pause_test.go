package state

import (
	"strings"
	"testing"
	"time"
)

func TestPauseConditionsRequireTheEvidenceThatClearsThem(t *testing.T) {
	for _, testCase := range []struct {
		reason, until string
		isValid       bool
	}{
		{reason: "memory", isValid: true},
		{reason: "overlord", isValid: true},
		{reason: "memory", until: "guess"},
		{reason: "allowance"},
		{reason: "allowance", until: "2026-10-03T12:00:00Z", isValid: true},
		{reason: "dependency", until: "task:build", isValid: true},
		{reason: "dependency", until: "task:../outside"},
		{reason: "dependency", until: "date:2026-10-03T12:00:00Z", isValid: true},
		{reason: "dependency", until: "date:tomorrow"},
		{reason: "dependency", until: "pr:https://github.com/owner/repo/pull/42", isValid: true},
		{reason: "dependency", until: "pr:https://github.com/owner/repo/pull/0"},
		{reason: "dependency", until: "pr:https://github.com/owner/repo/issues/42"},
		{reason: "dependency", until: "pr:https://user@github.com/owner/repo/pull/42"},
		{reason: "dependency", until: "pr:https://github.com/owner/repo/pull/42?token=private"},
		{reason: "dependency", until: "pr:https://elsewhere.invalid/owner/repo/pull/42"},
		{reason: "question", until: "question-42", isValid: true},
		{reason: "question", until: ""},
		{reason: "ci", until: "pr:https://github.com/owner/repo/pull/42@" + strings.Repeat("a", 40), isValid: true},
		{reason: "deploy", until: "run:https://github.com/owner/repo/actions/runs/42@" + strings.Repeat("a", 40), isValid: true},
		{reason: "ci", until: "pr:https://github.com/owner/repo/pull/42"},
		{reason: "deploy", until: "run:https://github.com/owner/repo/actions/runs/42@wrong"},
		{reason: "restart", until: "2026-10-18T11:13:27.2056637Z", isValid: true},
		{reason: "restart"},
		{reason: "restart", until: "next boot"},
		{reason: "handoff", isValid: true},
		{reason: "handoff", until: "pr:https://github.com/owner/repo/pull/42"},
		{reason: "unknown"},
	} {
		t.Run(testCase.reason+"/"+testCase.until, func(t *testing.T) {
			condition, err := NewPauseCondition(testCase.reason, testCase.until, time.Now().UTC())
			if (err == nil) != testCase.isValid {
				t.Fatalf("condition=%+v error=%v valid=%v", condition, err, testCase.isValid)
			}
			if testCase.isValid && condition.Description() == "" {
				t.Fatal("valid pause has no explanation")
			}
		})
	}
}

// A dependency pause says what the goblin waits for, in the words the board's
// card uses, not the condition's syntax, and no more (the Overlord,
// 2026-10-17, of a paused card: "I just don't like the resume").
func TestADependencyPauseSaysWhatItWaitsFor(t *testing.T) {
	for until, want := range map[string]string{
		"date:2026-10-09T13:39:00Z": "Paused until 2026-10-09T13:39:00Z",
		"task:other-task":           "Paused until other-task delivers",
		"pr:https://github.com/fpresta0607/code-goblins/pull/9": "Paused until https://github.com/fpresta0607/code-goblins/pull/9 merges",
	} {
		t.Run(until, func(t *testing.T) {
			// Arrange
			condition, err := NewPauseCondition("dependency", until, time.Now())
			if err != nil {
				t.Fatal(err)
			}

			// Act
			got := condition.Description()

			// Assert
			if got != want {
				t.Errorf("Description() = %q, want %q", got, want)
			}
		})
	}
}

// goblins pause refuses the handoff reason, which only goblins handoff
// takes, so the reasons a mistyped pause is told of leave it out.
func TestAnUnknownPauseReasonNamesOnlyTheReasonsGoblinsPauseTakes(t *testing.T) {
	// Act
	_, err := NewPauseCondition("memroy", "", time.Now())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "restart") || strings.Contains(err.Error(), "handoff") {
		t.Fatalf("unknown reason = %v, want the reasons goblins pause takes, without handoff", err)
	}
}

// A handoff pause is not always a handed-off task: a helper paused with its
// parent and a pause whose handoff was only the fallback carry the reason
// too, so its description says only what it is.
func TestAHandoffPauseDescriptionSaysOnlyThatItIsPausedForAHandoff(t *testing.T) {
	// Arrange
	condition, err := NewPauseCondition("handoff", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// Act
	got := condition.Description()

	// Assert
	if got != "Paused for a cloud handoff" {
		t.Errorf("Description() = %q, want %q", got, "Paused for a cloud handoff")
	}
}
