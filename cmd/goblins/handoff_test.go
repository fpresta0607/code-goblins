package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestGoblinsHandoffRefusesAMissingOrInvalidTaskBeforeItReadsTheHome(t *testing.T) {
	for _, args := range [][]string{{"handoff"}, {"handoff", "Not A Task"}, {"handoff", "cancel"}, {"handoff", "cancel", "../outside"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// Arrange
			isHomeRead := false
			runtime := commandRuntime{resolveHome: func() (home.Home, error) {
				isHomeRead = true
				return home.Home{}, nil
			}}
			var output, failure bytes.Buffer

			// Act
			code := runWithRuntime(args, &output, &failure, runtime)

			// Assert
			if code != 2 || isHomeRead || !strings.Contains(failure.String(), handoffUsage) {
				t.Fatalf("code=%d home read=%v error=%q, want the usage", code, isHomeRead, failure.String())
			}
		})
	}
}

func TestGoblinsHandoffRefusesAHomeThatIsNotPrimary(t *testing.T) {
	// Arrange
	h := home.Home{Root: t.TempDir(), State: t.TempDir()}
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}
	var output, failure bytes.Buffer

	// Act
	code := runWithRuntime([]string{"handoff", "task"}, &output, &failure, runtime)

	// Assert
	if code != 1 || !strings.Contains(failure.String(), "primary home") {
		t.Fatalf("code=%d error=%q, want a primary home required", code, failure.String())
	}
}
