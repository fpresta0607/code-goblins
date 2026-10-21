package host

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/awake/awaketest"
)

// A machine that pauses stops the host with its client, and the wall clock
// runs on through the pause. A wait between them held to the wall clock found
// its deadline passed once the machine ran again, though the side it waited
// on had had no time to act (#1180). Each test shortens its wait under the
// pause's length, and the other side acts only once the machine runs again.

// shorten sets *wait to length for the test.
func shorten(t *testing.T, wait *time.Duration, length time.Duration) {
	t.Helper()
	previous := *wait
	*wait = length
	t.Cleanup(func() { *wait = previous })
}

// hostPipe listens on a fresh host pipe served by this process.
func hostPipe(t *testing.T) (string, *listener) {
	t.Helper()
	name, err := pipeName()
	if err != nil {
		t.Fatal(err)
	}
	pipes, err := listen(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(pipes.waiting) })
	return name, pipes
}

// A client that finds every instance of the host's pipe busy waits for the
// host to open the next across a pause, since the pause stopped the host too.
func TestADialWaitsOutABusyHostPipeAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: a first client holds the pipe's one instance until the host
	// opens the next, after the pause.
	name, pipes := hostPipe(t)
	first, err := dialPipe(name, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	shorten(t, &busyPipeWait, time.Second)
	opened := make(chan error, 1)
	go func() {
		awaketest.ReadyThenHold()
		connection, err := pipes.accept()
		if err == nil {
			_ = connection.Close()
		}
		opened <- err
	}()

	// Act
	second, err := dialPipe(name, os.Getpid())

	// Assert
	if err != nil {
		t.Fatalf("the dial gave up across a pause of %s on a pipe its host freed after it: %v", awaketest.Length, err)
	}
	_ = second.Close()
	if err := <-opened; err != nil {
		t.Errorf("the host could not open its next instance: %v", err)
	}
}

// A client that said hello waits for the host's answer across a pause.
func TestAClientWaitsForTheHostsAnswerAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the host answers the hello after the pause.
	name, pipes := hostPipe(t)
	shorten(t, &handshakeTimeout, time.Second)
	go func() {
		connection, err := pipes.accept()
		if err != nil {
			return
		}
		defer connection.Close()
		if _, err := readHello(connection); err != nil {
			return
		}
		awaketest.ReadyThenHold()
		_ = writeHello(connection, hello{Version: Version})
	}()

	// Act
	client, _, err := dial(Record{Pipe: name, Token: "token", HostPID: os.Getpid()}, hello{Version: Version, Token: "token"})

	// Assert
	if err != nil {
		t.Fatalf("the client gave up across a pause of %s on a host that answered after it: %v", awaketest.Length, err)
	}
	_ = client.Close()
}

// A host waits for the hello of a client that connected across a pause, and
// serves it.
func TestAHostWaitsForAClientsHelloAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the client says hello after the pause, and asks for a
	// delivery, which needs no console until it types.
	name, pipes := hostPipe(t)
	shorten(t, &handshakeTimeout, time.Second)
	client, err := dialPipe(name, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	connection, err := pipes.accept()
	if err != nil {
		t.Fatal(err)
	}
	answered := make(chan error, 1)
	var answer hello
	go func() {
		defer client.Close()
		awaketest.ReadyThenHold()
		err := writeHello(client, hello{Version: Version, Token: "token", Deliver: true})
		if err == nil {
			answer, err = readHello(client)
		}
		answered <- err
	}()

	// Act
	serve(connection, "token", nil, nil, nil, nil)

	// Assert
	if err := <-answered; err != nil || !answer.Deliver {
		t.Fatalf("the host answered %+v, %v to a client that said hello after a pause of %s; want the delivery served", answer, err, awaketest.Length)
	}
}

// A screen read waits for the host's screen across a pause.
func TestAScreenReadWaitsForTheScreenAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the host sends the screen after the pause.
	name, pipes := hostPipe(t)
	shorten(t, &screenTimeout, 500*time.Millisecond)
	shorten(t, &handshakeTimeout, 500*time.Millisecond)
	payload, err := json.Marshal(screen{Rows: []string{"ready"}})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		connection, err := pipes.accept()
		if err != nil {
			return
		}
		defer connection.Close()
		if _, err := readHello(connection); err != nil || writeHello(connection, hello{Version: Version}) != nil {
			return
		}
		awaketest.ReadyThenHold()
		_ = writeFrame(connection, frameScreen, payload)
	}()

	// Act
	rows, err := ReadScreen(Record{ID: "g1", Pipe: name, Token: "token", HostPID: os.Getpid()})

	// Assert
	if err != nil || len(rows) != 1 || rows[0] != "ready" {
		t.Fatalf("ReadScreen = %q, %v across a pause of %s on a host that sent the screen after it; want the screen", rows, err, awaketest.Length)
	}
}
