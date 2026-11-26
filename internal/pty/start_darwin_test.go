package pty

import "testing"

func TestAProgramKeptToProcessorThreadsIsRefusedOnMacOS(t *testing.T) {
	// Act
	terminal, err := Start(Spec{Args: []string{"sh", "-c", "exit 0"}, Cols: 80, Rows: 24, Processors: 1})

	// Assert
	if err == nil {
		_ = terminal.Close()
		t.Fatal("Start kept a program to processor threads on macOS, want it refused")
	}
}
