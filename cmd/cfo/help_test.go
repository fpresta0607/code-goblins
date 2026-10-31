package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestHelpReadsOnlyTheRequestedContractWithoutOpeningAHome(t *testing.T) {
	for _, test := range []struct {
		name   string
		args   []string
		want   string
		absent string
	}{
		{"command", []string{"help", "spawn"}, "--overlap-ok", "## Project authentication"},
		{"subcommand", []string{"help", "auth", "request"}, "--wait-on", "## cfo auth store"},
		{"procedure", []string{"help", "contract", "supervision"}, "WAKE_ACK_REQUIRED", "## Model routing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			var stdout, stderr bytes.Buffer
			runtime := commandRuntime{resolveHome: func() (home.Home, error) {
				t.Fatal("read-only reference help tried to resolve the live home")
				return home.Home{}, nil
			}}

			// Act
			code := runWithRuntime(test.args, &stdout, &stderr, runtime)

			// Assert
			if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), test.want) || strings.Contains(stdout.String(), test.absent) {
				t.Fatalf("help %v: exit %d, stderr %q, output %q", test.args, code, stderr.String(), stdout.String())
			}
		})
	}
}

func TestHelpRejectsUnknownAndMalformedTopicsWithoutDumpingTheContract(t *testing.T) {
	for _, args := range [][]string{
		{"help", "not-a-command"}, {"help", "auth", "not-a-command"},
		{"help", "contract", "missing"}, {"help", "../AGENTS"},
		{"help", "spawn", "--yolo"}, {"help", "SPAWN"},
	} {
		// Arrange
		var stdout, stderr bytes.Buffer

		// Act
		code := runWithRuntime(args, &stdout, &stderr, commandRuntime{})

		// Assert
		if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown help topic") {
			t.Errorf("help %v: exit %d, stdout %q, stderr %q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestHelpListsTopicsWithoutLoadingTheirBodies(t *testing.T) {
	// Arrange
	var stdout, stderr bytes.Buffer

	// Act
	code := runWithRuntime([]string{"help"}, &stdout, &stderr, commandRuntime{})

	// Assert
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "contract supervision") || !strings.Contains(stdout.String(), "auth request") || strings.Contains(stdout.String(), "WAKE_ACK_REQUIRED") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestHelpListsOnlyTheRequestedCommandGroup(t *testing.T) {
	// Arrange
	var stdout, stderr bytes.Buffer

	// Act
	code := runWithRuntime([]string{"help", "pr"}, &stdout, &stderr, commandRuntime{})

	// Assert
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "pr merge") || strings.Contains(stdout.String(), "auth request") || strings.Contains(stdout.String(), "--verified") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestHelpReportsReferenceAndOutputFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		args   []string
		files  fs.FS
		output io.Writer
		want   string
	}{
		{"missing index", nil, fstest.MapFS{}, io.Discard, "list docs"},
		{"unreadable body", []string{"spawn"}, fstest.MapFS{"docs/commands/spawn.md": {Mode: fs.ModeDir}}, io.Discard, "read docs/commands/spawn"},
		{"body output", []string{"spawn"}, fstest.MapFS{"docs/commands/spawn.md": {Data: []byte("contract")}}, gateOutputWriter(func([]byte) (int, error) { return 0, errors.New("output closed") }), "write docs/commands/spawn"},
		{"index output", nil, fstest.MapFS{"docs/commands/spawn.md": {Data: []byte("contract")}}, gateOutputWriter(func([]byte) (int, error) { return 0, errors.New("output closed") }), "write topics"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			var stderr bytes.Buffer

			// Act
			code := runHelp(test.args, test.output, &stderr, test.files)

			// Assert
			if code != 1 || !strings.Contains(stderr.String(), test.want) {
				t.Errorf("exit %d, stderr %q; want reference failure naming %q", code, stderr.String(), test.want)
			}
		})
	}
}
