package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectionRepairCLIRefusesWhatItCannotRepairBeforeLogin(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBLINS_HOME", directory)
	t.Setenv("GOBLINS_STATE_OVERRIDE", filepath.Join(directory, "state"))
	project := filepath.Join(directory, "acme")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		args    []string
		code    int
		message string
	}{
		{"no arguments", nil, 2, "usage:"},
		{"the earlier task form", []string{"acme", "current", "service:repo", "cli"}, 2, "usage:"},
		{"project folder that is not there", []string{filepath.Join(directory, "globex"), "service:repo", "cli"}, 1, "Project folder was not found."},
		{"connection the project does not offer", []string{project, "service:repo", "unoffered"}, 1, "Refresh this connection"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			var stdout, stderr strings.Builder

			// Act
			code := runConnectionRepair(test.args, &stdout, &stderr)

			// Assert
			if code != test.code || !strings.Contains(stderr.String(), test.message) || stdout.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}
