package host

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// streamChild speaks one JSON object a line both ways, as Claude Code does
// with stream-json: it says it is ready, answers each line it reads, and says
// goodbye and exits at the end of its input, unless told to linger past it.
func streamChild() {
	emit := func(fields map[string]any) {
		data, _ := json.Marshal(fields)
		fmt.Println(string(data))
	}
	emit(map[string]any{"type": "ready"})
	isLingering := false
	lines := bufio.NewScanner(os.Stdin)
	for lines.Scan() {
		var request struct {
			Say    string `json:"say"`
			Ask    string `json:"ask"`
			Exit   *int   `json:"exit"`
			Garble bool   `json:"garble"`
			Linger bool   `json:"linger"`
		}
		if err := json.Unmarshal(lines.Bytes(), &request); err != nil {
			emit(map[string]any{"type": "unreadable", "error": err.Error()})
			continue
		}
		switch {
		case request.Exit != nil:
			os.Exit(*request.Exit)
		case request.Ask == "host-id":
			emit(map[string]any{"type": "host-id", "id": os.Getenv(IDVariable)})
		case request.Ask == "host-proof":
			sum := sha256.Sum256([]byte(os.Getenv(ProofVariable)))
			emit(map[string]any{"type": "host-proof", "sum": hex.EncodeToString(sum[:])})
		case request.Garble:
			// A line that is no JSON object, a blank line and an array, then
			// an object written with spaces in it.
			fmt.Println("not json")
			fmt.Println()
			fmt.Println("[1, 2]")
			fmt.Println(`{ "type" : "after-garble" }`)
		case request.Linger:
			isLingering = true
			emit(map[string]any{"type": "lingering"})
		default:
			emit(map[string]any{"type": "said", "text": request.Say})
		}
	}
	if isLingering {
		time.Sleep(time.Minute)
	}
	emit(map[string]any{"type": "bye"})
}

// launchStream starts a host running stream-child for the test and ends it,
// by pid, when the test does.
func launchStream(t *testing.T) (string, Record) {
	t.Helper()
	return launchStreamOf(t, "stream-child")
}

// launchStreamOf starts a host running this test binary with role and args
// as a stream session, and ends it, by pid, when the test does.
func launchStreamOf(t *testing.T, role string, args ...string) (string, Record) {
	t.Helper()
	stateDir := t.TempDir()
	record, err := Launch(stateDir, []string{os.Args[0]}, hostEnvironment(), Spec{ID: "s1", Args: append([]string{os.Args[0], role}, args...), IsStream: true})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	pin(t, record.HostPID)
	t.Cleanup(func() { end(record.HostPID) })
	if !record.Stream || record.ChildPID == record.HostPID || !running(record.ChildPID) {
		t.Fatalf("record %+v does not name a running stream session", record)
	}
	pin(t, record.ChildPID)
	return stateDir, record
}

// streamWatch is a viewer of a stream session and every event it has read.
type streamWatch struct {
	*StreamViewer
	events <-chan StreamEvent
	seen   []StreamEvent
}

// watchStream connects a viewer and reads its events as they come, until the
// test ends.
func watchStream(t *testing.T, record Record) *streamWatch {
	t.Helper()
	viewer, err := WatchStream(record)
	if err != nil {
		t.Fatalf("WatchStream: %v", err)
	}
	events := make(chan StreamEvent)
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		_ = viewer.Close()
	})
	go func() {
		defer close(events)
		for {
			event, err := viewer.Next()
			if err != nil {
				return
			}
			select {
			case events <- event:
			case <-done:
				return
			}
		}
	}()
	return &streamWatch{StreamViewer: viewer, events: events}
}

// lineType is the type a session's line names.
func lineType(line json.RawMessage) string {
	var fields struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(line, &fields)
	return fields.Type
}

// waitFor reads until the program writes a line of type kind and returns it.
func (w *streamWatch) waitFor(t *testing.T, kind string) StreamLine {
	t.Helper()
	until := time.After(time.Minute)
	for {
		select {
		case event, open := <-w.events:
			if !open {
				t.Fatalf("the viewer ended before a %q line; it read %+v", kind, w.seen)
			}
			w.seen = append(w.seen, event)
			if event.IsExited {
				t.Fatalf("the program exited with %d before a %q line", event.Code, kind)
			}
			if !event.Line.IsWritten && lineType(event.Line.Line) == kind {
				return event.Line
			}
		case <-until:
			t.Fatalf("no %q line within a minute; the viewer read %+v", kind, w.seen)
		}
	}
}

// waitForExit reads until the program's end and returns its exit code.
func (w *streamWatch) waitForExit(t *testing.T) uint32 {
	t.Helper()
	until := time.After(time.Minute)
	for {
		select {
		case event, open := <-w.events:
			if !open {
				t.Fatalf("the viewer ended before the program did; it read %+v", w.seen)
			}
			w.seen = append(w.seen, event)
			if event.IsExited {
				return event.Code
			}
		case <-until:
			t.Fatalf("the program did not end within a minute; the viewer read %+v", w.seen)
		}
	}
}

// lines is every line the viewer has read, in the order it read them.
func (w *streamWatch) lines() []StreamLine {
	var lines []StreamLine
	for _, event := range w.seen {
		if !event.IsExited {
			lines = append(lines, event.Line)
		}
	}
	return lines
}

func dialWriter(t *testing.T, record Record) *StreamWriter {
	t.Helper()
	writer, err := DialStreamWriter(record)
	if err != nil {
		t.Fatalf("DialStreamWriter: %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	return writer
}

// A line written to a stream session reaches its program, and a viewer reads
// it there, marked written, then the program's answer, each numbered one
// after the line before.
func TestAStreamSessionCarriesWrittenLinesInAndItsProgramsLinesOut(t *testing.T) {
	// Arrange
	_, record := launchStream(t)
	watch := watchStream(t, record)
	watch.waitFor(t, "ready")
	writer := dialWriter(t, record)

	// Act
	err := writer.Write([]byte(`{"say":"hello"}`))

	// Assert
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	said := watch.waitFor(t, "said")
	if !strings.Contains(string(said.Line), `"text":"hello"`) {
		t.Errorf("the program said %s, want hello", said.Line)
	}
	lines := watch.lines()
	written := slices.IndexFunc(lines, func(line StreamLine) bool { return line.IsWritten })
	if written < 0 || string(lines[written].Line) != `{"say":"hello"}` || written > slices.IndexFunc(lines, func(line StreamLine) bool { return line.Seq == said.Seq }) {
		t.Errorf("the viewer read %+v, want the written line before the answer", lines)
	}
	for i, line := range lines {
		if line.Seq != uint64(i+1) {
			t.Errorf("line %d is numbered %d, want %d", i, line.Seq, i+1)
		}
	}
}

// A viewer that comes later reads the whole session so far, numbered as the
// first viewer read it.
func TestAStreamSessionReplaysItsLinesToAViewerThatComesLater(t *testing.T) {
	// Arrange
	_, record := launchStream(t)
	first := watchStream(t, record)
	writer := dialWriter(t, record)
	for _, word := range []string{"one", "two"} {
		if err := writer.Write([]byte(`{"say":"` + word + `"}`)); err != nil {
			t.Fatalf("Write %s: %v", word, err)
		}
	}
	for len(first.lines()) < 5 {
		first.waitFor(t, "said")
	}
	_ = first.Close()

	// Act
	later := watchStream(t, record)
	for len(later.lines()) < 5 {
		later.waitFor(t, "said")
	}

	// Assert
	if got, want := later.lines(), first.lines(); !slices.EqualFunc(got, want, func(a, b StreamLine) bool {
		return a.Seq == b.Seq && a.IsWritten == b.IsWritten && string(a.Line) == string(b.Line)
	}) {
		t.Errorf("the later viewer read %+v, want %+v", got, want)
	}
}

// A line that is not one JSON object is refused with why, and never reaches
// the program; a line written with spaces reaches it compact.
func TestAStreamWriterIsToldWhyALineWasNotWritten(t *testing.T) {
	// Arrange
	_, record := launchStream(t)
	watch := watchStream(t, record)
	watch.waitFor(t, "ready")
	writer := dialWriter(t, record)

	// Act
	var refusals []error
	for _, line := range []string{"hello", "[1]", `{"say":"a"}{"say":"b"}`, "", "  "} {
		refusals = append(refusals, writer.Write([]byte(line)))
	}
	acceptedErr := writer.Write([]byte(` { "say" : "spaced" } `))

	// Assert
	for i, err := range refusals {
		if err == nil || !strings.Contains(err.Error(), "one JSON object") {
			t.Errorf("refusal %d = %v, want one naming a JSON object", i, err)
		}
	}
	if acceptedErr != nil {
		t.Fatalf("Write of a spaced object: %v", acceptedErr)
	}
	watch.waitFor(t, "said")
	var written []string
	for _, line := range watch.lines() {
		if line.IsWritten {
			written = append(written, string(line.Line))
		}
	}
	if !slices.Equal(written, []string{`{"say":"spaced"}`}) {
		t.Errorf("the session holds the written lines %q, want only the spaced object, compact", written)
	}
}

// What the program writes that is not one JSON object is left out of the
// session, and the lines around it keep their numbers in a row.
func TestAStreamSessionLeavesOutWhatIsNotAJSONObject(t *testing.T) {
	// Arrange
	_, record := launchStream(t)
	watch := watchStream(t, record)
	watch.waitFor(t, "ready")
	writer := dialWriter(t, record)

	// Act
	if err := writer.Write([]byte(`{"garble":true}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	after := watch.waitFor(t, "after-garble")

	// Assert
	if string(after.Line) != `{"type":"after-garble"}` {
		t.Errorf("the program's object reads %s, want it compact", after.Line)
	}
	lines := watch.lines()
	if len(lines) != 3 || lines[1].Seq != 2 || after.Seq != 3 {
		t.Errorf("the viewer read %+v, want ready, the written line and the object after the garble, numbered 1 to 3", lines)
	}
}

// The terminal the host announces for and the proof it hands its program
// reach a stream session's program, as they reach a terminal's.
func TestAStreamSessionsProgramCarriesItsHostsIDAndProof(t *testing.T) {
	// Arrange
	_, record := launchStream(t)
	watch := watchStream(t, record)
	writer := dialWriter(t, record)

	// Act
	for _, ask := range []string{"host-id", "host-proof"} {
		if err := writer.Write([]byte(`{"ask":"` + ask + `"}`)); err != nil {
			t.Fatalf("Write %s: %v", ask, err)
		}
	}
	id := watch.waitFor(t, "host-id")
	proof := watch.waitFor(t, "host-proof")

	// Assert
	if !strings.Contains(string(id.Line), `"id":"s1"`) {
		t.Errorf("the program reads host id %s, want s1", id.Line)
	}
	var sum struct {
		Sum string `json:"sum"`
	}
	if err := json.Unmarshal(proof.Line, &sum); err != nil || sum.Sum != record.ProofSum {
		t.Errorf("the program's proof digests to %q, want the record's %q", sum.Sum, record.ProofSum)
	}
}

// A stream host refuses a terminal's viewer, and a terminal's host refuses a
// stream session's viewer and writer, each saying why.
func TestEachKindOfHostRefusesTheOtherKindsClient(t *testing.T) {
	_, stream := launchStream(t)
	_, terminal := launch(t)
	for _, testCase := range []struct {
		name    string
		connect func() error
		want    string
	}{
		{"a terminal's viewer of a stream host", func() error { _, err := Dial(stream); return err }, "runs a stream session, not a terminal"},
		{"a stream viewer of a terminal's host", func() error { _, err := WatchStream(terminal); return err }, "runs a terminal, not a stream session"},
		{"a stream writer of a terminal's host", func() error { _, err := DialStreamWriter(terminal); return err }, "runs a terminal, not a stream session"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Act
			err := testCase.connect()

			// Assert
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("connect = %v, want a refusal saying it %s", err, testCase.want)
			}
		})
	}
}

// A stream session ends with its program: by an exit of its own, with its
// code, or closed by a client, which closes the program's input and lets it
// end by itself, or ends it once it lingers past streamCloseWait. The host
// records how, and every viewer reads the end.
func TestAStreamSessionEndsWithItsProgramAndRecordsHow(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		end        func(t *testing.T, writer *StreamWriter)
		wantCode   uint32
		wantClosed bool
		wantBye    bool
	}{
		{name: "an exit of its own", end: func(t *testing.T, writer *StreamWriter) { writeLine(t, writer, `{"exit":7}`) }, wantCode: 7},
		{name: "closed, ending by itself", end: func(t *testing.T, writer *StreamWriter) { closeSession(t, writer) }, wantClosed: true, wantBye: true},
		{name: "closed, lingering past its input", end: func(t *testing.T, writer *StreamWriter) {
			writeLine(t, writer, `{"linger":true}`)
			closeSession(t, writer)
		}, wantClosed: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			stateDir, record := launchStream(t)
			watch := watchStream(t, record)
			watch.waitFor(t, "ready")
			writer := dialWriter(t, record)

			// Act
			testCase.end(t, writer)
			code := watch.waitForExit(t)

			// Assert
			if !exited(record.HostPID) {
				t.Fatal("the host is still running after its session ended")
			}
			if !testCase.wantClosed && code != testCase.wantCode {
				t.Errorf("the viewer read exit code %d, want %d", code, testCase.wantCode)
			}
			isBye := slices.ContainsFunc(watch.lines(), func(line StreamLine) bool { return lineType(line.Line) == "bye" })
			if isBye != testCase.wantBye {
				t.Errorf("the program said goodbye: %v, want %v", isBye, testCase.wantBye)
			}
			ended, err := ReadEnd(stateDir, "s1")
			if err != nil {
				t.Fatalf("ReadEnd: %v", err)
			}
			if ended.HostPID != record.HostPID || ended.ChildPID != record.ChildPID || ended.Closed != testCase.wantClosed || !testCase.wantClosed && ended.Code != testCase.wantCode {
				t.Errorf("end = %+v, want host %d, program %d, closed %v, code %d", ended, record.HostPID, record.ChildPID, testCase.wantClosed, testCase.wantCode)
			}
			if _, err := ReadRecord(stateDir, "s1"); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the record is still there: %v", err)
			}
		})
	}
}

// Close ends a stream session as it ends a terminal, through the stream
// host's own kind of connection.
func TestCloseEndsAStreamSession(t *testing.T) {
	// Arrange
	stateDir, record := launchStream(t)
	watch := watchStream(t, record)
	watch.waitFor(t, "ready")

	// Act
	err := Close(stateDir, record, time.Minute)

	// Assert
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !exited(record.HostPID) {
		t.Fatal("the host is still running after Close")
	}
	if ended, err := ReadEnd(stateDir, "s1"); err != nil || !ended.Closed {
		t.Errorf("end = %+v, %v, want one closed by a client", ended, err)
	}
}

// A host is run with --stream for a stream session and without it for a
// terminal.
func TestArgumentsAskForAStreamSessionOnlyWhenTheSpecIsOne(t *testing.T) {
	for _, isStream := range []bool{false, true} {
		t.Run(fmt.Sprint("stream ", isStream), func(t *testing.T) {
			// Act
			args := Arguments(`C:\state`, Spec{ID: "s1", Args: []string{"claude", "--stream"}, IsStream: isStream})

			// Assert
			beforeCommand := args[:slices.Index(args, "--")]
			if slices.Contains(beforeCommand, "--stream") != isStream {
				t.Errorf("Arguments = %q, want --stream before the command only for a stream session", args)
			}
		})
	}
}

func writeLine(t *testing.T, writer *StreamWriter, line string) {
	t.Helper()
	if err := writer.Write([]byte(line)); err != nil {
		t.Fatalf("Write %s: %v", line, err)
	}
}

func closeSession(t *testing.T, writer *StreamWriter) {
	t.Helper()
	if err := writer.CloseSession(); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
}
