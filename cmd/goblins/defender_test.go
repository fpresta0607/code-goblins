package main

import (
	"bytes"
	"context"
	"errors"
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

// With nothing recorded it says so in one line, with the time it read from.
func TestDefenderSaysWhenNothingWasRecorded(t *testing.T) {
	// Arrange
	t.Setenv("GOBLINS_HOME", t.TempDir())
	t.Setenv("GOBLINS_STATE_OVERRIDE", "")
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
	t.Setenv("GOBLINS_HOME", t.TempDir())
	t.Setenv("GOBLINS_STATE_OVERRIDE", "")
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
	t.Setenv("GOBLINS_HOME", t.TempDir())
	t.Setenv("GOBLINS_STATE_OVERRIDE", "")
	var stdout, stderr bytes.Buffer
	if code := runWithRuntime([]string{"defender", "--since", "yesterday"}, &stdout, &stderr, defenderRuntime(defender.Report{}, nil, nil)); code != 2 {
		t.Errorf("a time it cannot read exits %d, want 2: %s", code, stderr.String())
	}
}
