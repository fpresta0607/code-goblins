package supervisor

import (
	"os"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// While an update of the home installs, the supervisor starts and resumes
// nothing by itself: the update is about to stop it and swap the programs a
// start would run a terminal from, and the command it would dispatch is
// refused meanwhile. The reading after the update dispatches as usual.
func TestNothingStartsByItselfWhileAnUpdateInstalls(t *testing.T) {
	// Arrange: a goblin paused for memory, and memory to bring it back.
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC()
	pausedGoblin(t, h, "paused-task", "memory", "", now.Add(-time.Hour))
	meter := &memoryReadings{readings: [][2]float64{{8, 8}, {8, 8}, {8, 8}, {8, 8}}}
	handler.Service.Options.Dispatch.Memory = meter.read
	if err := os.MkdirAll(update.Dir(h.State), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.AcquireExclusiveNamed(update.Dir(h.State), update.LockName); err != nil {
		t.Fatal(err)
	}
	isInstalling := true
	t.Cleanup(func() {
		if isInstalling {
			_ = lock.ReleaseExclusiveNamed(update.Dir(h.State), update.LockName)
		}
	})

	// Act: two good readings, which resume a memory pause, while it installs.
	for _, at := range []time.Time{now, now.Add(time.Minute)} {
		if err := handler.Service.checkFleet(t.Context(), at); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	handler.Service.starts.Lock()
	isChanging := handler.Service.starting != "" || len(handler.Service.changing) > 0
	handler.Service.starts.Unlock()
	if calls := spawner.recorded(); len(calls) != 0 || isChanging {
		t.Fatalf("the supervisor dispatched %v (changing %t) while an update installs", calls, isChanging)
	}

	// Act: the update is done.
	if err := lock.ReleaseExclusiveNamed(update.Dir(h.State), update.LockName); err != nil {
		t.Fatal(err)
	}
	isInstalling = false
	for _, at := range []time.Time{now.Add(2 * time.Minute), now.Add(3 * time.Minute)} {
		if err := handler.Service.checkFleet(t.Context(), at); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	calls := awaitDispatch(t, handler.Service, spawner, 1)
	if calls[0][0] != "resume" || calls[0][1] != "paused-task" {
		t.Fatalf("first dispatch after the update=%v, want the memory pause resumed", calls)
	}
}
