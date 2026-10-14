package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/svc"
)

// A service whose settings cannot be read stops with an error of its own,
// and its log says why, before it serves anything.
func TestServiceThatCannotReadItsSettingsStopsAndSaysWhy(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 4)

	// Act
	isServiceError, code := handler{dir: dir}.Execute(nil, requests, changes)

	// Assert
	log, err := os.ReadFile(filepath.Join(dir, "ci-runner.log"))
	if !isServiceError || code != 2 || err != nil || !strings.Contains(string(log), "the service cannot start: cirunner: read the service's settings") {
		t.Errorf("Execute = %v, %d with log %q (%v); want service error 2 and why in the log", isServiceError, code, log, err)
	}
	close(changes)
	for change := range changes {
		if change.State == svc.Running {
			t.Error("the service said it runs; want it never to")
		}
	}
}
