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

// A menu is drawn only while the screen is still bound to input he submitted.
// The host keeps that binding through a delivery of the message he queued,
// since that message is his too, and clears it once another writer, a
// watcher's wake line, types into the terminal.
func TestHisOwnDeliveryKeepsTheMenuHisSubmissionOpened(t *testing.T) {
	// Arrange
	_, record := launch(t)
	his := view(t, record)
	his.waitFor(t, "ready")
	if err := his.MarkHis(); err != nil {
		t.Fatal(err)
	}
	if err := his.MarkSubmission("menu", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := his.Input([]byte("his words\r")); err != nil {
		t.Fatal(err)
	}
	his.waitFor(t, "got his words")
	if _, submission, _, err := his.Screen(record, "menu"); err != nil || submission != "menu" {
		t.Fatalf("submission = %q, %v after his own keys; want his mark to stand", submission, err)
	}

	// Act: the board delivers the message he queued.
	delivery, err := DialDelivery(record)
	if err != nil {
		t.Fatal(err)
	}
	defer delivery.Close()
	if err := delivery.MarkHis(); err != nil {
		t.Fatal(err)
	}
	if err := delivery.Write([]byte("board words\r")); err != nil {
		t.Fatal(err)
	}
	his.waitFor(t, "got board words")

	// Assert: the delivery is his, so the menu his submission opened stands.
	if _, submission, _, err := his.Screen(record, "menu"); err != nil || submission != "menu" {
		t.Fatalf("submission = %q, %v after his own delivery; want the menu still his", submission, err)
	}

	// Act: a watcher types a wake line into the terminal.
	wake := connect(t, record)
	if err := wake.Input([]byte("wake line\r")); err != nil {
		t.Fatal(err)
	}
	wake.waitFor(t, "got wake line")

	// Assert: the screen is no longer bound to his submission.
	if _, submission, _, err := his.Screen(record, "menu"); err != nil || submission != "" {
		t.Fatalf("submission = %q, %v after a watcher typed; want no menu of his", submission, err)
	}
}

// A delivery of the message he queued leaves a terminal-wide mark even for a
// view that submitted nothing through the board, and a delivery that is
// nobody's own input clears it, so a picker a notice drew is never his.
func TestADeliveryHeAskedForLeavesHisMarkForAViewThatSubmittedNothing(t *testing.T) {
	// Arrange
	_, record := launch(t)
	his := view(t, record)
	his.waitFor(t, "ready")
	if _, submission, _, err := his.Screen(record, ""); err != nil || submission != "" {
		t.Fatalf("submission = %q, %v before anything typed; want no mark", submission, err)
	}

	// Act: the board delivers the message he queued.
	delivery, err := DialDelivery(record)
	if err != nil {
		t.Fatal(err)
	}
	defer delivery.Close()
	if err := delivery.MarkHis(); err != nil {
		t.Fatal(err)
	}
	if err := delivery.Write([]byte("his words\r")); err != nil {
		t.Fatal(err)
	}
	his.waitFor(t, "got his words")

	// Assert: the screen is his even though this view submitted nothing.
	if _, submission, _, err := his.Screen(record, ""); err != nil || submission == "" {
		t.Fatalf("submission = %q, %v after his delivery; want the screen bound to him", submission, err)
	}

	// Act: a delivery nobody says is his types a notice.
	notice, err := DialDelivery(record)
	if err != nil {
		t.Fatal(err)
	}
	defer notice.Close()
	if err := notice.Write([]byte("a notice\r")); err != nil {
		t.Fatal(err)
	}
	his.waitFor(t, "got a notice")

	// Assert: the notice is nobody's submission.
	if _, submission, _, err := his.Screen(record, ""); err != nil || submission != "" {
		t.Fatalf("submission = %q, %v after a delivery that is not his; want no mark", submission, err)
	}
}

// A host started by an older ceo types input without confirming it, so a
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
