package browserlogin

import (
	"bytes"
	"fmt"
	"testing"
)

func TestAStartupLogKeepsOnlyTheLastFourKiBChromeWrote(t *testing.T) {
	var written bytes.Buffer
	for line := 0; written.Len() <= 3*4096; line++ {
		fmt.Fprintf(&written, "chrome log line %d\n", line)
	}
	log := &startupLog{}

	for _, chunk := range bytes.SplitAfter(written.Bytes(), []byte("\n")) {
		if count, err := log.Write(chunk); count != len(chunk) || err != nil {
			t.Fatalf("Write = %d, %v, want %d, nil", count, err, len(chunk))
		}
	}
	kept := log.end()

	if want := string(written.Bytes()[written.Len()-4096:]); kept != want {
		t.Fatalf("kept %d bytes starting %q, want the last 4096 starting %q", len(kept), kept[:min(len(kept), 32)], want[:32])
	}
}

func TestAStartupLogKeepsNothingWrittenAfterItsStartEnds(t *testing.T) {
	log := &startupLog{}
	if _, err := log.Write([]byte("while starting\n")); err != nil {
		t.Fatal(err)
	}
	if kept := log.end(); kept != "while starting\n" {
		t.Fatalf("first end = %q, want what was written while starting", kept)
	}

	later := []byte("a page's console output\n")
	count, err := log.Write(later)
	kept := log.end()

	if count != len(later) || err != nil || kept != "" {
		t.Fatalf("Write after end = %d, %v, then end = %q, want %d, nil, then nothing", count, err, kept, len(later))
	}
}
