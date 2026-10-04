package conpty

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
