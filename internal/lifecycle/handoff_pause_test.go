package lifecycle

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestAHandoffPauseResumesOnlyThroughHandoffReturnOrCancel(t *testing.T) {
	for _, test := range []struct {
		name          string
		isHandoffEnd  bool
		wantPhase     string
		wantIsResumed bool
	}{
		{name: "a plain resume", wantPhase: "paused"},
		{name: "handoff return or cancel", isHandoffEnd: true, wantPhase: "running", wantIsResumed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, meta := lifecycleFixture(t)
			if _, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "handoff-pause", Action: "pause", Reason: "handoff"}); err != nil {
				t.Fatal(err)
			}
			isResumed := false
			service.Operations.Resume = func(context.Context, state.TaskMeta, *state.Lifecycle) error {
				isResumed = true
				return nil
			}

			// Act
			_, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume", IsHandoffEnd: test.isHandoffEnd})

			// Assert
			record, readErr := state.ReadLifecycle(service.StateDir, meta.ID)
			if readErr != nil || record.Phase != test.wantPhase || isResumed != test.wantIsResumed {
				t.Fatalf("resume = %v, record %+v (%v), resumed %v; want phase %s, resumed %v", err, record, readErr, isResumed, test.wantPhase, test.wantIsResumed)
			}
			if !test.isHandoffEnd && (err == nil || !strings.Contains(err.Error(), "goblins handoff return")) {
				t.Fatalf("a plain resume of a handed-off task = %v, want a refusal naming goblins handoff return and cancel", err)
			}
		})
	}
}

func TestNoOtherPauseReplacesAHandoffPauseInPlace(t *testing.T) {
	// Arrange
	service, meta := lifecycleFixture(t)
	if _, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "handoff-pause", Action: "pause", Reason: "handoff"}); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "memory-pause", Action: "pause", Reason: "memory"})

	// Assert
	record, readErr := state.ReadLifecycle(service.StateDir, meta.ID)
	if err == nil || readErr != nil || record.Pause == nil || record.Pause.Reason != "handoff" {
		t.Fatalf("memory pause over a handoff = %v, record %+v (%v); want it refused and the handoff pause kept", err, record.Pause, readErr)
	}
}

func TestAPauseRecordsWhetherItWroteTheGoblinsHandoffItself(t *testing.T) {
	for _, test := range []struct {
		name         string
		isOwn        bool
		wantFallback bool
	}{
		{name: "the goblin wrote its own", isOwn: true},
		{name: "the pause wrote it for the goblin", wantFallback: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, meta := lifecycleFixture(t)
			service.Operations.Prepare = func(_ context.Context, _ state.TaskMeta, path string) error {
				if test.isOwn {
					return os.WriteFile(path, []byte("Done and left, for publication."), 0o600)
				}
				return nil
			}
			service.Operations.Handoff = func(context.Context, state.TaskMeta) []byte {
				return []byte("Written for the goblin from its record.")
			}

			// Act
			record, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "handoff-pause", Action: "pause", Reason: "handoff"})

			// Assert
			if err != nil || !record.HandoffSaved || record.HandoffFallback != test.wantFallback {
				t.Fatalf("pause = %+v, %v; want a saved handoff whose fallback mark is %v", record, err, test.wantFallback)
			}
		})
	}
}

// The pause waits for the file its instruction names, written beside its
// place and renamed onto it, as every pause does.
func TestAHandoffPauseInstructionNamesTheFileThePauseWaitsFor(t *testing.T) {
	// Arrange
	handoff := "/tasktmp/task/pause-handoff.md"

	// Act
	instruction := HandoffPauseInstruction(handoff)

	// Assert
	for _, want := range []string{handoff + ".partial", "rename it to " + handoff} {
		if !strings.Contains(instruction, want) {
			t.Errorf("instruction %q does not name %q", instruction, want)
		}
	}
}

// A helper paused with its handed-off parent takes its parent's handoff
// reason, but resumes with its parent as any helper does, through a plain
// resume, so the guard is the parent's alone.
func TestAHelperPausedWithAHandedOffParentResumesWithIt(t *testing.T) {
	// Arrange
	service, meta := lifecycleFixture(t)
	if _, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: state.HelperOperation("handoff-pause", meta.ID), Action: "pause", Reason: "handoff"}); err != nil {
		t.Fatal(err)
	}

	// Act
	record, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "auto-resume-1", Action: "resume", Reason: "Pause condition cleared"})

	// Assert
	if err != nil || record.Phase != "running" {
		t.Fatalf("resume of a helper paused with its handed-off parent = %+v, %v; want it running", record, err)
	}
}

// A handoff pause taken in place over another pause would keep the handoff
// that pause asked for, which was never written for publication.
func TestAHandoffPauseIsRefusedOverAPauseOfAnotherReason(t *testing.T) {
	// Arrange
	service, meta := lifecycleFixture(t)
	if _, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "memory-pause", Action: "pause", Reason: "memory"}); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "handoff-pause", Action: "pause", Reason: "handoff"})

	// Assert
	record, readErr := state.ReadLifecycle(service.StateDir, meta.ID)
	if err == nil || !strings.Contains(err.Error(), "for publication") || readErr != nil || record.Pause == nil || record.Pause.Reason != "memory" {
		t.Fatalf("handoff pause over a memory pause = %v, record %+v (%v); want it refused and the memory pause kept", err, record.Pause, readErr)
	}
}

func TestAResumeKeepsTheMarkThatThePauseWroteTheHandoffItself(t *testing.T) {
	// Arrange
	service, meta := lifecycleFixture(t)
	service.Operations.Prepare = func(context.Context, state.TaskMeta, string) error { return nil }
	service.Operations.Handoff = func(context.Context, state.TaskMeta) []byte {
		return []byte("Written for the goblin from its record.")
	}
	if _, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "handoff-pause", Action: "pause", Reason: "handoff"}); err != nil {
		t.Fatal(err)
	}

	// Act
	record, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "handoff-end-1", Action: "resume", IsHandoffEnd: true})

	// Assert
	if err != nil || record.Phase != "running" || !record.HandoffFallback {
		t.Fatalf("resume = %+v, %v; want it running with the fallback mark kept", record, err)
	}
}

// A pause whose handoff was only the fallback it wrote published nothing, so
// the task is not handed off and a plain resume clears it.
func TestAHandoffPauseOfTheFallbackHandoffResumesPlainly(t *testing.T) {
	// Arrange
	service, meta := lifecycleFixture(t)
	service.Operations.Prepare = func(context.Context, state.TaskMeta, string) error { return nil }
	service.Operations.Handoff = func(context.Context, state.TaskMeta) []byte {
		return []byte("Written for the goblin from its record.")
	}
	if _, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "handoff-pause", Action: "pause", Reason: "handoff"}); err != nil {
		t.Fatal(err)
	}

	// Act
	record, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume"})

	// Assert
	if err != nil || record.Phase != "running" {
		t.Fatalf("plain resume of a fallback handoff pause = %+v, %v; want it running", record, err)
	}
}

// A parent's handoff pause reaches a helper its memory pause already holds,
// which takes its parent's reason in place as a helper does.
func TestAHelpersMemoryPauseTakesItsParentsHandoffReasonInPlace(t *testing.T) {
	// Arrange
	service, meta := lifecycleFixture(t)
	if _, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "memory-pause", Action: "pause", Reason: "memory"}); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: state.HelperOperation("handoff-pause", meta.ID), Action: "pause", Reason: "handoff"})

	// Assert
	record, readErr := state.ReadLifecycle(service.StateDir, meta.ID)
	if err != nil || readErr != nil || record.Pause == nil || record.Pause.Reason != "handoff" {
		t.Fatalf("helper's handoff re-pause = %v, record %+v (%v); want it taken in place", err, record.Pause, readErr)
	}
}
