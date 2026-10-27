package gatetest

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// The rows a browser check writes are read as its cases, in the order they
// failed, with the reporter's own mark for a case drawn without color and with
// its symbols as it draws one with color, and the output still reaches the run
// unchanged.
func TestBrowserReportNamesTheCasesThatFailed(t *testing.T) {
	// Arrange
	var out bytes.Buffer
	report := NewBrowserReport(&out)
	rows := strings.Join([]string{
		`  ok  87 [board] › tests\composer-status.spec.ts:10:7 › classic keeps held queue status compact (3.8s)`,
		`  x   88 [board] › tests\composer-status.spec.ts:85:7 › classic gives a held send failure priority (23ms)`,
		`  ✓  89 [stepped-in] › tests\afk.spec.ts:12:3 › the bar says since when (1.2m)`,
		`  ✘  90 [stepped-in] › tests\afk.spec.ts:19:3 › a waiting goblin says so once (4s)`,
		`  -  91 [iphone] › tests\iphone-terminal.spec.ts:5:1 › a case the run skipped (0ms)`,
		`x 12 of 1603 cases did not pass`,
		`  x   92 [board] › tests\run-terminal.spec.ts:31:1 › a title with no time`,
		"",
	}, "\n")
	want := []string{
		"[board] tests/composer-status.spec.ts:85:7 › classic gives a held send failure priority",
		"[stepped-in] tests/afk.spec.ts:19:3 › a waiting goblin says so once",
		"[board] tests/run-terminal.spec.ts:31:1 › a title with no time",
	}

	// Act
	if _, err := report.Write([]byte(rows)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Assert
	if got := report.Failed(); !slices.Equal(got, want) {
		t.Errorf("failed cases are\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if out.String() != rows {
		t.Errorf("the run's output was changed: %q", out.String())
	}
}

// A row a run writes in pieces is read whole, and the last row is read even
// when the run wrote no newline after it, as a run the time limit ends leaves
// it.
func TestBrowserReportReadsRowsSplitAcrossWritesAndWithoutANewline(t *testing.T) {
	// Arrange
	var out bytes.Buffer
	report := NewBrowserReport(&out)
	row := `  x   88 [board] › tests\afk.spec.ts:85:7 › a case that failed (23ms)`
	want := []string{"[board] tests/afk.spec.ts:85:7 › a case that failed"}

	// Act
	for _, piece := range []string{row[:9], row[9:40], row[40:]} {
		if _, err := report.Write([]byte(piece)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Assert
	if got := report.Failed(); !slices.Equal(got, want) {
		t.Errorf("failed cases are %q, want %q", got, want)
	}
	if out.String() != row {
		t.Errorf("the run's output is %q, want %q", out.String(), row)
	}
}

// A case that failed and passed when the run tried it again is no failure:
// the reporter writes one row per try, and a case is what its last row said.
func TestBrowserReportDropsACaseThatPassedOnARetry(t *testing.T) {
	// Arrange
	var out bytes.Buffer
	report := NewBrowserReport(&out)
	rows := strings.Join([]string{
		`  x   88 [board] › tests\afk.spec.ts:85:7 › a case that failed once (23ms)`,
		`  ok  88 [board] › tests\afk.spec.ts:85:7 › a case that failed once (1.1s)`,
		`  x   89 [board] › tests\afk.spec.ts:90:7 › a case that failed twice (23ms)`,
		"",
	}, "\n")

	// Act
	if _, err := report.Write([]byte(rows)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Assert
	want := []string{"[board] tests/afk.spec.ts:90:7 › a case that failed twice"}
	if got := report.Failed(); !slices.Equal(got, want) {
		t.Errorf("failed cases are %q, want %q", got, want)
	}
}

// A line that is not one of the reporter's rows is no case, whatever it holds:
// a check's own output can say "x" without a case having failed.
func TestBrowserReportReadsOnlyTheReportersRows(t *testing.T) {
	// Arrange
	var out bytes.Buffer
	report := NewBrowserReport(&out)
	for _, line := range []string{
		"x 12 of 1603 cases did not pass",
		"x  3 cases failed",
		"  ok",
		"  x   ",
		"  x   88 [board] ",
		"  3 failed",
		"",
	} {
		if _, err := report.Write([]byte(line + "\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Act
	failed := report.Failed()

	// Assert
	if len(failed) != 0 {
		t.Errorf("these lines are read as failed cases: %q", failed)
	}
}
