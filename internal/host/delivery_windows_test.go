package host

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// A delivery types each part into the terminal and returns only once the host
// has written it into the terminal's input, so the sender knows it arrived.
func TestADeliveryReturnsOnceTheHostWroteEachPart(t *testing.T) {
	_, record := launch(t)
	v := connect(t, record)
	v.waitFor(t, "ready")

	delivery, err := DialDelivery(record)
	if err != nil {
		t.Fatalf("DialDelivery: %v", err)
	}
	defer delivery.Close()
	for _, part := range []string{"answered on the board", "\r"} {
		if err := delivery.Write([]byte(part)); err != nil {
			t.Fatalf("Write %q: %v", part, err)
		}
	}

	v.waitFor(t, "got answered on the board")
}

// A host started by an older cfo types input without confirming it, so a
// delivery refuses it before typing anything rather than guess.
func TestADeliveryRefusesAHostThatCannotConfirm(t *testing.T) {
	name, err := pipeName()
	if err != nil {
		t.Fatal(err)
	}
	pipes, err := listen(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(pipes.waiting) })
	typed := make(chan bool, 1)
	go func() {
		connection, err := pipes.accept()
		if err != nil {
			typed <- false
			return
		}
		defer connection.Close()
		if _, err := readHello(connection); err != nil {
			typed <- false
			return
		}
		// An older host answers a delivery as it answers a viewer: a plain
		// hello, then the history, then whatever it is typed.
		_ = writeHello(connection, hello{Version: Version})
		_ = writeFrame(connection, frameOutput, []byte("history"))
		kind, _, err := readFrame(connection)
		typed <- err == nil && kind == frameInput
	}()

	delivery, err := DialDelivery(Record{ID: "old", Pipe: name, Token: "token", Version: Version, HostPID: os.Getpid()})

	if err == nil {
		_ = delivery.Close()
	}
	if !errors.Is(err, ErrNoDelivery) {
		t.Fatalf("DialDelivery error = %v, want the host refused as one that cannot confirm", err)
	}
	if <-typed {
		t.Error("the older host was typed into")
	}
}
