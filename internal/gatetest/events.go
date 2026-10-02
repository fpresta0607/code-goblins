package gatetest

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"
)

// PackageResult is what became of one package's tests in a run of go test.
type PackageResult struct {
	ImportPath string
	// Status is passed, failed, build_failed when the package or its tests
	// did not compile, no_tests when it has no test files, or unfinished when
	// its tests were still running as the run ended.
	Status  string
	Seconds float64
	// Tests is how many top-level tests started. Failed names the tests and
	// subtests that failed, and Unfinished the ones that started and never
	// ended, which is how a test that hangs shows.
	Tests      int
	Failed     []string
	Unfinished []string
}

// Running is a package whose tests have started and not ended, with one of
// the tests running in it and when that test started. Test is empty while no
// test runs, and Since is then when the package started.
type Running struct {
	ImportPath string
	Test       string
	Since      time.Time
}

// Events reads what go test -json writes. It is the io.Writer go test's
// standard output goes to. To short it writes what go test prints without
// -v: a compile error as it arrives, and for each package, as it ends, its
// summary line, with what the package and its failed or unfinished tests
// wrote when it did not pass. To full it writes every line, as go test -v
// prints them. It keeps a result for each package.
type Events struct {
	mu          sync.Mutex
	short, full io.Writer
	partial     []byte
	packages    map[string]*packageRun
	order       []string
	// last is the last line written to short that says something.
	last string
}

type packageRun struct {
	result PackageResult
	since  time.Time
	ended  bool
	// lines are what the package and its tests wrote, in order, until the
	// package ends.
	lines []written
	tests map[string]*testRun
	names []string
}

type written struct{ test, text string }

type testRun struct {
	since time.Time
	// outcome is pass, fail or skip, and empty while the test has not ended.
	outcome string
	paused  bool
}

// NewEvents returns a reader of go test -json's output that writes the short
// output to short and the full output to full.
func NewEvents(short, full io.Writer) *Events {
	return &Events{short: short, full: full, packages: map[string]*packageRun{}}
}

// Write takes the next piece of go test's output, which can end anywhere in
// a line.
func (e *Events) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.partial = append(e.partial, p...)
	for {
		end := bytes.IndexByte(e.partial, '\n')
		if end < 0 {
			return len(p), nil
		}
		e.line(e.partial[:end])
		e.partial = e.partial[end+1:]
	}
}

// End says the output is over: a last line with no newline is read, and
// every package that started and did not end is unfinished, with what it
// wrote kept as a failed package's is.
func (e *Events) End() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.partial) > 0 {
		e.line(e.partial)
		e.partial = nil
	}
	for _, name := range e.order {
		if run := e.packages[name]; !run.ended {
			e.end(run, "unfinished", 0)
		}
	}
}

// line reads one line of the output: an event, or a line go test printed
// some other way, which goes to both outputs as it is.
func (e *Events) line(line []byte) {
	var event struct {
		Time        time.Time
		Action      string
		Package     string
		ImportPath  string
		Test        string
		Output      string
		Elapsed     float64
		FailedBuild string
	}
	if err := json.Unmarshal(line, &event); err != nil || event.Action == "" {
		text := strings.TrimSuffix(string(line), "\r") + "\n"
		e.say(text)
		io.WriteString(e.full, text)
		return
	}
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	if event.Package == "" {
		// A build's output belongs to no package's tests: it is printed as it
		// arrives, as go test prints it.
		if event.Action == "build-output" {
			e.say(event.Output)
			io.WriteString(e.full, event.Output)
		}
		return
	}
	run := e.packages[event.Package]
	if run == nil {
		run = &packageRun{result: PackageResult{ImportPath: event.Package}, since: event.Time, tests: map[string]*testRun{}}
		e.packages[event.Package] = run
		e.order = append(e.order, event.Package)
	}
	test := run.tests[event.Test]
	switch event.Action {
	case "output":
		io.WriteString(e.full, event.Output)
		if !run.ended {
			run.lines = append(run.lines, written{event.Test, event.Output})
		}
	case "run":
		run.tests[event.Test] = &testRun{since: event.Time}
		run.names = append(run.names, event.Test)
		if !strings.Contains(event.Test, "/") {
			run.result.Tests++
		}
	case "pause", "cont":
		if test != nil {
			test.paused = event.Action == "pause"
		}
	case "pass", "fail", "skip":
		switch {
		case event.Test != "":
			if test != nil {
				test.outcome = event.Action
			}
		case event.Action == "pass":
			e.end(run, "passed", event.Elapsed)
		case event.Action == "skip":
			e.end(run, "no_tests", event.Elapsed)
		case event.FailedBuild != "":
			e.end(run, "build_failed", event.Elapsed)
		default:
			e.end(run, "failed", event.Elapsed)
		}
	}
}

// end records what became of a package and writes its part of the short
// output: its summary line, and when it did not pass everything it wrote
// itself and everything its failed and unfinished tests wrote.
func (e *Events) end(run *packageRun, status string, seconds float64) {
	run.ended, run.result.Status, run.result.Seconds = true, status, seconds
	shown := map[string]bool{"": true}
	for _, name := range run.names {
		switch run.tests[name].outcome {
		case "fail":
			run.result.Failed = append(run.result.Failed, name)
			shown[name] = true
		case "":
			run.result.Unfinished = append(run.result.Unfinished, name)
			shown[name] = true
		}
	}
	if status == "passed" || status == "no_tests" {
		for index := len(run.lines) - 1; index >= 0; index-- {
			if run.lines[index].test == "" {
				e.say(run.lines[index].text)
				break
			}
		}
	} else {
		for _, line := range run.lines {
			if shown[line.test] && !strings.HasPrefix(line.text, "=== ") {
				e.say(line.text)
			}
		}
	}
	run.lines = nil
}

// say writes text to the short output and keeps its last line that says
// something.
func (e *Events) say(text string) {
	io.WriteString(e.short, text)
	if line := strings.TrimSpace(text); line != "" {
		e.last = line
	}
}

// Results are the packages' results, in the order the packages started.
func (e *Events) Results() []PackageResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	results := make([]PackageResult, 0, len(e.order))
	for _, name := range e.order {
		results = append(results, e.packages[name].result)
	}
	return results
}

// Progress says where the run is: the packages that started and did not end,
// each with the tests running in it, how many packages have ended, and the
// last line of the short output. A test with a subtest running is left to the
// subtest, and a test waiting for its turn among parallel ones is not
// running.
func (e *Events) Progress() (running []Running, done int, last string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, name := range e.order {
		run := e.packages[name]
		if run.ended {
			done++
			continue
		}
		found := false
		for _, test := range run.names {
			state := run.tests[test]
			if state.outcome != "" || state.paused {
				continue
			}
			inner := false
			for _, other := range run.names {
				inner = inner || strings.HasPrefix(other, test+"/") && run.tests[other].outcome == "" && !run.tests[other].paused
			}
			if !inner {
				running = append(running, Running{ImportPath: name, Test: test, Since: state.since})
				found = true
			}
		}
		if !found {
			running = append(running, Running{ImportPath: name, Since: run.since})
		}
	}
	return running, done, e.last
}
