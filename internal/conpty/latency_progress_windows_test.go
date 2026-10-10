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

// The progress file is the test's own record, and a key's echo never waits
// for it. Here it is a pipe that takes nothing until it is read.
func TestConsoleLatencyChildEchoesAKeyBeforeItsProgressIsWritten(t *testing.T) {
	if os.Getenv("PROBE_AS_MAIN") != "" {
		t.Skip("PROBE: the child writes its receipts as main does")
	}
	// Arrange
	name := fmt.Sprintf(`\\.\pipe\conpty-latency-progress-%d`, os.Getpid())
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := windows.CreateNamedPipe(path, windows.PIPE_ACCESS_DUPLEX, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT, 1, 0, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	progress := os.NewFile(uintptr(pipe), name)
	defer progress.Close()
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
	read := make(chan string, 1)
	go func() {
		var lines strings.Builder
		for receipts := bufio.NewScanner(progress); receipts.Scan() && !strings.HasPrefix(receipts.Text(), "key-0001 output"); {
			lines.WriteString(receipts.Text() + "\n")
		}
		read <- lines.String()
	}()
	select {
	case lines := <-read:
		if !strings.Contains(lines, "input kind=1 down=1 repeat=1 character=0061") {
			t.Fatalf("the progress file holds %q before the echo's own line, want the key's receipt", lines)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the progress file never held the echo's line")
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
