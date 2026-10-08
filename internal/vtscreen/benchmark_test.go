package vtscreen

import (
	"strings"
	"testing"
)

// BenchmarkWrite measures how fast a screen takes a program's output: lines
// of styled text that scroll, as a harness prints a long answer.
func BenchmarkWrite(b *testing.B) {
	line := "\x1b[38;2;215;119;87m⏺\x1b[39m " + strings.Repeat("the quick brown fox jumps ", 4) + "\x1b[1mdone\x1b[22m\r\n"
	output := []byte(strings.Repeat(line, 1000))
	s, err := New(120, 40)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(output)))
	for b.Loop() {
		s.Write(output)
	}
}
