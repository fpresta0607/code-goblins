package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/installtest"
)

// An evidence writer once read a response marker with Get-Content -Raw and
// handed it to ConvertTo-Json -Depth 8; Windows PowerShell 5.1 walked the
// string's provider objects until it held 7.9 GB and never wrote its file.
// The capsule now tells a goblin to read file text with evidenceFileText, and
// this proves that read in Windows PowerShell 5.1 gives the marker field as a
// JSON string with no provider metadata. Both reads serialize at depth 1, as
// the bounded probe that found the defect did: that already shows a decorated
// string's provider fields, so the decorated read proves the defect is still
// there to avoid, and a read that brought it back fails here without the deep
// walk.
func TestEvidenceFileTextIsAPlainJSONStringInWindowsPowerShell(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "response.marker")
	const text = "response marker 1447\nsecond line\n"
	if err := os.WriteFile(marker, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "$ErrorActionPreference = 'Stop'\n" +
		"$decorated = Get-Content -Raw -LiteralPath $env:EVIDENCE_MARKER\n" +
		"$plain = " + evidenceFileText + "($env:EVIDENCE_MARKER)\n" +
		"ConvertTo-Json -InputObject @{ marker = $decorated } -Depth 1 -Compress\n" +
		"ConvertTo-Json -InputObject @{ marker = $plain } -Depth 1 -Compress\n"
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	shell := exec.CommandContext(ctx, installtest.WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-Command", script)
	shell.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	shell.Env = append(os.Environ(), "EVIDENCE_MARKER="+marker)
	out, err := shell.Output()
	if err != nil {
		t.Fatalf("Windows PowerShell: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")), "\n")
	if len(lines) != 2 {
		t.Fatalf("output = %q, want two JSON lines", out)
	}

	var decorated map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &decorated); err != nil {
		t.Fatalf("decorated line %q: %v", lines[0], err)
	}
	if !strings.HasPrefix(string(decorated["marker"]), "{") || !strings.Contains(string(decorated["marker"]), `"PSProvider"`) {
		t.Fatalf("Get-Content -Raw marker = %s, want the provider-decorated object this rule avoids", decorated["marker"])
	}

	var plain map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &plain); err != nil {
		t.Fatalf("plain line %q: %v", lines[1], err)
	}
	if got, isString := plain["marker"].(string); len(plain) != 1 || !isString || got != text {
		t.Errorf("evidence = %s, want only marker as the JSON string %q with no provider metadata", lines[1], text)
	}
}
