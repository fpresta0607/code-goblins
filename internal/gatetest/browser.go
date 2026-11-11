package gatetest

import (
	"context"
	"io"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// BrowserReport passes a browser check's own report through to out and keeps
// the cases it reported as failed, so a check the time limit ends still names
// a case that failed before it.
//
// The pick runs the browser specs with Playwright's list reporter, for the
// reader, and with the gate's own reporter, the board's tests/gate-cases.ts,
// which writes a row as a try ends whose case is not what it was expected to
// be, and a row when a retry then passes it:
//
//	ceo gate case: unexpected [board] tests/afk.spec.ts:85:7 › a title
//	ceo gate case: flaky [board] tests/afk.spec.ts:85:7 › a title
//
// so a case is what its last row said. The list reporter marks its row of a
// case by the case's status alone, an x for a case declared with test.fail
// that failed as expected, so its rows cannot say which case failed (issue
// 1587). On 2026-10-27 a prepush run of 141 specs reported three assertion
// failures, 465 of 1603 cases ran, the time limit ended it, and the run said
// it passed what it ran (issue 1542). A row is read only in that shape, so no
// other line of a check's output can be taken for a case.
//
// A row is read only until the check's time ends. Ending the check ends its
// processes, the browser and the worker running a case among them, and
// Playwright closes that case as failed when either goes before its runner
// does, so the reporter writes a row for it. That row is the end's doing: a
// prepush whose 17 cases had all passed failed the push on case 18, which the
// limit had ended and which passes by itself (issue 1927). The check's
// processes are ended only after its time has, so no row read before that
// can be one of these, and a row that arrives after it is left to CI with
// the cases that never ran.
type BrowserReport struct {
	out io.Writer
	// ended is closed when the check's time ends.
	ended <-chan struct{}

	mu sync.Mutex
	// carry is the row a Write left without its newline, which the next one
	// finishes, and which Failed reads when the run wrote no newline after it.
	carry string
	// failed holds the cases whose last row said unexpected, in the order
	// they failed.
	failed []string
}

// browserCase matches one row of the gate's own reporter: what the case is
// so far, and its name, which is its project, its file, its line and its
// titles.
var browserCase = regexp.MustCompile(`^ceo gate case: (unexpected|flaky) (\[[^\]]*\] \S.*)$`)

// NewBrowserReport keeps the cases out reports as failed until ctx, the
// context of the check's run, ends, and passes everything it is given on to
// out.
func NewBrowserReport(ctx context.Context, out io.Writer) *BrowserReport {
	return &BrowserReport{out: out, ended: ctx.Done()}
}

// Write passes p to out and reads the rows it holds, unless the check's time
// has ended. A row that end finds unfinished is not read either.
func (r *BrowserReport) Write(p []byte) (int, error) {
	written, err := r.out.Write(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.ended:
		r.carry = ""
		return written, err
	default:
	}
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
	return slices.Clone(r.failed)
}

// note reads one line of the report as the row of a case, if it is one. Its
// caller holds the lock.
func (r *BrowserReport) note(line string) {
	match := browserCase.FindStringSubmatch(strings.TrimRight(line, "\r"))
	if match == nil {
		return
	}
	name := match[2]
	r.failed = slices.DeleteFunc(r.failed, func(failed string) bool { return failed == name })
	if match[1] == "unexpected" {
		r.failed = append(r.failed, name)
	}
}
