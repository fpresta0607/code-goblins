// Command citest runs a CI job's Go tests, and runs a test that failed once
// more before the job fails. A test can fail by chance on a loaded runner: a
// key's echo that waits for a core, a process listing read between two
// starts. Such a failure used to fail the whole run, and the run after it
// passed with nothing changed. So a test that failed runs again, once, by its
// exact name and with the runner to itself: one that passes then is recorded
// as having failed once, by job, package and name, and the job passes; one
// that fails again fails the job as before.
//
// Only a failure go test names in full gets a second try. A package that did
// not compile, whose tests were cut short (a panic or a timeout leaves the
// tests after it unrun, so running the named one again would pass over them),
// that failed with no test named, or that reported nothing at all fails the
// job as it stands. A second try counts only when every test it names ran
// and passed: one that ran fewer tests than it named proves nothing.
//
// Usage, from the repository root, with the packages as go list prints them:
//
//	go run ./tools/citest -job "go (rest)" -record failed-once.jsonl -timeout 20m <import path>...
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/gatetest"
)

// options are what a job asks for.
type options struct {
	// job is the job's name and record the file each test that failed once
	// is appended to, one JSON object to a line.
	job, record string
	// run and skip are the patterns the job's share of a package is chosen
	// by, as go test's -run and -skip take them, and timeout its -timeout.
	run, skip, timeout string
	// packages are the import paths the job tests.
	packages []string
}

// failedOnce is one test that failed and passed when it ran again, as the
// record keeps it. The browser jobs write the same shape, with the spec file
// as the suite.
type failedOnce struct {
	Job   string `json:"job"`
	Suite string `json:"suite"`
	Test  string `json:"test"`
}

// goTest runs go test -json with args, writing what it prints to stdout. The
// error is for a go that could not run at all: a failing test is no error.
type goTest func(args []string, stdout io.Writer) error

func main() {
	var opts options
	flag.StringVar(&opts.job, "job", "", "the job's name, which the record gives each test that failed once")
	flag.StringVar(&opts.record, "record", "", "the file each test that failed once is appended to")
	flag.StringVar(&opts.run, "run", "", "run only the tests this pattern matches, as go test -run")
	flag.StringVar(&opts.skip, "skip", "", "skip the tests this pattern matches, as go test -skip")
	flag.StringVar(&opts.timeout, "timeout", "20m", "how long one package's tests may take, as go test -timeout")
	flag.Parse()
	opts.packages = flag.Args()
	passed, err := run(opts, osGoTest, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "citest:", err)
	}
	if err != nil || !passed {
		os.Exit(1)
	}
}

// osGoTest runs the real go test in the directory the tool was started in.
func osGoTest(args []string, stdout io.Writer) error {
	process := execx.Command("go", append([]string{"test", "-json"}, args...)...)
	process.Stdout, process.Stderr = stdout, os.Stderr
	// A go test that ran and exited with a failure is read from its events.
	if err := process.Run(); process.ProcessState == nil {
		return err
	}
	return nil
}

// run tests the job's packages and gives each fully named failure its second
// try. It reports whether the job passes.
func run(opts options, test goTest, stdout io.Writer) (bool, error) {
	if len(opts.packages) == 0 {
		return false, errors.New("no package to test")
	}
	first, err := try(test, opts.args("", opts.packages), stdout)
	if err != nil {
		return false, err
	}
	passed := true
	for _, importPath := range opts.packages {
		result, reported := first[importPath]
		switch {
		case !reported:
			passed = false
			fmt.Fprintf(stdout, "citest: %s reported no result, so its tests did not all run and the failure stands\n", importPath)
		case result.Status == "passed" || result.Status == "no_tests":
		case result.Status != "failed" || len(result.Unfinished) > 0 || len(result.Failed) == 0:
			passed = false
			fmt.Fprintf(stdout, "citest: %s %s, which no second try of a named test can answer, so the failure stands\n", importPath, describe(result))
		default:
			again, err := opts.secondTry(test, importPath, topLevel(result.Failed), stdout)
			if err != nil {
				return false, err
			}
			passed = passed && again
		}
	}
	return passed, nil
}

// secondTry runs the named tests of one package again and records each that
// passes. It reports whether all of them did.
func (opts options) secondTry(test goTest, importPath string, names []string, stdout io.Writer) (bool, error) {
	fmt.Fprintf(stdout, "citest: %s failed in %s and runs once more\n", strings.Join(names, ", "), importPath)
	quoted := make([]string, len(names))
	for index, name := range names {
		quoted[index] = regexp.QuoteMeta(name)
	}
	results, err := try(test, opts.args("^("+strings.Join(quoted, "|")+")$", []string{importPath}), stdout)
	if err != nil {
		return false, err
	}
	result := results[importPath]
	failedTwice := topLevel(append(result.Failed, result.Unfinished...))
	for _, name := range names {
		if slices.Contains(failedTwice, name) {
			fmt.Fprintf(stdout, "citest: failed twice: %s %s\n", importPath, name)
			continue
		}
		// A second try that ran fewer tests than it named cannot say which
		// of them it ran, so none of them is taken as passed.
		if result.Tests != len(names) {
			continue
		}
		fmt.Fprintf(stdout, "citest: failed once and passed on the second try: %s %s\n", importPath, name)
		if err := opts.keep(failedOnce{Job: opts.job, Suite: importPath, Test: name}); err != nil {
			return false, err
		}
	}
	if result.Tests != len(names) {
		fmt.Fprintf(stdout, "citest: the second try of %s ran %d of the %d tests it named, so the failure stands\n", importPath, result.Tests, len(names))
		return false, nil
	}
	return result.Status == "passed" && len(failedTwice) == 0, nil
}

// args are go test's arguments for packages: the job's own share of them, or
// with only the tests named.
func (opts options) args(only string, packages []string) []string {
	args := []string{"-count=1", "-timeout", opts.timeout}
	switch {
	case only != "":
		args = append(args, "-run", only)
	default:
		if opts.run != "" {
			args = append(args, "-run", opts.run)
		}
		if opts.skip != "" {
			args = append(args, "-skip", opts.skip)
		}
	}
	return append(args, packages...)
}

// keep appends one test that failed once to the record.
func (opts options) keep(entry failedOnce) error {
	if opts.record == "" {
		return nil
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := fsx.OpenAppend(opts.record, 0o644)
	if err != nil {
		return fmt.Errorf("keep %s %s in the record: %w", entry.Suite, entry.Test, err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		file.Close()
		return fmt.Errorf("keep %s %s in the record: %w", entry.Suite, entry.Test, err)
	}
	return file.Close()
}

// try runs go test once and returns each package's result by import path,
// printing what go test prints without -v.
func try(test goTest, args []string, stdout io.Writer) (map[string]gatetest.PackageResult, error) {
	events := gatetest.NewEvents(stdout, io.Discard)
	if err := test(args, events); err != nil {
		return nil, fmt.Errorf("go test could not run: %w", err)
	}
	if err := events.End(); err != nil {
		return nil, err
	}
	results := map[string]gatetest.PackageResult{}
	for _, result := range events.Results() {
		results[result.ImportPath] = result
	}
	return results, nil
}

// topLevel are the tests among names, each once and in order: a subtest that
// failed names its test, which is what go test -run can choose exactly.
func topLevel(names []string) []string {
	var tests []string
	for _, name := range names {
		if test, _, _ := strings.Cut(name, "/"); !slices.Contains(tests, test) {
			tests = append(tests, test)
		}
	}
	return tests
}

// describe says how a package that gets no second try ended.
func describe(result gatetest.PackageResult) string {
	switch {
	case result.Status == "build_failed":
		return "did not compile"
	case len(result.Unfinished) > 0:
		return "was cut short with " + strings.Join(topLevel(result.Unfinished), ", ") + " unfinished"
	case result.Status == "unfinished":
		return "never ended"
	}
	return "failed with no test named"
}
