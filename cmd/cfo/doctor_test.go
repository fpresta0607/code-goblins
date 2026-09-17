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

// fakeDoctorTool writes a .bat that answers --version, so a temp PATH can
// stand in for a fully provisioned machine.
func fakeDoctorTool(t *testing.T, dir, name string) {
	t.Helper()
	script := "@echo off\r\necho " + name + " 1.0.0\r\n"
	if err := os.WriteFile(filepath.Join(dir, name+".bat"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

// TestRunDoctorReportsPresentationUnavailableAndStaysHealthy drives the whole
// command with every tool but lavish-axi on PATH: doctor's stdout names the
// PRESENTATION_UNAVAILABLE state and the exit code still reports healthy, so a
// missing presentation tool can never block dispatch.
func TestRunDoctorReportsPresentationUnavailableAndStaysHealthy(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{
		"git", "gh", "herdr", "tasks-axi", "quota-axi", "no-mistakes", "gh-axi", "chrome-devtools-axi",
		"claude", "codex", "pi", "kimi",
	} {
		fakeDoctorTool(t, bin, name)
	}
	t.Setenv("PATH", bin)
	t.Setenv("CFO_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	exit := run([]string{"doctor"}, &stdout, &stderr)
	want := "PRESENTATION_UNAVAILABLE lavish-axi not found on PATH (requires >=0.1.71; install: npm install -g lavish-axi@latest) - nonvisual work proceeds in plain text"
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout lacks %q\n%s", want, stdout.String())
	}
	if exit != 0 {
		t.Errorf("exit = %d, want 0: a missing presentation tool must not make doctor unhealthy\n%s", exit, stdout.String())
	}
}
