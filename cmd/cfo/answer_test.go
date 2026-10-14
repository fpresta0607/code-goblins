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

// cfo answer <task-id> "<option>" exited 2 printing nothing and recording
// nothing, so it read as an answer given while the goblin's question stayed
// open (#851). Every argument shape cfo answer cannot act on is refused
// before the home is read, saying why, with its usage.
func TestAnswerCommandRefusesAShapeItCannotActOnWithItsUsage(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		why  string
	}{
		{"a task id and a bare option", []string{"cg-trains-health-answer", "Hold mine"}, `unexpected argument "Hold mine"`},
		{"a wake sequence and a bare option", []string{"1002017", "Hold mine"}, `unexpected argument "Hold mine"`},
		{"an unknown id and a bare option", []string{"4242", "Hold mine", "--note", "go"}, `unexpected argument "Hold mine"`},
		{"a task id with its option", []string{"cg-trains-health-answer", "--option", "Hold mine"}, "cg-trains-health-answer names neither a goblin question nor its notify's wake sequence"},
		{"no id", []string{"--option", "Hold mine"}, "a question ID or a goblin notify's wake sequence is required"},
		{"no option", []string{"1002017"}, "--option is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			runtime := commandRuntime{resolveHome: func() (home.Home, error) {
				t.Fatal("a refused command resolved the home")
				return home.Home{}, nil
			}}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runAnswer(test.args, &stdout, &stderr, runtime)

			// Assert
			if exit != 2 || !strings.Contains(stderr.String(), test.why) || !strings.Contains(stderr.String(), "usage: cfo answer <question-id|wake-sequence> --option <choice>") {
				t.Fatalf("exit=%d stderr=%q, want 2 saying %q with the usage", exit, stderr.String(), test.why)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing: only a recorded answer prints", stdout.String())
			}
		})
	}
}
