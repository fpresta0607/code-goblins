//go:build linux || darwin

package pty

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// pair opens a pseudo terminal of 90 by 20 cells and returns its controlling
// side and the terminal a program would have, both closed when the test ends.
func pair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	ptmx := os.NewFile(uintptr(fd), "/dev/ptmx")
	t.Cleanup(func() { ptmx.Close() })
	name, err := unlock(fd)
	if err != nil {
		t.Fatal(err)
	}
	tty, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tty.Close() })
	if err := resize(ptmx, 90, 20); err != nil {
		t.Fatal(err)
	}
	return ptmx, tty
}

func termios(t *testing.T, tty *os.File) *unix.Termios {
	t.Helper()
	mode, err := unix.IoctlGetTermios(int(tty.Fd()), getTermios)
	if err != nil {
		t.Fatal(err)
	}
	return mode
}

func TestRawTakesKeysAsTypedAndPutsTheTerminalBack(t *testing.T) {
	for _, test := range []struct {
		name        string
		isOutputRaw bool
	}{
		{"with output as written, as goblins attach shows a terminal", true},
		{"with a newline starting its line, as the quick start's menu draws", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			_, tty := pair(t)
			was := termios(t, tty)

			// Act
			restore, err := Raw(int(tty.Fd()), test.isOutputRaw)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			raw := termios(t, tty)
			if raw.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG|unix.IEXTEN) != 0 {
				t.Errorf("the terminal still reads lines, echoes or signals: local flags %#x", raw.Lflag)
			}
			if raw.Iflag&(unix.ICRNL|unix.IXON) != 0 {
				t.Errorf("the terminal still turns Enter into a newline or takes Ctrl-S and Ctrl-Q: input flags %#x", raw.Iflag)
			}
			if raw.Cc[unix.VMIN] != 1 || raw.Cc[unix.VTIME] != 0 {
				t.Errorf("a read waits for %d keys or %d tenths of a second, want each key at once", raw.Cc[unix.VMIN], raw.Cc[unix.VTIME])
			}
			if isOutputProcessed := raw.Oflag&unix.OPOST != 0; isOutputProcessed == test.isOutputRaw {
				t.Errorf("output processing is %v, want %v", isOutputProcessed, !test.isOutputRaw)
			}
			if err := restore(); err != nil {
				t.Fatal(err)
			}
			if back := termios(t, tty); *back != *was {
				t.Errorf("the terminal's mode came back as %+v, want %+v", *back, *was)
			}
		})
	}
}

func TestSizeReadsTheTerminalsCells(t *testing.T) {
	// Arrange
	_, tty := pair(t)

	// Act
	cols, rows, err := Size(int(tty.Fd()))

	// Assert
	if err != nil || cols != 90 || rows != 20 {
		t.Fatalf("Size = %dx%d, %v, want 90x20", cols, rows, err)
	}
}

func TestRawAndSizeRefuseWhatIsNoTerminal(t *testing.T) {
	// Arrange
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()

	// Act
	_, rawErr := Raw(int(reader.Fd()), true)
	_, _, sizeErr := Size(int(writer.Fd()))

	// Assert
	if rawErr == nil || sizeErr == nil {
		t.Fatalf("Raw on a pipe = %v and Size = %v, want both refused", rawErr, sizeErr)
	}
}
