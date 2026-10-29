package host

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/claudestream"
)

// recordedLine is one line of a recorded Claude Code session: one it wrote
// ("out"), or one written to it ("in").
type recordedLine struct {
	Direction string          `json:"dir"`
	Line      json.RawMessage `json:"line"`
}

func readRecording(path string) ([]recordedLine, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []recordedLine
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var line recordedLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	return lines, scanner.Err()
}

// streamReplay plays the Claude Code session recorded at path back as Claude
// Code played it: it writes each line Claude Code wrote, and waits for each
// line written to it, ending with code 3 at one that differs.
func streamReplay(path string) {
	lines, err := readRecording(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	input := bufio.NewScanner(os.Stdin)
	input.Buffer(nil, 1<<20)
	for _, line := range lines {
		if line.Direction == "out" {
			fmt.Println(string(line.Line))
			continue
		}
		if !input.Scan() || !bytes.Equal(input.Bytes(), line.Line) {
			fmt.Fprintf(os.Stderr, "replay: read %q, want %s\n", input.Bytes(), line.Line)
			os.Exit(3)
		}
	}
}

// Claude Code 2.1.312's recorded session, played back through a stream host:
// a viewer reads every line in the recording's order, the lines the board
// writes reach the program as they were written, and the session reads as the
// recording does.
func TestARecordedClaudeCodeSessionPlaysBackThroughAStreamHost(t *testing.T) {
	// Arrange
	path, err := filepath.Abs(filepath.Join("..", "claudestream", "testdata", "spike.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := readRecording(path)
	if err != nil {
		t.Fatalf("read the recording: %v", err)
	}
	_, record := launchStreamOf(t, "stream-replay", path)
	watch := watchStream(t, record)
	writer := dialWriter(t, record)
	programLines := 0
	readProgramLines := func(count int) {
		t.Helper()
		for programLines < count {
			select {
			case event, open := <-watch.events:
				if !open || event.IsExited {
					t.Fatalf("the session ended after %d of the program's lines, want %d; the viewer read %+v", programLines, count, watch.lines())
				}
				watch.seen = append(watch.seen, event)
				if !event.Line.IsWritten {
					programLines++
				}
			case <-time.After(time.Minute):
				t.Fatalf("the program wrote %d lines within a minute, want %d", programLines, count)
			}
		}
	}

	// Act: each line the board wrote goes in once the viewer read every line
	// the program wrote before it.
	written := 0
	for _, line := range recorded {
		if line.Direction == "out" {
			written++
			continue
		}
		readProgramLines(written)
		if err := writer.Write(line.Line); err != nil {
			t.Fatalf("Write %s: %v", line.Line, err)
		}
	}
	readProgramLines(written)
	code := watch.waitForExit(t)

	// Assert
	if code != 0 {
		t.Errorf("the replay exited with %d, want 0", code)
	}
	read := watch.lines()
	if len(read) != len(recorded) {
		t.Fatalf("the viewer read %d lines, want the recording's %d", len(read), len(recorded))
	}
	var session claudestream.Session
	for i, line := range read {
		if line.IsWritten != (recorded[i].Direction == "in") || !bytes.Equal(line.Line, recorded[i].Line) {
			t.Fatalf("line %d reads written %v %s, want %s %s", i, line.IsWritten, line.Line, recorded[i].Direction, recorded[i].Line)
		}
		if err := session.Apply(line.Line, line.IsWritten); err != nil {
			t.Fatalf("Apply line %d: %v", i, err)
		}
	}
	if session.IsWorking || len(session.Asks) != 0 || len(session.Unknown) != 0 || session.Result == nil || session.Result.Subtype != "error_during_execution" {
		t.Errorf("the session read as %+v, want the interrupted last turn ended", session)
	}
}
