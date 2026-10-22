package supervisor

import (
	"bufio"
	"context"
	"os"
	"strings"
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

// shorten sets *wait to length for the test.
func shorten(t *testing.T, wait *time.Duration, length time.Duration) {
	t.Helper()
	previous := *wait
	*wait = length
	t.Cleanup(func() { *wait = previous })
}

// A client that connected sends its request across a pause, since the pause
// stopped it with the supervisor, and is answered (#1247).
func TestARunRequestSentAfterAPauseOfTheWholeMachineIsAnswered(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the client connects and sends its request only after the
	// pause.
	store, h := testStore(t)
	shorten(t, &runReadTimeout, time.Second)
	runPipe(t, &Service{Store: store})
	client := dialRunPipe(t, h.State)
	defer client.Close()
	awaketest.ReadyThenHold()

	// Act
	_, err := client.Write([]byte("not a request\n"))
	var answer string
	if err == nil {
		answer, err = bufio.NewReader(client).ReadString('\n')
	}

	// Assert
	if err != nil || !strings.Contains(answer, "not valid JSON") {
		t.Fatalf("the supervisor answered %q, %v across a pause of %s to a request sent after it; want its refusal of the request", answer, err, awaketest.Length)
	}
}

// A client waits across a pause for the supervisor's answer, since the pause
// stopped the supervisor too (#1247).
func TestARunRequestWaitsForTheSupervisorsAnswerAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the supervisor's loop takes the look the request asks for
	// only after the pause.
	store, h := testStore(t)
	shorten(t, &runReplyTimeout, time.Second)
	s := &Service{Store: store, looks: make(chan chan struct{})}
	runPipe(t, s)
	go func() {
		looked := <-s.looks
		awaketest.ReadyThenHold()
		close(looked)
	}()

	// Act
	_, err := askPipe(h.State, runPipeRequest{Kind: "reported"})

	// Assert
	if err != nil {
		t.Fatalf("the request gave up across a pause of %s on a supervisor that answered after it: %v", awaketest.Length, err)
	}
}
