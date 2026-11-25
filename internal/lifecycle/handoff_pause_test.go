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
			writesOwnHandoff(&service)
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
	writesOwnHandoff(&service)
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
	writesOwnHandoff(&service)
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

// writesOwnHandoff has service's goblin write its own handoff when its pause
// asks, as a goblin that reached its stopping point does.
func writesOwnHandoff(service *Service) {
	service.Operations.Prepare = func(_ context.Context, _ state.TaskMeta, path string) error {
		return os.WriteFile(path, []byte("Done and left, for publication."), 0o600)
	}
}

// A goblin relaunched outside its lifecycle, as goblins switch does, leaves
// its earlier session's pause record behind: neither that pause nor an
// earlier session's handoff holds the session that now runs.
func TestAPauseOfAnEarlierSessionDoesNotHoldTheSessionThatRuns(t *testing.T) {
	for _, test := range []struct {
		name          string
		earlier, next Request
	}{
		{name: "a handoff over an earlier session's Overlord pause", earlier: Request{Operation: "overlord-pause", Action: "pause", Reason: "overlord"}, next: Request{Operation: "handoff-pause", Action: "pause", Reason: "handoff"}},
		{name: "a pause over an earlier session's handoff", earlier: Request{Operation: "handoff-pause", Action: "pause", Reason: "handoff"}, next: Request{Operation: "memory-pause", Action: "pause", Reason: "memory"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, meta := lifecycleFixture(t)
			writesOwnHandoff(&service)
			test.earlier.ID, test.earlier.Generation = meta.ID, meta.SpawnGen
			if _, err := service.Run(t.Context(), test.earlier); err != nil {
				t.Fatal(err)
			}
			meta.SpawnGen = "generation-2"
			if err := state.WriteTaskMeta(service.StateDir, meta); err != nil {
				t.Fatal(err)
			}
			test.next.ID, test.next.Generation = meta.ID, meta.SpawnGen

			// Act
			record, err := service.Run(t.Context(), test.next)

			// Assert
			if err != nil || record.Phase != "paused" || record.Pause == nil || record.Pause.Reason != test.next.Reason {
				t.Fatalf("pause = %+v, %v; want the running session paused for %s", record, err, test.next.Reason)
			}
		})
	}
}

// A second goblins handoff, retried after a publication failed part-way,
// takes its pause in place over the first, keeping the goblin's handoff.
func TestASecondHandoffPauseIsTakenInPlaceOverTheFirst(t *testing.T) {
	// Arrange
	service, meta := lifecycleFixture(t)
	writesOwnHandoff(&service)
	first, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "handoff-1", Action: "pause", Reason: "handoff"})
	if err != nil {
		t.Fatal(err)
	}

	// Act
	second, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "handoff-2", Action: "pause", Reason: "handoff"})

	// Assert
	if err != nil || second.Phase != "paused" || second.Handoff != first.Handoff || !second.HandoffSaved || !second.IsHandedOff() {
		t.Fatalf("second handoff pause = %+v, %v; want it in place, keeping %s", second, err, first.Handoff)
	}
}
