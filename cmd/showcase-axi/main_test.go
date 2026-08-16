package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/showcase"
)

func TestUsageAndVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Errorf("run() = %d, want 2", code)
	}
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Errorf("run(version) = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "showcase-axi") {
		t.Errorf("version output = %q", stdout.String())
	}
}

func TestEndThenPollDeliversEndedPayload(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(artifact, []byte("# Plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"end", artifact}, &stdout, &stderr); code != 0 {
		t.Fatalf("end = %d: %s", code, stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"poll", artifact}, &stdout, &stderr); code != 0 {
		t.Fatalf("poll = %d: %s", code, stderr.String())
	}
	var payload showcase.Payload
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("poll output is not JSON: %q: %v", stdout.String(), err)
	}
	if !payload.Ended || payload.EndedBy != "agent" {
		t.Errorf("payload = %+v, want ended by agent", payload)
	}
}

func TestExportCommandWritesStandaloneFile(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "report.md")
	if err := os.WriteFile(artifact, []byte("# Report\n\nFindings.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.html")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"export", artifact, "--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("export = %d: %s", code, stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("exported file missing: %v", err)
	}
	if !strings.Contains(string(data), "Findings.") {
		t.Errorf("exported file lacks the rendered artifact")
	}
}
