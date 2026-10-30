package gatetest

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// The rows a browser check writes are read as its cases, in the order they
// failed, and the output still reaches the run unchanged.
func TestBrowserReportNamesTheCasesThatFailed(t *testing.T) {
	// Arrange
	var out bytes.Buffer
	report := NewBrowserReport(&out)
	rows := strings.Join([]string{
		`  ok  87 [board] › tests\composer-status.spec.ts:10:7 › classic keeps held queue status compact (3.8s)`,
		`  x   88 [board] › tests\composer-status.spec.ts:85:7 › classic gives a held send failure priority (23ms)`,
		`cfo gate case: unexpected [board] tests/composer-status.spec.ts:85:7 › classic gives a held send failure priority`,
		`  ✘  90 [stepped-in] › tests\afk.spec.ts:19:3 › a waiting goblin says so once (4s)`,
		`cfo gate case: unexpected [stepped-in] tests/afk.spec.ts:19:3 › a waiting goblin says so once`,
		`  -  91 [iphone] › tests\iphone-terminal.spec.ts:5:1 › a case the run skipped (0ms)`,
		`cfo gate case: unexpected [board] tests/run-terminal.spec.ts:31:1 › a describe › a title`,
		"",
	}, "\n")
	want := []string{
		"[board] tests/composer-status.spec.ts:85:7 › classic gives a held send failure priority",
		"[stepped-in] tests/afk.spec.ts:19:3 › a waiting goblin says so once",
		"[board] tests/run-terminal.spec.ts:31:1 › a describe › a title",
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

// A case declared with test.fail that failed as expected is no failure, though
// the list reporter marks it x as it marks a case that failed (issue 1587).
// The rows are from a run of `npx playwright test
// --reporter=list,./tests/gate-cases.ts` on the board's @playwright/test
// 1.63.0, with retries off as at a desk, of a case that passed, a case
// declared with test.fail that failed, one declared with test.fail that
// passed, a case that failed and a skipped one.
func TestBrowserReportDropsACaseThatFailedAsExpected(t *testing.T) {
	// Arrange
	var out bytes.Buffer
	report := NewBrowserReport(&out)
	transcript := `Running 5 tests using 1 worker

  ok 1 [board] › tests\cases.spec.ts:2:1 › passes (1ms)
  x  2 [board] › tests\cases.spec.ts:3:6 › a test does not start (4ms)
  ok 3 [board] › tests\cases.spec.ts:4:6 › an expected failure that passes (0ms)
cfo gate case: unexpected [board] tests/cases.spec.ts:4:6 › an expected failure that passes
  x  4 [board] › tests\cases.spec.ts:5:1 › fails (6ms)
cfo gate case: unexpected [board] tests/cases.spec.ts:5:1 › fails
  -  5 [board] › tests\cases.spec.ts:8:6 › skipped


  1) [board] › tests\cases.spec.ts:4:6 › an expected failure that passes ───────────────────────────

    Expected to fail, but passed.

  2) [board] › tests\cases.spec.ts:5:1 › fails ─────────────────────────────────────────────────────

    Error: expect(received).toBe(expected) // Object.is equality

  2 failed
    [board] › tests\cases.spec.ts:4:6 › an expected failure that passes ────────────────────────────
    [board] › tests\cases.spec.ts:5:1 › fails ──────────────────────────────────────────────────────
  1 skipped
  2 passed (1.1s)
`
	want := []string{
		"[board] tests/cases.spec.ts:4:6 › an expected failure that passes",
		"[board] tests/cases.spec.ts:5:1 › fails",
	}

	// Act
	if _, err := report.Write([]byte(transcript)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Assert
	if got := report.Failed(); !slices.Equal(got, want) {
		t.Errorf("failed cases are %q, want %q", got, want)
	}
}

// A row a run writes in pieces is read whole, and the last row is read even
// when the run wrote no newline after it, as a run the time limit ends leaves
// it.
func TestBrowserReportReadsRowsSplitAcrossWritesAndWithoutANewline(t *testing.T) {
	// Arrange
	var out bytes.Buffer
	report := NewBrowserReport(&out)
	row := `cfo gate case: unexpected [board] tests/afk.spec.ts:85:7 › a case that failed`
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

// A case that failed and passed when the run tried it again is no failure: the
// reporter writes a row per try that failed and a flaky row for the retry that
// passed it, so a case is what its last row said. The rows are a transcript of
// `npx playwright test --reporter=list,./tests/gate-cases.ts` on the board's
// @playwright/test 1.63.0, with `retries: 1`, of a case that passes on its
// retry and one that fails both tries.
func TestBrowserReportDropsACaseThatPassedOnARetry(t *testing.T) {
	// Arrange
	var out bytes.Buffer
	report := NewBrowserReport(&out)
	rows := strings.Join([]string{
		`Running 2 tests using 1 worker`,
		``,
		`  x  1 [board] › tests\cases.spec.ts:5:1 › fails (6ms)`,
		`cfo gate case: unexpected [board] tests/cases.spec.ts:5:1 › fails`,
		`  x  2 [board] › tests\cases.spec.ts:5:1 › fails (retry #1) (6ms)`,
		`cfo gate case: unexpected [board] tests/cases.spec.ts:5:1 › fails`,
		`  x  3 [board] › tests\cases.spec.ts:7:1 › flaky (7ms)`,
		`cfo gate case: unexpected [board] tests/cases.spec.ts:7:1 › flaky`,
		`  ok 4 [board] › tests\cases.spec.ts:7:1 › flaky (retry #1) (5ms)`,
		`cfo gate case: flaky [board] tests/cases.spec.ts:7:1 › flaky`,
		"",
	}, "\n")

	// Act
	if _, err := report.Write([]byte(rows)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Assert
	want := []string{"[board] tests/cases.spec.ts:5:1 › fails"}
	if got := report.Failed(); !slices.Equal(got, want) {
		t.Errorf("failed cases are %q, want %q", got, want)
	}
}

// A line that is not one of the gate reporter's rows is no case, whatever it
// holds: a check's own output can say "x" or "unexpected" without a case
// having failed, and the list reporter's x is no failure by itself.
func TestBrowserReportReadsOnlyTheGateReportersRows(t *testing.T) {
	// Arrange
	var out bytes.Buffer
	report := NewBrowserReport(&out)
	for _, line := range []string{
		`  x   88 [board] › tests\composer-status.spec.ts:85:7 › a title (23ms)`,
		"x 12 of 1603 cases did not pass",
		"cfo gate case: unexpected",
		"cfo gate case: unexpected tests/afk.spec.ts:3:1 › no project",
		"cfo gate case: expected [board] tests/afk.spec.ts:3:1 › a title",
		"  cfo gate case: unexpected [board] tests/afk.spec.ts:3:1 › an indented row",
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
