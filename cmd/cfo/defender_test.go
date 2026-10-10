package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/defender"
)

// defenderRuntime is a runtime whose Defender recorded report, and the time
// each read asked from.
func defenderRuntime(report defender.Report, err error, asked *time.Time) commandRuntime {
	runtime := defaultCommandRuntime()
	runtime.readDefender = func(_ context.Context, since time.Time) (defender.Report, error) {
		if asked != nil {
			*asked = since
		}
		return report, err
	}
	return runtime
}

// cfo defender lists what Defender recorded under the fleet's folders since
// a time: each detection with its files and who made them, each upload, and
// only a count of what it recorded elsewhere on the machine.
func TestDefenderListsWhatWasRecordedUnderTheFleetsFolders(t *testing.T) {
	// Arrange
	home := t.TempDir()
	t.Setenv("CFO_HOME", home)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	// The detected file is in a test's temporary folder on another drive,
	// so what its path says of who made it does not depend on where this
	// test runs. The uploaded one is in the home.
	made := `X:\CodeGoblins\scratch\cg-auth-probe-hang\TestOneLineInstallKeepsANoMistakesAtThePin67573441\003\cfo.exe.download`
	script := filepath.Join(home, "candidates", "cand-v0.5.13", "install.ps1")
	report := defender.Report{
		Detections: []defender.Detection{
			{ID: "{A1}", Time: time.Date(2026, 10, 7, 20, 43, 10, 0, time.UTC), Threat: "Trojan:Win32/Bearfoos.B!ml", Process: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, Files: []string{made}},
			{ID: "{B2}", Time: time.Date(2026, 10, 2, 4, 16, 16, 0, time.UTC), Threat: "Trojan:Win32/ClickFix.DQ!MTB", CommandLines: 42},
			{ID: "{C3}", Time: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), Threat: "Trojan:Win32/Other", Files: []string{`D:\Photos\viewer.exe`}},
		},
		Uploads: []defender.Upload{{Time: time.Date(2026, 10, 10, 13, 31, 17, 0, time.UTC), File: script, SHA256: "98be6d6b1ffa14adac0cb872aeed0e42b0f25c2260821891dffd5ef92bf3e277"}},
	}
	var asked time.Time
	var stdout, stderr bytes.Buffer

	// Act
	code := runWithRuntime([]string{"defender", "--since", "2026-10-01T00:00:00Z"}, &stdout, &stderr, defenderRuntime(report, nil, &asked))

	// Assert
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !asked.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Defender was read from %s, want the time given", asked)
	}
	for _, want := range []string{
		"since 2026-10-01T00:00:00Z: 2 detections, 1 upload",
		"detection 2026-10-07T20:43:10Z Trojan:Win32/Bearfoos.B!ml",
		made + " (test TestOneLineInstallKeepsANoMistakesAtThePin of task cg-auth-probe-hang)",
		"detection 2026-10-02T04:16:16Z Trojan:Win32/ClickFix.DQ!MTB",
		"42 command lines, no file",
		"upload 2026-10-10T13:31:17Z " + script,
		" sha256 98be6d6b1ffa",
		"1 more that names no file under the fleet's folders is not listed",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "Photos") {
		t.Errorf("stdout names a file outside the fleet's folders:\n%s", stdout.String())
	}
	if strings.ContainsAny(stdout.String(), ";\u2014") {
		t.Errorf("stdout holds a semicolon or an em dash:\n%s", stdout.String())
	}
}

// With nothing recorded it says so in one line, with the time it read from.
func TestDefenderSaysWhenNothingWasRecorded(t *testing.T) {
	// Arrange
	t.Setenv("CFO_HOME", t.TempDir())
	t.Setenv("CFO_STATE_OVERRIDE", "")
	var asked time.Time
	var stdout, stderr bytes.Buffer
	before := time.Now().UTC()

	// Act
	code := runWithRuntime([]string{"defender"}, &stdout, &stderr, defenderRuntime(defender.Report{}, nil, &asked))

	// Assert
	if code != 0 || !strings.Contains(stdout.String(), "Microsoft Defender recorded no detection and no upload under the fleet's folders since ") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if day := before.Sub(asked); day < 24*time.Hour-time.Minute || day > 24*time.Hour+time.Minute {
		t.Errorf("with no --since Defender was read from %s ago, want a day", day)
	}
}

// A Defender that cannot be read fails the command with what it said: no
// answer is never shown as a quiet machine.
func TestDefenderFailsWhenDefenderCannotBeRead(t *testing.T) {
	// Arrange
	t.Setenv("CFO_HOME", t.TempDir())
	t.Setenv("CFO_STATE_OVERRIDE", "")
	var stdout, stderr bytes.Buffer

	// Act
	code := runWithRuntime([]string{"defender"}, &stdout, &stderr, defenderRuntime(defender.Report{}, errors.New("read Microsoft Defender's records: access denied"), nil))

	// Assert
	if code != 1 || !strings.Contains(stderr.String(), "access denied") || stdout.Len() != 0 {
		t.Errorf("exit %d, stdout %q, stderr %q, want exit 1 saying why and nothing listed", code, stdout.String(), stderr.String())
	}
}

func TestDefenderTakesATimeADayOrALength(t *testing.T) {
	now := time.Date(2026, 10, 10, 16, 0, 0, 0, time.UTC)
	for given, want := range map[string]time.Time{
		"2026-10-10T15:00:00Z":      time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC),
		"2026-10-10T10:00:00-05:00": time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC),
		"2026-10-08":                time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
		"36h":                       now.Add(-36 * time.Hour),
		"90m":                       now.Add(-90 * time.Minute),
		"7d":                        now.Add(-7 * 24 * time.Hour),
	} {
		if got, err := sinceTime(given, now); err != nil || !got.Equal(want) {
			t.Errorf("sinceTime(%q) = %s, %v, want %s", given, got, err, want)
		}
	}
	for _, given := range []string{"", "yesterday", "-3h", "0d", "10-10-2026"} {
		if got, err := sinceTime(given, now); err == nil {
			t.Errorf("sinceTime(%q) = %s, want it refused", given, got)
		}
	}
	t.Setenv("CFO_HOME", t.TempDir())
	t.Setenv("CFO_STATE_OVERRIDE", "")
	var stdout, stderr bytes.Buffer
	if code := runWithRuntime([]string{"defender", "--since", "yesterday"}, &stdout, &stderr, defenderRuntime(defender.Report{}, nil, nil)); code != 2 {
		t.Errorf("a time it cannot read exits %d, want 2: %s", code, stderr.String())
	}
}

// --json prints the same report for a program, each file with who made it.
func TestDefenderPrintsTheReportForAProgram(t *testing.T) {
	// Arrange
	t.Setenv("CFO_HOME", t.TempDir())
	t.Setenv("CFO_STATE_OVERRIDE", "")
	made := `X:\CodeGoblins\state\tasktmp\cg-dev-drive\TestTheDriveTakesAProgram20261008\cfo.exe`
	report := defender.Report{Detections: []defender.Detection{{ID: "{A1}", Time: time.Date(2026, 10, 8, 3, 57, 34, 0, time.UTC), Threat: "Trojan:Win32/Bearfoos.A!ml", Files: []string{made}}}}
	var stdout, stderr bytes.Buffer

	// Act
	code := runWithRuntime([]string{"defender", "--since", "7d", "--json"}, &stdout, &stderr, defenderRuntime(report, nil, nil))

	// Assert
	var printed struct {
		Since      time.Time `json:"since"`
		Detections []struct {
			ID     string `json:"id"`
			Threat string `json:"threat"`
			Files  []struct {
				Path string `json:"path"`
				Task string `json:"task"`
			} `json:"files"`
		} `json:"detections"`
		Uploads   []json.RawMessage `json:"uploads"`
		Elsewhere int               `json:"elsewhere"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &printed); code != 0 || err != nil {
		t.Fatalf("exit %d, %v: %s%s", code, err, stdout.String(), stderr.String())
	}
	if len(printed.Detections) != 1 || len(printed.Detections[0].Files) != 1 || printed.Detections[0].Files[0].Path != made || printed.Detections[0].Files[0].Task != "cg-dev-drive" || printed.Uploads == nil || printed.Since.IsZero() {
		t.Errorf("printed %s", stdout.String())
	}
}
