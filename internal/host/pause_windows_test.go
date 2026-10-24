package host

import (
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/awake/awaketest"
)

// A machine that pauses stops the host with its client, and the wall clock
// runs on through the pause. A wait between them held to the wall clock found
// its deadline passed once the machine ran again, though the side it waited
// on had had no time to act (#1180, #1247). Each test shortens its wait under
// the pause's length, and the other side acts only once the machine runs
// again.

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

// A delivery that typed a part waits for the host's confirmation across a
// pause.
func TestADeliveryWaitsForTheHostsConfirmationAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the host confirms the part after the pause.
	name, pipes := hostPipe(t)
	shorten(t, &deliveryTimeout, time.Second)
	go func() {
		connection, err := pipes.accept()
		if err != nil {
			return
		}
		defer connection.Close()
		if _, err := readHello(connection); err != nil || writeHello(connection, hello{Version: Version, Deliver: true}) != nil {
			return
		}
		if _, _, err := readFrame(connection); err != nil {
			return
		}
		awaketest.ReadyThenHold()
		_ = writeFrame(connection, frameAck, nil)
	}()
	delivery, err := DialDelivery(Record{Pipe: name, Token: "token", HostPID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	defer delivery.Close()

	// Act
	err = delivery.Write([]byte("typed"))

	// Assert
	if err != nil {
		t.Fatalf("the delivery gave up across a pause of %s on a host that confirmed the part after it: %v", awaketest.Length, err)
	}
}

// An ending host waits across a pause for a viewer to hear how its terminal
// ended, since the pause stopped the viewer too.
func TestAnEndingHostWaitsForItsViewersAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: a viewer connects while the terminal runs and says hello only
	// after the pause, which falls once the terminal has ended.
	shorten(t, &farewellTimeout, time.Second)
	stateDir := t.TempDir()
	ran := make(chan error, 1)
	go func() {
		ran <- Run(stateDir, Spec{ID: "g1", Args: []string{os.Args[0], "echo-child"}, Cols: 80, Rows: 25})
	}()
	var record Record
	for tries := 0; ; tries++ {
		var err error
		if record, err = ReadRecord(stateDir, "g1"); err == nil {
			break
		}
		if tries == 400 {
			t.Fatalf("the host did not record itself within 10 s: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	late, err := dialPipe(record.Pipe, record.HostPID)
	if err != nil {
		t.Fatal(err)
	}
	defer late.Close()
	closer, err := Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	var isGreeted atomic.Bool
	go func() {
		for tries := 0; tries < 400; tries++ {
			if _, err := ReadEnd(stateDir, "g1"); err == nil {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		awaketest.ReadyThenHold()
		isGreeted.Store(true)
		_ = writeHello(late, hello{Version: Version, Token: record.Token})
	}()

	// Act
	if err := closer.CloseTerminal(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(time.Minute):
		t.Fatal("the host did not end within a minute of its terminal's close")
	}

	// Assert
	if !isGreeted.Load() {
		t.Errorf("the host ended across a pause of %s before a viewer the pause stopped said hello, so the viewer never heard how its terminal ended", awaketest.Length)
	}
}

// endWhenToldToClose plays a host that serves one client from pipes: it
// answers the client's hello and, once told to close, runs beforeEnd and
// ends, which removes launched's record.
func endWhenToldToClose(stateDir string, launched Record, pipes *listener, beforeEnd func()) {
	connection, err := pipes.accept()
	if err != nil {
		return
	}
	defer connection.Close()
	if _, err := readHello(connection); err != nil || writeHello(connection, hello{Version: Version}) != nil {
		return
	}
	if kind, _, err := readFrame(connection); err != nil || kind != frameClose {
		return
	}
	beforeEnd()
	removeRecord(stateDir, launched.ID, launched.HostPID)
}

// A close dials again across a pause a host that does not answer yet, since
// the pause stopped the host too.
func TestACloseDialsAgainAHostThatAnswersAfterAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the host opens its pipe only after the pause, and ends once
	// told to close.
	stateDir := t.TempDir()
	name, err := pipeName()
	if err != nil {
		t.Fatal(err)
	}
	launched := Record{ID: "g1", Pipe: name, Token: "token", HostPID: os.Getpid(), Started: time.Now()}
	if err := writeRecord(stateDir, launched); err != nil {
		t.Fatal(err)
	}
	go func() {
		awaketest.ReadyThenHold()
		pipes, err := listen(name)
		if err != nil {
			return
		}
		defer windows.CloseHandle(pipes.waiting)
		endWhenToldToClose(stateDir, launched, pipes, func() {})
	}()

	// Act
	err = Close(stateDir, launched, time.Second)

	// Assert
	if err != nil {
		t.Fatalf("the close gave up across a pause of %s on a host that answered after it: %v", awaketest.Length, err)
	}
}

// A close waits across a pause for the host it told to close to end, since
// the pause stopped the host too.
func TestACloseWaitsForItsHostToEndAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the host takes the close request and ends only after the
	// pause.
	stateDir := t.TempDir()
	name, pipes := hostPipe(t)
	shorten(t, &closeEndWait, time.Second)
	launched := Record{ID: "g1", Pipe: name, Token: "token", HostPID: os.Getpid(), Started: time.Now()}
	if err := writeRecord(stateDir, launched); err != nil {
		t.Fatal(err)
	}
	go endWhenToldToClose(stateDir, launched, pipes, awaketest.ReadyThenHold)

	// Act
	err := Close(stateDir, launched, time.Minute)

	// Assert
	if err != nil {
		t.Fatalf("the close gave up across a pause of %s on a host that ended after it: %v", awaketest.Length, err)
	}
}

// A launch waits across a pause for its host to record itself, since the
// pause stopped the host too.
func TestALaunchWaitsForItsHostToRecordItselfAcrossAPauseOfTheWholeMachine(t *testing.T) {
	if !awaketest.IsPausedProcess() {
		awaketest.Across(t)
		return
	}
	// Arrange: the launch finds the host's record only after the pause, as
	// when the pause stopped the host before it recorded itself, and the
	// pause falls once the launch has begun to look.
	shorten(t, &launchTimeout, time.Second)
	usual := readLaunchRecord
	var isRecorded atomic.Bool
	var looking sync.Once
	readLaunchRecord = func(stateDir, id string) (Record, error) {
		looking.Do(func() {
			go func() {
				awaketest.ReadyThenHold()
				isRecorded.Store(true)
			}()
		})
		if !isRecorded.Load() {
			return Record{}, os.ErrNotExist
		}
		return usual(stateDir, id)
	}
	t.Cleanup(func() { readLaunchRecord = usual })
	stateDir := t.TempDir()

	// Act
	record, err := Launch(stateDir, []string{os.Args[0]}, hostEnvironment(), Spec{ID: "g1", Args: []string{os.Args[0], "echo-child"}, Cols: 80, Rows: 25})

	// Assert
	if err != nil {
		t.Fatalf("the launch gave up across a pause of %s on a host that recorded itself after it: %v", awaketest.Length, err)
	}
	pin(t, record.HostPID)
	t.Cleanup(func() { end(record.HostPID) })
}
