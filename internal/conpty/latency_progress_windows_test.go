package conpty

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// heldFile is a file that takes nothing a program writes to it until the test
// reads it, a pipe with no buffer: its name, for the program, and the end the
// test reads.
func heldFile(t *testing.T) (string, *os.File) {
	t.Helper()
	name := fmt.Sprintf(`\\.\pipe\%s-%d`, t.Name(), os.Getpid())
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := windows.CreateNamedPipe(path, windows.PIPE_ACCESS_DUPLEX, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT, 1, 0, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(pipe), name)
	t.Cleanup(func() { file.Close() })
	return name, file
}

// linesUntil reads file's lines up to the first that starts with last, and
// fails the test if that line has not come within ten seconds.
func linesUntil(t *testing.T, file *os.File, last string) string {
	t.Helper()
	read := make(chan string, 1)
	go func() {
		var lines strings.Builder
		for scanner := bufio.NewScanner(file); scanner.Scan(); {
			lines.WriteString(scanner.Text() + "\n")
			if strings.HasPrefix(scanner.Text(), last) {
				break
			}
		}
		read <- lines.String()
	}()
	select {
	case lines := <-read:
		return lines
	case <-time.After(10 * time.Second):
		t.Fatalf("no line that starts %q was written within 10s", last)
		return ""
	}
}

// The progress file is the test's own record, and a key's echo never waits
// for it: the echo comes while the file takes nothing.
func TestConsoleLatencyChildEchoesAKeyBeforeItsProgressIsWritten(t *testing.T) {
	// Arrange
	name, progress := heldFile(t)
	console, output := startChild(t, Spec{
		Args: []string{os.Args[0], "-test.run=^TestConsoleLatencyChild$", "--", "latency-child", "idle"},
		Env:  append(os.Environ(), "CONPTY_LATENCY_PROGRESS="+name),
		Cols: 120, Rows: 40,
	})

	// Act
	if _, err := console.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}

	// Assert
	output.waitFor(t, "key-0001")
	if lines := linesUntil(t, progress, "key-0001 output"); !strings.Contains(lines, "input kind=1 down=1 repeat=1 character=0061") {
		t.Fatalf("the progress file holds %q, want the key's receipt before its echo's line", lines)
	}
}

func TestConsoleLatencyRecordsInputBeforeItsResponse(t *testing.T) {
	progressPath := filepath.Join(t.TempDir(), "native-input.log")
	console, output := startChild(t, Spec{
		Args: []string{os.Args[0], "-test.run=^TestConsoleLatencyChild$", "--", "latency-child", "busy"},
		Env:  append(os.Environ(), "CONPTY_LATENCY_PROGRESS="+progressPath),
		Cols: 120, Rows: 40,
	})

	if _, err := console.Write([]byte("!")); err != nil {
		t.Fatal(err)
	}
	isReceiptObserved := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		progress, err := os.ReadFile(progressPath)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if strings.Contains(string(progress), "character=0021") {
			isReceiptObserved = true
			break
		}
	}
	output.mu.Lock()
	isResponsePremature := strings.Contains(output.text.String(), "burst-")
	output.mu.Unlock()
	if isResponsePremature {
		t.Fatal("burst response appeared before its payload")
	}

	payload := strings.Repeat("0123456789abcdefABCD", 100)
	started := time.Now()
	if _, err := console.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("burst-%x", sha256.Sum256([]byte(payload)))
	isResponseObserved := false
	for deadline := started.Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		output.mu.Lock()
		isResponseObserved = strings.Contains(output.text.String(), marker)
		output.mu.Unlock()
		if isResponseObserved {
			break
		}
	}
	if !isResponseObserved || time.Since(started) > 2*time.Second {
		t.Fatal("ordered native burst response missed its latency bound")
	}
	if !isReceiptObserved {
		t.Fatal("native burst completed, but its initial input receipt was unavailable while the response was pending")
	}
}
