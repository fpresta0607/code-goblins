package execx_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Every process a fleet program starts through Command or CommandContext is
// counted by its program and the code that started it.
func TestStartsCountsEachStartByProgramAndCaller(t *testing.T) {
	// Arrange
	program := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	before := startsFrom(program, "execx_test/starts_test.go")

	// Act
	for range 2 {
		if err := execx.Command(os.Args[0], "-test.run=^$").Run(); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	if got := startsFrom(program, "execx_test/starts_test.go") - before; got != 2 {
		t.Fatalf("Starts counted %d starts of %s from this test, want 2: %+v", got, program, execx.Starts())
	}
}

func startsFrom(program, caller string) int {
	count := 0
	for _, start := range execx.Starts() {
		if start.Program == program && strings.HasPrefix(start.Caller, caller) {
			count += start.Count
		}
	}
	return count
}
