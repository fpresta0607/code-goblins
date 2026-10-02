package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/doctor"
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
		fmt.Sprintf("  %-11s %-7s %-8s %-7s %s", "scout", "claude", "fable", "xhigh", "investigations that produce a report"),
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
// stand in for a fully provisioned machine. Claude Code must be a program, as
// its native build is, so claude is this test binary copied as claude.exe,
// which TestMain answers as it.
func fakeDoctorTool(t *testing.T, dir, name string) {
	t.Helper()
	if name == "claude" {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		program, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}
		claudePath := filepath.Join(dir, "claude.exe")
		if err := os.WriteFile(claudePath, program, 0o700); err != nil {
			t.Fatal(err)
		}
		// Antivirus can still hold the freshly written program when the test
		// ends, so remove it with retries before t.TempDir's cleanup runs.
		t.Cleanup(func() {
			deadline := time.Now().Add(30 * time.Second)
			for {
				err := os.Remove(claudePath)
				if err == nil {
					return
				}
				if time.Now().After(deadline) {
					t.Errorf("remove %s: %v", claudePath, err)
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
		return
	}
	script := "@echo off\r\necho " + name + " 1.0.0\r\n"
	if err := os.WriteFile(filepath.Join(dir, name+".bat"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

// Without winget, doctor says how to get it, since the installer stops for
// it, and stays healthy: cfo itself never runs winget.
func TestRunDoctorReportsAMissingWingetAndStaysHealthy(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{
		"git", "gh", "herdr", "tasks-axi", "quota-axi", "no-mistakes", "gh-axi", "chrome-devtools-axi", "lavish-axi",
		"claude", "codex", "pi", "kimi",
	} {
		fakeDoctorTool(t, bin, name)
	}
	t.Setenv("PATH", bin)
	t.Setenv("CFO_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	exit := run([]string{"doctor"}, &stdout, &stderr)

	want := "INSTALLER_UNAVAILABLE winget not found on PATH (install: App Installer from the Microsoft Store, https://apps.microsoft.com/detail/9NBLGGH4NNS1) - only install.ps1 needs it, to add git and gh"
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout lacks %q\n%s", want, stdout.String())
	}
	if exit != 0 {
		t.Errorf("exit = %d, want 0: a missing winget must not make doctor unhealthy\n%s", exit, stdout.String())
	}
}

// Without Herdr, doctor says who needs it and how to get it, and stays
// healthy: a goblin or CFO in a native terminal needs no Herdr.
func TestRunDoctorReportsAMissingHerdrAsOptionalAndStaysHealthy(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{
		"git", "gh", "tasks-axi", "quota-axi", "no-mistakes", "gh-axi", "chrome-devtools-axi", "lavish-axi", "winget",
		"claude", "codex", "pi", "kimi",
	} {
		fakeDoctorTool(t, bin, name)
	}
	t.Setenv("PATH", bin)
	t.Setenv("CFO_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	exit := run([]string{"doctor"}, &stdout, &stderr)

	want := "OPTIONAL herdr not found on PATH (install: irm https://herdr.dev/install.ps1 | iex) - only a goblin or CFO started in Herdr needs it"
	if !strings.Contains(stdout.String(), want) || strings.Contains(stdout.String(), "MISSING") {
		t.Errorf("stdout lacks %q or reports something missing\n%s", want, stdout.String())
	}
	if exit != 0 {
		t.Errorf("exit = %d, want 0: a missing Herdr must not make doctor unhealthy\n%s", exit, stdout.String())
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
	want := "PRESENTATION_UNAVAILABLE lavish-axi not found on PATH (requires >=0.1.79; install: npm install -g " + doctor.LavishRelease + ") - nonvisual work proceeds in plain text"
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout lacks %q\n%s", want, stdout.String())
	}
	if exit != 0 {
		t.Errorf("exit = %d, want 0: a missing presentation tool must not make doctor unhealthy\n%s", exit, stdout.String())
	}
}

// cfo doctor shows what the monitor counted: the stale wakes it raised and the
// ones it held back, each reason with the last goblin and the evidence that
// held it, so a detector that stops seeing is visible rather than read as a
// quiet fleet.
func TestRunDoctorReportsStaleWakesHeldBackBesideThoseRaised(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CFO_HOME", root)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	var empty bytes.Buffer
	reportStaleWakes(&empty)
	if !strings.Contains(empty.String(), "stale wakes: nothing counted yet") {
		t.Errorf("a home the monitor never scanned reports %q, want nothing counted yet", empty.String())
	}

	if err := os.MkdirAll(filepath.Join(root, "state", "monitor"), 0o755); err != nil {
		t.Fatal(err)
	}
	tally := `{"schema":"cfo-monitor-tally.v1","since":"2026-09-30T12:00:00Z","reasons":{"busy_turn_over_age":{"raised":1,"suppressed":3,"last":"2026-09-30T14:02:00Z","last_task":"g1","last_why":"the pane shows work running: ⎿  Running… (22s · timeout 10m)"},"goblin_idle":{"raised":2,"suppressed":0}}}`
	if err := os.WriteFile(filepath.Join(root, "state", "monitor", "tally.json"), []byte(tally), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	reportStaleWakes(&stdout)

	for _, want := range []string{
		"stale wakes since 2026-09-30 12:00 UTC",
		fmt.Sprintf("  %-20s raised %4d  held back %4d  last g1 at 09-30 14:02: the pane shows work running: ⎿  Running… (22s · timeout 10m)", "busy_turn_over_age", 1, 3),
		fmt.Sprintf("  %-20s raised %4d  held back %4d", "goblin_idle", 2, 0),
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("doctor lacks %q\n%s", want, stdout.String())
		}
	}
}

// cfo doctor says which harness this home starts the CFO as and what a CFO in
// it gets, from the same table the quick start and the first-run page read:
// a Codex or pi CFO names each thing it goes without, and Claude Code none.
func TestRunDoctorSaysWhatTheCFOsHarnessGets(t *testing.T) {
	for _, tc := range []struct {
		name, remembered string
		want             []string
		lacks            int
	}{
		{"a home that remembers none starts Claude Code", "", []string{"cfo harness: Claude Code (the best experience); its SessionStart hook registers it\n"}, 0},
		{"Codex", "codex", []string{"cfo harness: Codex (woken by a typed line; no digest or guards); its first prompt runs cfo register\n", "  goes without: no turn-end guard and no pre-tool guards\n"}, 3},
		{"pi", "pi", []string{"cfo harness: pi (woken by a typed line; no digest, guards or resume); its first prompt runs cfo register\n", "  goes without: a closed pi CFO starts a new conversation: it is not resumed\n"}, 4},
		{"a harness no CFO runs in", "kimi", []string{"cfo harness: unreadable ("}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			t.Setenv("CFO_HOME", root)
			t.Setenv("CFO_STATE_OVERRIDE", "")
			if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.remembered != "" {
				if err := os.WriteFile(filepath.Join(root, "state", "cfo-harness"), []byte(tc.remembered+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout bytes.Buffer

			// Act
			reportCFOHarness(&stdout)

			// Assert
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("doctor lacks %q\n%s", want, stdout.String())
				}
			}
			if got := strings.Count(stdout.String(), "  goes without: "); got != tc.lacks {
				t.Errorf("doctor names %d things the CFO goes without, want %d\n%s", got, tc.lacks, stdout.String())
			}
		})
	}
}
