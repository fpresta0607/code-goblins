package supervisor

import (
	"slices"
	"testing"
	"time"
)

// processorsWithFree reads the Overlord's processor with free of its
// performance cores idle.
func processorsWithFree(free float64) func() (Processors, error) {
	return func() (Processors, error) {
		return Processors{PerformanceCores: 6, EfficiencyCores: 4, Free: free}, nil
	}
}

// On 2026-10-09 eight goblins resumed within eight minutes of a restart, each
// straight into its builds, while the Overlord's Chrome took 15.2 s to open: a
// start read memory and disk and no processor. What the supervisor starts by
// itself waits while under a quarter of the performance cores are free, and
// says so where the CFO is told of work that waits.
func TestTheSchedulerStartsNothingByItselfWhileThePerformanceCoresAreBusy(t *testing.T) {
	for _, test := range []struct {
		name        string
		free        float64
		wantStarted bool
	}{
		{"a tenth of the performance cores free", 0.1, false},
		{"a quarter of them free", 0.25, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 9*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
			handler.Service.Options.Dispatch.Processors = processorsWithFree(test.free)

			// Act
			err := handler.Service.checkFleet(t.Context(), time.Now().UTC())

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if test.wantStarted {
				if calls := awaitDispatch(t, handler.Service, spawner, 1); calls[0][0] != "spawn" || calls[0][1] != "next-task" {
					t.Fatalf("dispatches=%v, want next-task spawned", calls)
				}
				return
			}
			if calls := spawner.recorded(); len(calls) != 0 {
				t.Fatalf("dispatches=%v, want nothing started while the performance cores are busy", calls)
			}
			handler.Service.mu.Lock()
			scheduling := handler.Service.scheduling
			handler.Service.mu.Unlock()
			want := WaitingWork{ID: "next-task", Why: "Only 0.6 of 6 performance cores are free, and a start by itself waits for 1.5"}
			if scheduling == nil || !slices.Contains(scheduling.Waiting, want) {
				t.Fatalf("scheduling=%+v, want %+v among the work that waits", scheduling, want)
			}
		})
	}
}

// A Start the Overlord clicks is his word on his own machine, and runs.
func TestAStartTheOverlordClicksRunsWhileThePerformanceCoresAreBusy(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 9*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
	handler.Service.Options.Dispatch.Processors = processorsWithFree(0)

	// Act
	response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
	if response.Code == 202 {
		waitStarted(t, handler, "next-task")
	}

	// Assert
	if response.Code != 202 {
		t.Fatalf("start=%d %s, want the Overlord's Start accepted", response.Code, response.Body)
	}
	if calls := spawner.recorded(); len(calls) != 1 || calls[0][0] != "spawn" || calls[0][1] != "next-task" {
		t.Fatalf("dispatches=%v, want next-task spawned", calls)
	}
}

// After a restart the CFO comes back at once, and each goblin only at a
// reading with room on the performance cores.
func TestTheComebackBringsNoGoblinBackWhileThePerformanceCoresAreBusy(t *testing.T) {
	// Arrange
	recorder := &comebackRecorder{}
	service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8}, [2]float64{8, 8}, [2]float64{8, 8}, [2]float64{8, 8})
	closedCFO(t, h.State)
	terminalStarted(t, h, NativeCFOTerminal, lastSignIn.Add(time.Minute))
	workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))
	free := 0.1
	service.Options.Dispatch.Processors = func() (Processors, error) {
		return Processors{PerformanceCores: 6, EfficiencyCores: 4, Free: free}, nil
	}
	var came [][]string

	// Act
	for minute := range 4 {
		if minute == 3 {
			free = 0.5
		}
		reading(t, service, minute)
		came = append(came, recorder.came())
	}

	// Assert
	want := [][]string{{"cfo"}, {"cfo"}, {"cfo"}, {"cfo", "alpha"}}
	for index := range want {
		if !slices.Equal(came[index], want[index]) {
			t.Errorf("after reading %d came back %v, want %v", index+1, came[index], want[index])
		}
	}
}
