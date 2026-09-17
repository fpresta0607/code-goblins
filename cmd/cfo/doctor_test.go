package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDoctorPrintsTheLaneTableBesideTheSwitchRules(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "routing.json"), []byte(shippedRoutingJSON(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", root)

	var stdout, stderr bytes.Buffer
	if exit := run([]string{"doctor"}, &stdout, &stderr); exit != 0 && exit != 1 {
		t.Fatalf("exit = %d, want doctor's health verdict", exit)
	}
	path := filepath.Join(root, "data", "routing.json")
	wants := []string{
		"routing: 2 standing switch rule(s) from " + path,
		"routing: 4 execution lane(s) from " + path + " (default build, escalate to deep)",
		fmt.Sprintf("  %-11s %-7s %-8s %-7s %s", "deep", "claude", "fable", "xhigh", "architecture, security, migration, rescue, anything high risk"),
		fmt.Sprintf("  %-11s %-7s %-8s %-7s %s", "build", "claude", "opus", "high", "ordinary implementation; the default lane"),
		fmt.Sprintf("  %-11s %-7s %-8s %-7s %s", "mechanical", "claude", "sonnet", "medium", "renames, config edits, docs, tightly specified changes"),
		fmt.Sprintf("  %-11s %-7s %-8s %-7s %s", "scout", "claude", "opus", "xhigh", "investigations that produce a report"),
	}
	for _, want := range wants {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q\n%s", want, stdout.String())
		}
	}
}

func TestRunDoctorReportsAnInvalidLaneTableBelowTheSwitchRules(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	policy := `{"rules":[{"harness":"kimi","fault":"rate-limit","switch":{"harness":"codex"}}],"default_lane":"build","lanes":{"build":{"model":"opus"}}}`
	if err := os.WriteFile(filepath.Join(root, "data", "routing.json"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", root)

	var stdout, stderr bytes.Buffer
	run([]string{"doctor"}, &stdout, &stderr)
	path := filepath.Join(root, "data", "routing.json")
	for _, want := range []string{
		"routing: 1 standing switch rule(s) from " + path,
		"cfo switch <id> --harness codex",
		"routing: lane table invalid (routing: " + path + `: lane "build" has no harness)`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "execution lane(s)") {
		t.Errorf("stdout lists lanes from an invalid table\n%s", stdout.String())
	}
}

func TestRunDoctorSaysWhenThereAreNoLanes(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "routing.json"), []byte(`{"rules":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", root)

	var stdout, stderr bytes.Buffer
	run([]string{"doctor"}, &stdout, &stderr)
	if !strings.Contains(stdout.String(), "routing: no execution lanes (add lanes to ") {
		t.Errorf("stdout lacks the missing-lanes line\n%s", stdout.String())
	}
}
