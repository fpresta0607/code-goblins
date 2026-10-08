package gatetest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// mixedRun is what go test -json wrote for four packages: c does not compile,
// d has no test files, a has a failing test with a failing subtest beside a
// passing and a skipped one, and b passes.
const mixedRun = `{"ImportPath":"example.com/lab/c [example.com/lab/c.test]","Action":"build-output","Output":"# example.com/lab/c [example.com/lab/c.test]\n"}
{"ImportPath":"example.com/lab/c [example.com/lab/c.test]","Action":"build-output","Output":"c\\c.go:3:23: cannot use \"not an int\" (untyped string constant) as int value in return statement\n"}
{"ImportPath":"example.com/lab/c [example.com/lab/c.test]","Action":"build-fail"}
{"Time":"2026-10-02T20:25:00Z","Action":"start","Package":"example.com/lab/a"}
{"Time":"2026-10-02T20:25:00Z","Action":"start","Package":"example.com/lab/b"}
{"Time":"2026-10-02T20:25:00Z","Action":"start","Package":"example.com/lab/c"}
{"Time":"2026-10-02T20:25:00Z","Action":"output","Package":"example.com/lab/c","Output":"FAIL\texample.com/lab/c [build failed]\n"}
{"Time":"2026-10-02T20:25:00Z","Action":"fail","Package":"example.com/lab/c","Elapsed":0.001,"FailedBuild":"example.com/lab/c [example.com/lab/c.test]"}
{"Time":"2026-10-02T20:25:00Z","Action":"start","Package":"example.com/lab/d"}
{"Time":"2026-10-02T20:25:00Z","Action":"output","Package":"example.com/lab/d","Output":"?   \texample.com/lab/d\t[no test files]\n"}
{"Time":"2026-10-02T20:25:00Z","Action":"skip","Package":"example.com/lab/d","Elapsed":0}
{"Time":"2026-10-02T20:25:01Z","Action":"run","Package":"example.com/lab/b","Test":"TestOK"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Output":"a line from TestMain\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"run","Package":"example.com/lab/a","Test":"TestPasses"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Test":"TestPasses","Output":"=== RUN   TestPasses\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Test":"TestPasses","Output":"    a_test.go:14: logged by a passing test\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Test":"TestPasses","Output":"--- PASS: TestPasses (0.00s)\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"pass","Package":"example.com/lab/a","Test":"TestPasses","Elapsed":0}
{"Time":"2026-10-02T20:25:01Z","Action":"run","Package":"example.com/lab/a","Test":"TestFails"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Test":"TestFails","Output":"=== RUN   TestFails\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Test":"TestFails","Output":"printed by the failing test\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"run","Package":"example.com/lab/a","Test":"TestFails/sub"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Test":"TestFails/sub","Output":"=== RUN   TestFails/sub\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Test":"TestFails/sub","Output":"    a_test.go:18: the sub fails\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Test":"TestFails/sub","Output":"--- FAIL: TestFails/sub (0.00s)\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"fail","Package":"example.com/lab/a","Test":"TestFails/sub","Elapsed":0}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Test":"TestFails","Output":"--- FAIL: TestFails (0.00s)\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"fail","Package":"example.com/lab/a","Test":"TestFails","Elapsed":0}
{"Time":"2026-10-02T20:25:01Z","Action":"run","Package":"example.com/lab/a","Test":"TestSkips"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Test":"TestSkips","Output":"=== RUN   TestSkips\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Test":"TestSkips","Output":"    a_test.go:21: not here\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Test":"TestSkips","Output":"--- SKIP: TestSkips (0.00s)\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"skip","Package":"example.com/lab/a","Test":"TestSkips","Elapsed":0}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Output":"FAIL\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"output","Package":"example.com/lab/a","Output":"FAIL\texample.com/lab/a\t1.895s\n"}
{"Time":"2026-10-02T20:25:01Z","Action":"fail","Package":"example.com/lab/a","Elapsed":1.945}
{"Time":"2026-10-02T20:25:02Z","Action":"output","Package":"example.com/lab/b","Test":"TestOK","Output":"=== RUN   TestOK\n"}
{"Time":"2026-10-02T20:25:02Z","Action":"output","Package":"example.com/lab/b","Test":"TestOK","Output":"--- PASS: TestOK (0.00s)\n"}
{"Time":"2026-10-02T20:25:02Z","Action":"pass","Package":"example.com/lab/b","Test":"TestOK","Elapsed":0}
{"Time":"2026-10-02T20:25:02Z","Action":"output","Package":"example.com/lab/b","Output":"PASS\n"}
{"Time":"2026-10-02T20:25:02Z","Action":"output","Package":"example.com/lab/b","Output":"ok  \texample.com/lab/b\t1.389s\n"}
{"Time":"2026-10-02T20:25:02Z","Action":"pass","Package":"example.com/lab/b","Elapsed":1.408}
`

// read feeds a stream of events to a reader and returns it with the short
// and the full output it wrote.
func read(t *testing.T, stream string) (events *Events, short, full string) {
	t.Helper()
	var shortOutput, fullOutput bytes.Buffer
	events = NewEvents(&shortOutput, &fullOutput)
	if _, err := events.Write([]byte(stream)); err != nil {
		t.Fatal(err)
	}
	if err := events.End(); err != nil {
		t.Fatal(err)
	}
	return events, shortOutput.String(), fullOutput.String()
}

// The step's own output stays what go test prints without -v: a compile
// error as it arrives, then for each package as it ends its summary line,
// and for a package that failed what it printed itself and what its failed
// tests printed. A passing test's lines and a passing package's own prints
// are left to the full output.
func TestEventsPrintsWhatGoTestPrintsWithoutVerbose(t *testing.T) {
	// Act
	_, short, _ := read(t, mixedRun)

	// Assert
	want := "# example.com/lab/c [example.com/lab/c.test]\n" +
		"c\\c.go:3:23: cannot use \"not an int\" (untyped string constant) as int value in return statement\n" +
		"FAIL\texample.com/lab/c [build failed]\n" +
		"?   \texample.com/lab/d\t[no test files]\n" +
		"a line from TestMain\n" +
		"printed by the failing test\n" +
		"    a_test.go:18: the sub fails\n" +
		"--- FAIL: TestFails/sub (0.00s)\n" +
		"--- FAIL: TestFails (0.00s)\n" +
		"FAIL\n" +
		"FAIL\texample.com/lab/a\t1.895s\n" +
		"ok  \texample.com/lab/b\t1.389s\n"
	if short != want {
		t.Errorf("the short output is\n%s\nwant\n%s", short, want)
	}
}

// The full output is every line the build and the tests wrote, in the order
// they wrote it, as go test -v prints it: the log a failure is read from, and
// each test's own time.
func TestEventsKeepsEveryLineInTheFullOutput(t *testing.T) {
	// Act
	_, _, full := read(t, mixedRun)

	// Assert
	for _, want := range []string{
		"c\\c.go:3:23: cannot use",
		"=== RUN   TestPasses\n    a_test.go:14: logged by a passing test\n--- PASS: TestPasses (0.00s)\n",
		"--- SKIP: TestSkips (0.00s)\n",
		"PASS\nok  \texample.com/lab/b\t1.389s\n",
	} {
		if !strings.Contains(full, want) {
			t.Errorf("the full output lacks %q:\n%s", want, full)
		}
	}
	if lines, carried := strings.Count(full, "\n"), strings.Count(mixedRun, `"Output":`); lines != carried || carried < 20 {
		t.Errorf("the full output holds %d lines, want the %d the stream carried:\n%s", lines, carried, full)
	}
}

// Each package gets a result of its own: what became of it, how long it
// took, how many tests it ran and which failed.
func TestEventsGivesEachPackageItsResult(t *testing.T) {
	// Act
	events, _, _ := read(t, mixedRun)

	// Assert
	want := []PackageResult{
		{ImportPath: "example.com/lab/a", Status: "failed", Seconds: 1.945, Tests: 3, Failed: []string{"TestFails", "TestFails/sub"}},
		{ImportPath: "example.com/lab/b", Status: "passed", Seconds: 1.408, Tests: 1},
		{ImportPath: "example.com/lab/c", Status: "build_failed", Seconds: 0.001},
		{ImportPath: "example.com/lab/d", Status: "no_tests"},
	}
	got := events.Results()
	if len(got) != len(want) {
		t.Fatalf("results = %+v, want %+v", got, want)
	}
	for index := range want {
		if got[index].ImportPath != want[index].ImportPath || got[index].Status != want[index].Status || got[index].Seconds != want[index].Seconds ||
			got[index].Tests != want[index].Tests || !slices.Equal(got[index].Failed, want[index].Failed) || len(got[index].Unfinished) != 0 {
			t.Errorf("result %d = %+v, want %+v", index, got[index], want[index])
		}
	}
}

// A package whose tests were still running when the stream ended, because
// the run was stopped or the test binary died, did not pass: its result names
// the tests that never finished, and the short output keeps what they wrote.
func TestEventsNamesTheTestsAPackageNeverFinished(t *testing.T) {
	// Arrange
	stream := `{"Time":"2026-10-02T20:25:00Z","Action":"start","Package":"example.com/lab/hang"}
{"Time":"2026-10-02T20:25:00Z","Action":"run","Package":"example.com/lab/hang","Test":"TestQuick"}
{"Time":"2026-10-02T20:25:00Z","Action":"output","Package":"example.com/lab/hang","Test":"TestQuick","Output":"=== RUN   TestQuick\n"}
{"Time":"2026-10-02T20:25:00Z","Action":"output","Package":"example.com/lab/hang","Test":"TestQuick","Output":"--- PASS: TestQuick (0.00s)\n"}
{"Time":"2026-10-02T20:25:00Z","Action":"pass","Package":"example.com/lab/hang","Test":"TestQuick","Elapsed":0}
{"Time":"2026-10-02T20:25:00Z","Action":"run","Package":"example.com/lab/hang","Test":"TestHangs"}
{"Time":"2026-10-02T20:25:00Z","Action":"output","Package":"example.com/lab/hang","Test":"TestHangs","Output":"=== RUN   TestHangs\n"}
{"Time":"2026-10-02T20:25:00Z","Action":"output","Package":"example.com/lab/hang","Test":"TestHangs","Output":"    hang_test.go:11: about to hang\n"}
{"Time":"2026-10-02T20:25:00Z","Action":"start","Package":"example.com/lab/later"}
`

	// Act
	events, short, _ := read(t, stream)

	// Assert
	results := events.Results()
	if len(results) != 2 || results[0].Status != "unfinished" || !slices.Equal(results[0].Unfinished, []string{"TestHangs"}) || results[0].Tests != 2 {
		t.Fatalf("results = %+v, want example.com/lab/hang unfinished with TestHangs named, of 2 tests", results)
	}
	if results[1].ImportPath != "example.com/lab/later" || results[1].Status != "unfinished" || len(results[1].Unfinished) != 0 {
		t.Errorf("results[1] = %+v, want example.com/lab/later unfinished with no test named", results[1])
	}
	if want := "    hang_test.go:11: about to hang\n"; !strings.Contains(short, want) || strings.Contains(short, "TestQuick") {
		t.Errorf("the short output is %q; want what the unfinished test wrote, and nothing of the test that passed", short)
	}
}

// A test that ran into go test's timeout gets no event of its own: the test
// binary panics and the package fails. Such a test is named as unfinished in
// a failed package, and the short output keeps the panic that names it.
func TestEventsNamesTheTestAPackageTimedOutIn(t *testing.T) {
	// Arrange: what go test -json -timeout 3s wrote for a test that sleeps.
	stream := `{"Time":"2026-10-02T20:25:00Z","Action":"start","Package":"example.com/lab2/hang"}
{"Time":"2026-10-02T20:25:00Z","Action":"run","Package":"example.com/lab2/hang","Test":"TestQuick"}
{"Time":"2026-10-02T20:25:00Z","Action":"output","Package":"example.com/lab2/hang","Test":"TestQuick","Output":"=== RUN   TestQuick\n"}
{"Time":"2026-10-02T20:25:00Z","Action":"output","Package":"example.com/lab2/hang","Test":"TestQuick","Output":"--- PASS: TestQuick (0.00s)\n"}
{"Time":"2026-10-02T20:25:00Z","Action":"pass","Package":"example.com/lab2/hang","Test":"TestQuick","Elapsed":0}
{"Time":"2026-10-02T20:25:00Z","Action":"run","Package":"example.com/lab2/hang","Test":"TestHangs"}
{"Time":"2026-10-02T20:25:00Z","Action":"output","Package":"example.com/lab2/hang","Test":"TestHangs","Output":"=== RUN   TestHangs\n"}
{"Time":"2026-10-02T20:25:00Z","Action":"output","Package":"example.com/lab2/hang","Test":"TestHangs","Output":"    hang_test.go:11: about to hang\n"}
{"Time":"2026-10-02T20:25:03Z","Action":"output","Package":"example.com/lab2/hang","Test":"TestHangs","Output":"panic: test timed out after 3s\n"}
{"Time":"2026-10-02T20:25:03Z","Action":"output","Package":"example.com/lab2/hang","Test":"TestHangs","Output":"\trunning tests:\n"}
{"Time":"2026-10-02T20:25:03Z","Action":"output","Package":"example.com/lab2/hang","Test":"TestHangs","Output":"\t\tTestHangs (3s)\n"}
{"Time":"2026-10-02T20:25:03Z","Action":"output","Package":"example.com/lab2/hang","Output":"FAIL\texample.com/lab2/hang\t8.467s\n"}
{"Time":"2026-10-02T20:25:03Z","Action":"fail","Package":"example.com/lab2/hang","Elapsed":8.468}
`

	// Act
	events, short, _ := read(t, stream)

	// Assert
	results := events.Results()
	if len(results) != 1 || results[0].Status != "failed" || !slices.Equal(results[0].Unfinished, []string{"TestHangs"}) || len(results[0].Failed) != 0 {
		t.Fatalf("results = %+v, want the package failed with TestHangs unfinished and no test failed", results)
	}
	want := "    hang_test.go:11: about to hang\npanic: test timed out after 3s\n\trunning tests:\n\t\tTestHangs (3s)\nFAIL\texample.com/lab2/hang\t8.467s\n"
	if short != want {
		t.Errorf("the short output is\n%s\nwant\n%s", short, want)
	}
}

// What is running, and since when, can be asked while the stream is still
// arriving: the packages that started and did not end, each with the tests
// that are running in it, how many packages have ended, and the last line of
// the short output.
func TestEventsSaysWhatIsRunning(t *testing.T) {
	// Arrange
	var short, full bytes.Buffer
	events := NewEvents(&short, &full)
	stream := `{"Time":"2026-10-02T20:25:00Z","Action":"start","Package":"example.com/lab/a"}
{"Time":"2026-10-02T20:25:03Z","Action":"run","Package":"example.com/lab/a","Test":"TestSlow"}
{"Time":"2026-10-02T20:25:03Z","Action":"output","Package":"example.com/lab/a","Test":"TestSlow","Output":"=== RUN   TestSlow\n"}
{"Time":"2026-10-02T20:25:04Z","Action":"start","Package":"example.com/lab/b"}
{"Time":"2026-10-02T20:25:05Z","Action":"output","Package":"example.com/lab/b","Output":"ok  \texample.com/lab/b\t0.5s\n"}
{"Time":"2026-10-02T20:25:05Z","Action":"pass","Package":"example.com/lab/b","Elapsed":0.5}
`

	// Act
	if _, err := events.Write([]byte(stream)); err != nil {
		t.Fatal(err)
	}
	running, done, last := events.Progress()

	// Assert
	since := time.Date(2026, 10, 2, 20, 25, 3, 0, time.UTC)
	if len(running) != 1 || running[0].ImportPath != "example.com/lab/a" || running[0].Test != "TestSlow" || !running[0].Since.Equal(since) {
		t.Errorf("running = %+v, want example.com/lab/a in TestSlow since %s", running, since)
	}
	if done != 1 {
		t.Errorf("done = %d, want the one package that ended", done)
	}
	if want := "ok  \texample.com/lab/b\t0.5s"; last != want {
		t.Errorf("last = %q, want %q, the last line of the short output", last, want)
	}
}

// A package that has started and runs no test yet, as while it compiles or
// between two tests, is still running, since it started; a test with a
// subtest running is left to the subtest, and a test waiting among parallel
// ones is not running.
func TestEventsLeavesATestToItsRunningSubtestAndSkipsOneThatWaits(t *testing.T) {
	// Arrange
	var short, full bytes.Buffer
	events := NewEvents(&short, &full)
	stream := `{"Time":"2026-10-02T20:25:00Z","Action":"start","Package":"example.com/lab/quiet"}
{"Time":"2026-10-02T20:25:01Z","Action":"start","Package":"example.com/lab/a"}
{"Time":"2026-10-02T20:25:02Z","Action":"run","Package":"example.com/lab/a","Test":"TestWaits"}
{"Time":"2026-10-02T20:25:02Z","Action":"pause","Package":"example.com/lab/a","Test":"TestWaits"}
{"Time":"2026-10-02T20:25:03Z","Action":"run","Package":"example.com/lab/a","Test":"TestOuter"}
{"Time":"2026-10-02T20:25:04Z","Action":"run","Package":"example.com/lab/a","Test":"TestOuter/inner"}
`

	// Act
	if _, err := events.Write([]byte(stream)); err != nil {
		t.Fatal(err)
	}
	running, _, _ := events.Progress()

	// Assert
	started := time.Date(2026, 10, 2, 20, 25, 0, 0, time.UTC)
	if len(running) != 2 || running[0].ImportPath != "example.com/lab/quiet" || running[0].Test != "" || !running[0].Since.Equal(started) ||
		running[1].ImportPath != "example.com/lab/a" || running[1].Test != "TestOuter/inner" {
		t.Errorf("running = %+v; want the package that runs no test, since it started, then TestOuter/inner alone", running)
	}
}

// A line that is not an event, which go test can still print, is passed on
// as it is to both outputs and not lost.
func TestEventsPassesOnALineThatIsNotAnEvent(t *testing.T) {
	// Act
	_, short, full := read(t, "go: downloading example.com/x v1.0.0\n{\"Action\":\"start\",\"Package\":\"example.com/lab/a\"}\nnot json at all")

	// Assert
	for name, output := range map[string]string{"short": short, "full": full} {
		if !strings.Contains(output, "go: downloading example.com/x v1.0.0\n") || !strings.Contains(output, "not json at all\n") {
			t.Errorf("the %s output is %q; want both lines that are not events", name, output)
		}
	}
}

// The stream arrives in pieces that end anywhere, and an event split between
// two of them is read as one.
func TestEventsReadsAnEventSplitAcrossWrites(t *testing.T) {
	// Arrange
	var whole, pieces, full bytes.Buffer
	if _, err := NewEvents(&whole, &full).Write([]byte(mixedRun)); err != nil {
		t.Fatal(err)
	}
	events := NewEvents(&pieces, &full)

	// Act
	for start := 0; start < len(mixedRun); start += 7 {
		if _, err := events.Write([]byte(mixedRun[start:min(start+7, len(mixedRun))])); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	if !strings.Contains(whole.String(), "ok  \texample.com/lab/b\t1.389s\n") {
		t.Fatalf("one write gives %q, which is not the run's short output, so the comparison proves nothing", whole.String())
	}
	if pieces.String() != whole.String() {
		t.Errorf("read in pieces the short output is\n%s\nwant what one write gives:\n%s", pieces.String(), whole.String())
	}
}

type eventsOutputWriter func([]byte) (int, error)

func (write eventsOutputWriter) Write(p []byte) (int, error) { return write(p) }

func TestEventsReportsEitherOutputWriteFailure(t *testing.T) {
	for name, stream := range map[string]string{
		"ordinary output": "go: downloading example.com/x v1.0.0\n",
		"build output":    `{"Action":"build-output","Output":"a/a.go:3: bad build\n"}` + "\n",
		"passing package": `{"Action":"output","Package":"example.com/m/a","Output":"ok example.com/m/a\n"}` + "\n" +
			`{"Action":"pass","Package":"example.com/m/a","Elapsed":1}` + "\n",
		"failing package": `{"Action":"output","Package":"example.com/m/a","Output":"FAIL example.com/m/a\n"}` + "\n" +
			`{"Action":"fail","Package":"example.com/m/a","Elapsed":1}` + "\n",
	} {
		for _, output := range []string{"short", "full"} {
			for _, isShortWrite := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/short_write=%t", name, output, isShortWrite), func(t *testing.T) {
					// Arrange
					failure := errors.New("output unavailable")
					if isShortWrite {
						failure = io.ErrShortWrite
					}
					broken := eventsOutputWriter(func(p []byte) (int, error) {
						if isShortWrite {
							return len(p) - 1, nil
						}
						return 0, failure
					})
					var short, full io.Writer = io.Discard, io.Discard
					if output == "short" {
						short = broken
					} else {
						full = broken
					}
					events := NewEvents(short, full)

					// Act
					_, err := events.Write([]byte(stream))

					// Assert
					if !errors.Is(err, failure) {
						t.Errorf("Write error = %v, want %v", err, failure)
					}
					if _, _, last := events.Progress(); output == "short" && last != "" {
						t.Errorf("last output = %q after its write failed, want none", last)
					}
					if n, err := events.Write([]byte("later output\n")); n != 0 || !errors.Is(err, failure) {
						t.Errorf("later Write = %d, %v, want 0 and the original output failure", n, err)
					}
				})
			}
		}
	}
}

func TestEventsKeepsAnOutputFailureWhileEndingTheStream(t *testing.T) {
	for name, stream := range map[string]string{
		"last line": "a line without its newline",
		"unfinished package": `{"Action":"start","Package":"example.com/m/a"}` + "\n" +
			`{"Action":"run","Package":"example.com/m/a","Test":"TestWaits"}` + "\n" +
			`{"Action":"output","Package":"example.com/m/a","Test":"TestWaits","Output":"waiting\n"}` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			failure := errors.New("output unavailable at end")
			events := NewEvents(eventsOutputWriter(func([]byte) (int, error) { return 0, failure }), io.Discard)
			if _, err := events.Write([]byte(stream)); err != nil {
				t.Fatalf("Write before End: %v", err)
			}

			// Act
			err := events.End()

			// Assert
			if !errors.Is(err, failure) {
				t.Errorf("End error = %v, want %v", err, failure)
			}
			if _, err := events.Write(nil); !errors.Is(err, failure) {
				t.Errorf("Write after End error = %v, want %v", err, failure)
			}
		})
	}
}

func TestEventsReportsAClosedOutputWhileFlushingTheLastLine(t *testing.T) {
	for _, output := range []string{"short", "full"} {
		t.Run(output, func(t *testing.T) {
			// Arrange
			closed, err := os.Create(filepath.Join(t.TempDir(), "output.log"))
			if err != nil {
				t.Fatal(err)
			}
			if err := closed.Close(); err != nil {
				t.Fatal(err)
			}
			var short, full io.Writer = io.Discard, io.Discard
			if output == "short" {
				short = closed
			} else {
				full = closed
			}
			events := NewEvents(short, full)
			if _, err := events.Write([]byte("last line")); err != nil {
				t.Fatalf("Write before End: %v", err)
			}

			// Act
			err = events.End()

			// Assert
			if !errors.Is(err, os.ErrClosed) {
				t.Errorf("End error = %v, want a closed file error from %s", err, output)
			}
		})
	}
}
