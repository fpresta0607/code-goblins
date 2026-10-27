package gatetest

import (
	"io"
	"regexp"
	"strings"
	"sync"
)

// BrowserReport passes a browser check's own report through to out and keeps
// the cases it reported as failed, so a check the time limit ends still names
// a case that failed before it.
//
// The pick runs the browser specs with Playwright's list reporter, which
// writes one row per case as the case ends, marked with its pass or fail, as
// it read on this machine on 2026-10-27:
//
//	  ok  87 [board] › tests\composer-status.spec.ts:10:7 › a title (3.8s)
//	  x   88 [board] › tests\composer-status.spec.ts:85:7 › a title (23ms)
//
// A case a retry ran again writes one row per try, the retry's number before
// the time:
//
//	  x   1 [board] › tests\flaky.spec.ts:3:7 › a flaky case (2ms)
//	  ok  2 [board] › tests\flaky.spec.ts:3:7 › a flaky case (retry #1) (3ms)
//
// so a row's retry number and time are no part of the case's name, and a case
// is what its last row said.
//
// The marks are "ok" and "x" where the run's output is drawn with no color, in
// a log or a pipe, and the same marks as the reporter's symbols (✓ and ✘)
// where it is drawn with color. The line reporter writes the same rows without
// a mark, so it cannot say which case failed. That day a prepush run of 141
// specs reported three assertion failures, 465 of 1603 cases ran, the time
// limit ended it, and the run said it passed what it ran (issue 1542). A row
// is read only in that shape, so no other line of a check's output can be
// taken for a case.
type BrowserReport struct {
	out io.Writer

	mu sync.Mutex
	// carry is the row a Write left without its newline, which the next one
	// finishes, and which Failed reads when the run wrote no newline after it.
	carry string
	// failed holds the cases that failed, in the order they failed, and
	// passed the ones a later row of the same case said passed, which a retry
	// writes: a case is what its last row said, and retries are off at a desk.
	failed []string
	passed []string
}

var (
	// browserRow matches one row of a browser run's own report: the mark that
	// says a case passed or failed, the case's number, its project, and its
	// name, which is its file, its line and its titles.
	browserRow = regexp.MustCompile(`^(ok|✓|✔|x|✘|✗)\s+\d+\s+\[([^\]]*)\]\s+›\s+(\S.*)$`)
	// browserDuration matches the time the reporter puts at the end of a row.
	browserDuration = regexp.MustCompile(`\s+\([\d.]+(?:ms|s|m|h)(?: [\d.]+(?:ms|s|m|h))?\)$`)
	// browserRetry matches the retry number a try a retry ran says before its
	// time.
	browserRetry = regexp.MustCompile(`\s+\(retry #\d+\)$`)
)

// NewBrowserReport keeps the cases out reports as failed and passes
// everything it is given on to out.
func NewBrowserReport(out io.Writer) *BrowserReport {
	return &BrowserReport{out: out}
}

// Write passes p to out and reads the rows it holds.
func (r *BrowserReport) Write(p []byte) (int, error) {
	written, err := r.out.Write(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	text := r.carry + string(p[:written])
	r.carry = ""
	lines := strings.Split(text, "\n")
	r.carry = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		r.note(line)
	}
	return written, err
}

// Failed names the cases that failed, in the order they failed, and reads the
// last row even when the run wrote no newline after it.
func (r *BrowserReport) Failed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.carry != "" {
		r.note(r.carry)
		r.carry = ""
	}
	var failed []string
	for _, name := range r.failed {
		if !contains(r.passed, name) {
			failed = append(failed, name)
		}
	}
	return failed
}

// note reads one line of the report as the row of a case, if it is one. Its
// caller holds the lock.
func (r *BrowserReport) note(line string) {
	match := browserRow.FindStringSubmatch(strings.TrimLeft(strings.TrimRight(line, "\r"), " \t"))
	if match == nil {
		return
	}
	name := match[3]
	// The retry number comes before the time, so strip whichever of the two is
	// at the end until neither is left: a row that a retry ran ends with the
	// retry's number and then its time.
	for {
		at := browserDuration.FindStringIndex(name)
		if at == nil {
			at = browserRetry.FindStringIndex(name)
		}
		if at == nil {
			break
		}
		name = name[:at[0]]
	}
	name = strings.ReplaceAll("["+match[2]+"] "+name, `\`, "/")
	if strings.HasPrefix(match[1], "o") || match[1] == "✓" || match[1] == "✔" {
		if !contains(r.passed, name) {
			r.passed = append(r.passed, name)
		}
		return
	}
	if !contains(r.failed, name) {
		r.failed = append(r.failed, name)
	}
}

// contains says whether names holds name.
func contains(names []string, name string) bool {
	for _, have := range names {
		if have == name {
			return true
		}
	}
	return false
}
