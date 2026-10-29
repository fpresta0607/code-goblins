package host

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestStreamObjectCompactsOneObjectAndRefusesAnythingElse(t *testing.T) {
	for _, testCase := range []struct {
		line string
		want string
	}{
		{`{"type":"said"}`, `{"type":"said"}`},
		{" { \"type\" : \"said\", \"at\" : [1, 2] }\t", `{"type":"said","at":[1,2]}`},
		{"", ""},
		{"   ", ""},
		{"not json", ""},
		{"[1, 2]", ""},
		{`"text"`, ""},
		{`{"type":"said"`, ""},
		{`{"a":1}{"b":2}`, ""},
	} {
		t.Run(testCase.line, func(t *testing.T) {
			// Act
			object, err := streamObject([]byte(testCase.line))

			// Assert
			if testCase.want == "" {
				if err == nil || !strings.Contains(err.Error(), "one JSON object") {
					t.Errorf("streamObject = %s, %v; want a refusal naming one JSON object", object, err)
				}
				return
			}
			if err != nil || string(object) != testCase.want {
				t.Errorf("streamObject = %s, %v; want %s", object, err, testCase.want)
			}
		})
	}
}

// A line over the bound is read past whole, with its length, and the next
// line reads as itself; a line ends with or without its carriage return, and
// the last one with no end at all.
func TestReadStreamLineReadsPastALineOverTheBound(t *testing.T) {
	// Arrange
	long := strings.Repeat("x", maxStreamLine+1)
	output := bufio.NewReader(strings.NewReader("{\"a\":1}\r\n" + long + "\n{\"b\":2}\n{\"c\":3}"))

	// Act
	first, _, firstErr := readStreamLine(output)
	_, longLength, longErr := readStreamLine(output)
	second, _, secondErr := readStreamLine(output)
	last, _, lastErr := readStreamLine(output)
	_, _, endErr := readStreamLine(output)

	// Assert
	if string(first) != `{"a":1}` || firstErr != nil {
		t.Errorf("first line = %q, %v", first, firstErr)
	}
	if !errors.Is(longErr, errLineTooLong) || longLength != len(long)+1 {
		t.Errorf("long line = %d bytes, %v; want errLineTooLong at %d", longLength, longErr, len(long)+1)
	}
	if string(second) != `{"b":2}` || secondErr != nil {
		t.Errorf("line after the long one = %q, %v", second, secondErr)
	}
	if string(last) != `{"c":3}` || lastErr != nil {
		t.Errorf("last line = %q, %v", last, lastErr)
	}
	if !errors.Is(endErr, io.EOF) {
		t.Errorf("read past the end = %v, want EOF", endErr)
	}
}

// boundHistory sets maxStreamHistory to bound for the test.
func boundHistory(t *testing.T, bound int) {
	t.Helper()
	was := maxStreamHistory
	maxStreamHistory = bound
	t.Cleanup(func() { maxStreamHistory = was })
}

// numberedLine is a 10-byte line for numbers 1 to 9.
func numberedLine(number int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"line":%d}`, number))
}

// The history keeps its newest lines within its bound, numbered on from the
// first line it ever kept, for a viewer that comes later.
func TestALineHistoryKeepsItsNewestLinesWithinItsBound(t *testing.T) {
	// Arrange: the bound holds three lines.
	boundHistory(t, 30)
	lines := newLineHistory()

	// Act
	for number := 1; number <= 5; number++ {
		lines.add(numberedLine(number), number%2 == 0)
	}
	kept, _, detach := lines.attach()
	defer detach()

	// Assert
	var seqs []uint64
	for _, line := range kept {
		seqs = append(seqs, line.Seq)
	}
	if len(kept) != 3 || seqs[0] != 3 || seqs[2] != 5 || !bytes.Equal(kept[2].Line, numberedLine(5)) || !kept[1].IsWritten || kept[0].IsWritten {
		t.Errorf("kept %+v, want lines 3 to 5 as they were added", kept)
	}
}

// A viewer is fed each line once, then told there are no more once the
// program wrote its last; one that falls a whole bound behind is let go to
// come back, and the history goes on without it.
func TestALineFeedEndsWithTheProgramOrWhenItsViewerFallsBehind(t *testing.T) {
	t.Run("the program wrote its last line", func(t *testing.T) {
		// Arrange
		lines := newLineHistory()
		_, feed, detach := lines.attach()
		defer detach()
		lines.add(numberedLine(1), false)
		lines.add(numberedLine(2), true)

		// Act
		batch, isOpen := feed.take()
		lines.end()
		_, isOpenAfterEnd := feed.take()

		// Assert
		if !isOpen || len(batch) != 2 || batch[0].Seq != 1 || batch[1].Seq != 2 {
			t.Errorf("take = %+v, %v; want lines 1 and 2", batch, isOpen)
		}
		if isOpenAfterEnd || !lines.ended() {
			t.Error("the feed is still open after the program wrote its last line")
		}
	})
	t.Run("the viewer fell a whole bound behind", func(t *testing.T) {
		// Arrange: the bound holds three lines, and the viewer takes none.
		boundHistory(t, 30)
		lines := newLineHistory()
		_, behind, detachBehind := lines.attach()
		defer detachBehind()

		// Act
		for number := 1; number <= 4; number++ {
			lines.add(numberedLine(number), false)
		}
		_, isOpen := behind.take()
		_, later, detachLater := lines.attach()
		defer detachLater()
		lines.add(numberedLine(5), false)
		batch, isLaterOpen := later.take()

		// Assert
		if isOpen {
			t.Error("a viewer a whole bound behind is still fed")
		}
		if !isLaterOpen || len(batch) != 1 || batch[0].Seq != 5 {
			t.Errorf("the viewer that came back took %+v, %v; want line 5", batch, isLaterOpen)
		}
	})
}
