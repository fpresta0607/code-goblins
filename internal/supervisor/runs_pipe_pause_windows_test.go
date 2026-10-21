package supervisor

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/fpresta0607/code-goblins/internal/awake/awaketest"
)

// A machine that pauses stops the supervisor that is ending with the one
// starting after it, and the wall clock runs on through the pause. A
// supervisor that held its wait for the run pipe's name to the wall clock
// found it gone once the machine ran again and served no run requests,
// though the one before had had no time to let go (#1180). The pause spends
// none of the wait, so the supervisor serves the pipe once the one before
// lets go of it after the pause.
func TestTheRunPipeIsServedOnceTheSupervisorBeforeLetsGoAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the supervisor before still holds the pipe's name, and lets go
	// of it after the pause.
	store, h := testStore(t)
	name, err := syscall.UTF16PtrFromString(runPipeName(h.State))
	if err != nil {
		t.Fatal(err)
	}
	before, _, callErr := procCreateNamedPipeW.Call(uintptr(unsafe.Pointer(name)), pipeAccessDuplex|fileFlagFirstInstance, pipeRejectRemoteClients, pipeUnlimitedInstances, 64<<10, 64<<10, 0, 0)
	if syscall.Handle(before) == syscall.InvalidHandle {
		t.Fatalf("the pipe of the supervisor before could not be created: %v", callErr)
	}
	s := &Service{Store: store, subscribers: map[chan struct{}]struct{}{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Minute):
			t.Error("the supervisor did not stop serving run requests within a minute")
		}
	})

	// Act
	go func() {
		defer close(done)
		s.serveRunRequests(ctx)
	}()
	awaketest.ReadyThenHold()
	if err := syscall.CloseHandle(syscall.Handle(before)); err != nil {
		t.Fatal(err)
	}

	// Assert
	for tries := 0; ; tries++ {
		select {
		case <-done:
			s.mu.Lock()
			reported := s.lastError
			s.mu.Unlock()
			t.Fatalf("the supervisor gave up the run pipe across a pause of %s on a name the one before let go of after it: %s", awaketest.Length, reported)
		default:
		}
		if pipe, err := os.OpenFile(runPipeName(h.State), os.O_RDWR, 0); err == nil {
			_ = pipe.Close()
			return
		}
		if tries == 200 {
			t.Fatal("the supervisor did not serve the run pipe within 5 s of the one before letting go")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
