package supervisor

import (
	"testing"
	"time"
)

// On 2026-10-19 the supervisor filed issue #1203 for an error of its own it
// met at 08:24Z and again at 09:26Z: "Only 4.9 GB of memory is free; Start
// needs 5 GB to keep the 4 GB floor". The scheduler starts the next task at a
// reading of 5 GB or more, and the start reads memory again as it starts, so
// memory that fell short in between refuses it as passing. That is the start
// waiting for the next reading, which tries it again, not an error: the
// meter's line says why nothing started, and nobody is woken.
func TestAStartWhoseOwnMemoryReadingFallsShortWaitsForTheNextReadingAndIsNoError(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
	service := handler.Service
	// Each reading reads memory once, then its start reads it once more.
	service.Options.Dispatch.Memory = (&memoryReadings{readings: [][2]float64{{8, 40}, {4.95, 40}, {8, 40}, {8, 40}}}).read
	now := time.Now().UTC()

	// Act
	err := service.checkFleet(t.Context(), now)
	service.mu.Lock()
	line := service.scheduling.Text
	service.mu.Unlock()
	calls := spawner.recorded()
	nextErr := service.checkFleet(t.Context(), now.Add(time.Minute))

	// Assert
	if err != nil {
		t.Fatalf("the reading met %q, want the start to wait for the next reading", err)
	}
	if want := "nothing starts: Only 4.9 GB of memory is free; Start needs 5 GB to keep the 4 GB floor"; line != want || len(calls) != 0 {
		t.Fatalf("line %q and dispatches %v, want %q and none", line, calls, want)
	}
	if nextErr != nil {
		t.Fatal(nextErr)
	}
	if calls := awaitDispatch(t, service, spawner, 1); len(calls) != 1 || calls[0][0] != "spawn" || calls[0][1] != "next-task" {
		t.Fatalf("dispatches=%v, want next-task spawned at the next reading", calls)
	}
}
