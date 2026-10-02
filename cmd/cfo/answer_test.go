package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// --in names where the Overlord answered, which only a record of that answer
// takes: cfo answer without --record-only delivers the CFO's own choice.
func TestAnswerCommandTakesWhereTheOverlordAnsweredOnlyForARecord(t *testing.T) {
	// Arrange
	runtime := commandRuntime{resolveHome: func() (home.Home, error) {
		t.Fatal("a refused command resolved the home")
		return home.Home{}, nil
	}}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runAnswer([]string{"herdr-strays-20260929", "--option", "Stop them", "--in", "chat"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 2 || !strings.Contains(stderr.String(), "--in goes with --record-only") {
		t.Fatalf("exit=%d stderr=%q, want 2 naming --in goes with --record-only", exit, stderr.String())
	}
}
