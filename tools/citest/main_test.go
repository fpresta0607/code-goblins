package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	spawn = "example.test/internal/spawn"
	train = "example.test/internal/train"
)

// script stands in for go test: each run of it writes the next of its
// outputs, as go test -json would, and keeps the arguments it was given.
type script struct {
	outputs []string
	ran     [][]string
}

func (s *script) test(args []string, stdout io.Writer) error {
	s.ran = append(s.ran, args)
	if len(s.ran) > len(s.outputs) {
		return fmt.Errorf("go test ran %d times, and the script has %d outputs", len(s.ran), len(s.outputs))
	}
	_, err := io.WriteString(stdout, s.outputs[len(s.ran)-1])
	return err
}

// event is one line of go test -json about a test of a package, or about the
// package itself when test is empty.
func event(action, importPath, test string) string {
	if test == "" {
		return fmt.Sprintf(`{"Action":%q,"Package":%q,"Elapsed":1}`+"\n", action, importPath)
	}
	return fmt.Sprintf(`{"Action":%q,"Package":%q,"Test":%q}`+"\n", action, importPath, test)
}

// tests is a package's run in which each named test ended as said, and the
// package failed when one of them did.
func tests(importPath string, outcomes ...string) string {
	var out strings.Builder
	result := "pass"
	for index := 0; index < len(outcomes); index += 2 {
		name, outcome := outcomes[index], outcomes[index+1]
		out.WriteString(event("run", importPath, name))
		if outcome == "" {
			result = "fail"
			continue
		}
		out.WriteString(event(outcome, importPath, name))
		if outcome == "fail" {
			result = "fail"
		}
	}
	return out.String() + event(result, importPath, "")
}

// recorded reads the record a run left, one line to a test, or nothing when
// it left none.
func recorded(t *testing.T, record string) []string {
	t.Helper()
	data, err := os.ReadFile(record)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestATestThatFailsOnceRunsAgainByNameAndIsRecorded(t *testing.T) {
	// Arrange: two tests of one package fail, one of them in a subtest, and
	// both pass when they run again.
	record := filepath.Join(t.TempDir(), "failed-once.jsonl")
	opts := options{job: "go (spawn-1)", record: record, run: "Spawn", timeout: "20m", packages: []string{spawn, train}}
	goTest := &script{outputs: []string{
		tests(spawn, "TestSpawnStarts", "fail", "TestSpawnWaits/once", "fail", "TestSpawnWaits", "fail", "TestSpawnEnds", "pass") + tests(train, "TestTrainLands", "pass"),
		tests(spawn, "TestSpawnStarts", "pass", "TestSpawnWaits", "pass"),
	}}
	var out bytes.Buffer

	// Act
	passed, err := run(opts, goTest.test, &out)

	// Assert
	if err != nil || !passed {
		t.Fatalf("run = %t, %v, want the job to pass:\n%s", passed, err, &out)
	}
	wantRuns := [][]string{
		{"-count=1", "-timeout", "20m", "-run", "Spawn", spawn, train},
		{"-count=1", "-timeout", "20m", "-run", "^(TestSpawnStarts|TestSpawnWaits)$", spawn},
	}
	if !slices.EqualFunc(goTest.ran, wantRuns, slices.Equal[[]string]) {
		t.Errorf("go test ran with %q, want the job's share and then the two failed tests alone: %q", goTest.ran, wantRuns)
	}
	want := []string{
		`{"job":"go (spawn-1)","suite":"example.test/internal/spawn","test":"TestSpawnStarts"}`,
		`{"job":"go (spawn-1)","suite":"example.test/internal/spawn","test":"TestSpawnWaits"}`,
	}
	if got := recorded(t, record); !slices.Equal(got, want) {
		t.Errorf("the record holds %q, want %q", got, want)
	}
}

func TestATestThatFailsTwiceFailsTheJob(t *testing.T) {
	// Arrange: of two failed tests, one fails again.
	record := filepath.Join(t.TempDir(), "failed-once.jsonl")
	opts := options{job: "go (rest)", record: record, timeout: "20m", packages: []string{train}}
	goTest := &script{outputs: []string{
		tests(train, "TestTrainLands", "fail", "TestTrainHalves", "fail"),
		tests(train, "TestTrainLands", "pass", "TestTrainHalves", "fail"),
	}}
	var out bytes.Buffer

	// Act
	passed, err := run(opts, goTest.test, &out)

	// Assert
	if err != nil || passed {
		t.Fatalf("run = %t, %v, want the job to fail:\n%s", passed, err, &out)
	}
	if !strings.Contains(out.String(), "failed twice: "+train+" TestTrainHalves") {
		t.Errorf("the output does not name the test that failed twice:\n%s", &out)
	}
	want := []string{`{"job":"go (rest)","suite":"example.test/internal/train","test":"TestTrainLands"}`}
	if got := recorded(t, record); !slices.Equal(got, want) {
		t.Errorf("the record holds %q, want the test that failed once and no other: %q", got, want)
	}
}

func TestAJobWhoseTestsAllPassRunsThemOnceAndRecordsNothing(t *testing.T) {
	// Arrange: a job that runs what one pattern leaves of its package.
	record := filepath.Join(t.TempDir(), "failed-once.jsonl")
	opts := options{job: "go (spawn-2)", record: record, skip: "Spawn", timeout: "20m", packages: []string{spawn}}
	goTest := &script{outputs: []string{tests(spawn, "TestBriefs", "pass")}}
	var out bytes.Buffer

	// Act
	passed, err := run(opts, goTest.test, &out)

	// Assert
	if err != nil || !passed {
		t.Fatalf("run = %t, %v, want the job to pass:\n%s", passed, err, &out)
	}
	if want := [][]string{{"-count=1", "-timeout", "20m", "-skip", "Spawn", spawn}}; !slices.EqualFunc(goTest.ran, want, slices.Equal[[]string]) {
		t.Errorf("go test ran with %q, want once: %q", goTest.ran, want)
	}
	if got := recorded(t, record); got != nil {
		t.Errorf("the record holds %q, want no record at all", got)
	}
}

// A failure go test does not name in full gets no second try: running the
// named test again would pass over whatever the first run never reached.
func TestAFailureNotNamedInFullStandsWithNoSecondTry(t *testing.T) {
	for name, first := range map[string]string{
		"a test that never ended, as after a panic":           tests(train, "TestTrainLands", "fail", "TestTrainHalves", ""),
		"a package that did not compile":                      `{"Action":"fail","Package":"` + train + `","Elapsed":0,"FailedBuild":"` + train + `"}` + "\n",
		"a package that failed with no test named":            event("fail", train, ""),
		"a package whose output ended before it did":          event("run", train, "TestTrainLands"),
		"a package that reported nothing at all":              tests(spawn, "TestBriefs", "pass"),
		"a package that reported nothing beside a failed one": tests(spawn, "TestBriefs", "fail"),
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			record := filepath.Join(t.TempDir(), "failed-once.jsonl")
			opts := options{job: "go (rest)", record: record, timeout: "20m", packages: []string{spawn, train}}
			goTest := &script{outputs: []string{first, tests(spawn, "TestBriefs", "pass")}}
			if !strings.Contains(first, spawn) {
				goTest.outputs[0] += tests(spawn, "TestBriefs", "pass")
			}
			var out bytes.Buffer

			// Act
			passed, err := run(opts, goTest.test, &out)

			// Assert
			if err != nil || passed {
				t.Fatalf("run = %t, %v, want the job to fail:\n%s", passed, err, &out)
			}
			if !strings.Contains(out.String(), "the failure stands") {
				t.Errorf("the output does not say the failure stands:\n%s", &out)
			}
			for _, args := range goTest.ran[1:] {
				if slices.Contains(args, train) {
					t.Errorf("go test ran %s again with %q, want no second try of it", train, args)
				}
			}
		})
	}
}

// A second try that ran fewer tests than it named, such as none because no
// name matched, would pass having proved nothing.
func TestASecondTryThatRanFewerTestsThanItNamedDoesNotPass(t *testing.T) {
	for name, second := range map[string]string{
		"no test ran":  event("pass", train, ""),
		"one test ran": tests(train, "TestTrainLands", "pass"),
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			record := filepath.Join(t.TempDir(), "failed-once.jsonl")
			opts := options{job: "go (rest)", record: record, timeout: "20m", packages: []string{train}}
			goTest := &script{outputs: []string{tests(train, "TestTrainLands", "fail", "TestTrainHalves", "fail"), second}}
			var out bytes.Buffer

			// Act
			passed, err := run(opts, goTest.test, &out)

			// Assert
			if err != nil || passed {
				t.Fatalf("run = %t, %v, want the job to fail:\n%s", passed, err, &out)
			}
			if got := recorded(t, record); got != nil {
				t.Errorf("the record holds %q, want nothing taken as passed", got)
			}
		})
	}
}

func TestAGoThatCannotRunIsAnError(t *testing.T) {
	// Arrange
	opts := options{job: "go (rest)", timeout: "20m", packages: []string{train}}
	goTest := &script{}

	// Act
	passed, err := run(opts, goTest.test, io.Discard)

	// Assert
	if err == nil || passed {
		t.Errorf("run = %t, %v, want an error", passed, err)
	}
}

func TestAJobWithNoPackageIsAnError(t *testing.T) {
	// Act
	passed, err := run(options{job: "go (rest)", timeout: "20m"}, (&script{}).test, io.Discard)

	// Assert
	if err == nil || passed {
		t.Errorf("run = %t, %v, want an error: a job that tests nothing must not pass", passed, err)
	}
}
