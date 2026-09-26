package supervisor

import (
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// squatPipe creates the run request pipe name for state before any
// supervisor does, the way another process could, and returns what its
// clients send it.
func squatPipe(t *testing.T, state string) <-chan []byte {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(runPipeName(state))
	if err != nil {
		t.Fatal(err)
	}
	handle, _, callErr := procCreateNamedPipeW.Call(uintptr(unsafe.Pointer(name)), pipeAccessDuplex, 0, pipeUnlimitedInstances, 64<<10, 64<<10, 0, 0)
	if syscall.Handle(handle) == syscall.InvalidHandle {
		t.Fatalf("the squatter's pipe could not be created: %v", callErr)
	}
	pipe := os.NewFile(handle, "squatted pipe")
	received := make(chan []byte, 1)
	go func() {
		_, _, _ = procConnectNamedPipe.Call(handle, 0)
		data, _ := io.ReadAll(io.LimitReader(pipe, 64<<10))
		received <- data
	}()
	t.Cleanup(func() { _ = pipe.Close() })
	return received
}

// A supervisor that finds its pipe name already taken does not serve run
// requests beside the squatter, and says why.
func TestRunPipeIsNotServedWhenAnotherProcessHoldsItsName(t *testing.T) {
	store, h := testStore(t)
	squatPipe(t, h.State)
	s := &Service{Store: store, subscribers: map[chan struct{}]struct{}{}}

	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		defer close(done)
		s.serveRunRequests(ctx)
	}()
	select {
	case <-done:
	case <-time.After(firstInstanceWait + 5*time.Second):
		t.Fatal("the supervisor is serving run requests on a pipe name another process created first")
	}
	s.mu.Lock()
	reported := s.lastError
	s.mu.Unlock()
	if !strings.Contains(reported, "already holds the pipe") {
		t.Fatalf("published error = %q, want it to say another process holds the pipe", reported)
	}
	s.publish(nil)
	if issues := s.Store.Snapshot().Issues; !slices.ContainsFunc(issues, func(issue string) bool { return strings.Contains(issue, "already holds the pipe") }) {
		t.Fatalf("issues after a later publish = %q, want the board still to say another process holds the pipe", issues)
	}
}

// cfo run-request sends its command only to the process that holds this
// home's watch lock: a pipe of the same name served by anything else never
// sees it.
func TestRunRequestIsNotSentToAPipeTheSupervisorDoesNotServe(t *testing.T) {
	_, h := testStore(t)
	received := squatPipe(t, h.State)

	err := PublishRun(h, RunRequest{ID: "install-driver", Title: "Install the driver", Shell: "powershell", Admin: true, CommandFile: commandFile(t, "Write-Output secret-plan\n")})

	if err == nil || !strings.Contains(err.Error(), "not this home's supervisor") {
		t.Fatalf("PublishRun to a squatted pipe = %v, want it refused", err)
	}
	select {
	case data := <-received:
		if len(data) != 0 {
			t.Fatalf("the squatter received %q", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the client never closed its connection to the squatter")
	}
}
