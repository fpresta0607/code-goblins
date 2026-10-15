package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A log past its limit is moved aside once, and every line said is kept in
// the log or the one before it.
func TestServiceLogMovesAsideAtItsLimit(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "ci-runner.log")
	log, err := openLog(path, 400)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	for i := range 12 {
		log.Say(fmt.Sprintf("line %02d of the service's log", i))
	}
	closeErr := log.Close()

	// Assert
	current, err := os.ReadFile(path)
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	previous, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("no log was moved aside: %v", err)
	}
	if len(current) > 400 || len(previous) > 400 {
		t.Errorf("the logs hold %d and %d bytes; want each within 400", len(current), len(previous))
	}
	if !strings.Contains(string(current), "line 11 ") || strings.Contains(string(previous), "line 11 ") {
		t.Errorf("the last line is not in the current log alone: %q, %q", current, previous)
	}
}

// A log opened again adds to what it holds, and counts it against its limit.
func TestServiceLogAddsToItselfWhenOpenedAgain(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "ci-runner.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 390)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	log, err := openLog(path, 400)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	log.Say("the service started")
	closeErr := log.Close()

	// Assert
	current, err := os.ReadFile(path)
	previous, previousErr := os.ReadFile(path + ".1")
	if err != nil || closeErr != nil || previousErr != nil || !strings.HasSuffix(string(current), " the service started\n") || len(previous) != 391 {
		t.Errorf("log %q, moved aside %d bytes (%v); want the old log moved aside whole and the new line in a new log", current, len(previous), previousErr)
	}
}
