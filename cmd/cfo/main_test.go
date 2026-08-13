package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	drainHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(drainHome, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		args       []string
		env        map[string]string
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{name: "no args prints usage", args: nil, wantExit: 2, wantStderr: "usage: cfo"},
		{name: "unknown command", args: []string{"nonsense"}, wantExit: 2, wantStderr: `unknown command "nonsense"`},
		{name: "version", args: []string{"version"}, wantExit: 0, wantStdout: "cfo dev"},
		{name: "doctor runs and reports each tool", args: []string{"doctor"}, wantExit: -1, wantStdout: "git"},
		{name: "drain empty queue", args: []string{"drain"}, env: map[string]string{"CFO_HOME": drainHome}, wantExit: 0, wantStdout: "WAKE QUEUE: empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			var stdout, stderr bytes.Buffer
			got := run(tt.args, &stdout, &stderr)
			if tt.wantExit != -1 && got != tt.wantExit {
				t.Errorf("exit = %d, want %d", got, tt.wantExit)
			}
			if tt.wantExit == -1 && got != 0 && got != 1 {
				t.Errorf("exit = %d, want 0 or 1 (doctor's health verdict)", got)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
