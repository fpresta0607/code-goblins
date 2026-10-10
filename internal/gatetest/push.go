package gatetest

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// SlowTest is how long a test takes, by this machine's own record, before a
// push leaves it to CI in a slow package. From 2026-10-07 to 2026-10-08 the
// tests under it were 81 to 88 percent of the tests of cmd/cfo and
// internal/supervisor and 9 to 28 percent of their time.
const SlowTest = 2 * time.Second

// Times is how long each test took the last time it passed on this machine,
// in seconds, by its package's import path and its name.
type Times map[string]map[string]float64

// Step is one check a push runs before CI does.
type Step struct {
	// What names the check and Why says what put it in the pick.
	What string
	Why  string
	// Detail names what the check holds, one line each, when its name does
	// not: the tests a guard runs, and the browser specs, each with why.
	Detail []string
	// Command runs in Dir, a folder from the repository's root, or in the
	// root when Dir is empty.
	Dir     string
	Command []string
}

func (s Step) String() string {
	return s.What + " (" + s.Why + ")"
}

// Push is what a change picks to run before it is pushed: the checks CI
// would fail it on, as far as this machine can carry them.
type Push struct {
	// Root is the repository's top directory, Base where the branch left the
	// default branch and Commit the commit checked out.
	Root, Base, Commit string
	// Module is the module's path, and Policy says which policy the pick
	// follows and where it was read.
	Module string
	Policy string
	// Changed is how many files differ from Base, committed or not.
	Changed int
	// Steps are the checks, in the order they run, and Left what the pick
	// leaves to CI, each with why.
	Steps []Step
	Left  []Deferred
}

// ReadPush works out the pick for the branch checked out in dir, as Read
// works out a gate step's plan. timesOf gives this machine's record of how
// long each test of the module takes, which is known by the module's path.
func ReadPush(ctx context.Context, runner execx.Runner, dir string, timesOf func(module string) Times) (Push, error) {
	found, err := gather(ctx, runner, dir)
	if err != nil {
		return Push{}, err
	}
	return push(found, timesOf(found.module)), nil
}

// testArgs start every go test of a pick. One package is tested at a time,
// so -p bounds what builds at once. The run's own time limit ends a test
// that hangs, so go test's is off.
var testArgs = []string{"go", "test", "-json", "-count=1", "-p", "2", "-timeout", "0"}

// push picks what a change can break, in the order a failure is likeliest
// and cheapest to find. It vets every package the change reaches. It runs
// the policy's guards, the tests that read the whole tree, whatever changed.
// It tests the changed packages and the packages a contract names, then
// checks the board when a file under it changed, then tests the packages
// that import a changed one, nearest first. A package the policy lists as
// slow runs without the tests times records at SlowTest or longer, except
// the tests in a test file the change touched, and is left to CI when it did
// not change and times holds nothing of it. Whatever the pick leaves out it
// names in Left.
func push(found findings, times Times) Push {
	pick := Push{Root: found.root, Base: found.base, Commit: found.commit, Module: found.module, Changed: len(found.changed)}
	policy, says, policyErr := policyOf(found)
	pick.Policy = says
	reach := Classify(found.root, found.module, found.changed, found.packages, policy)
	slow := slowPaths(found.root, found.packages, policy)
	dirs := map[string]string{}
	for _, p := range found.packages {
		if dir, err := filepath.Rel(found.root, p.Dir); err == nil {
			dirs[p.ImportPath] = filepath.ToSlash(dir)
		}
	}
	if reach.Everything {
		for _, p := range found.packages {
			reach.Choices = append(reach.Choices, Choice{ImportPath: p.ImportPath, Why: "go.mod or go.sum changed"})
		}
	}

	// Nearest first, and at one distance the quick packages before the slow.
	far := distances(reach.Choices)
	slices.SortStableFunc(reach.Choices, func(a, b Choice) int {
		if far[a.ImportPath] != far[b.ImportPath] {
			return far[a.ImportPath] - far[b.ImportPath]
		}
		switch {
		case slow[a.ImportPath] == slow[b.ImportPath]:
			return 0
		case slow[a.ImportPath]:
			return 1
		}
		return -1
	})

	var own, importers []Step
	whole := map[string]bool{}
	for _, choice := range reach.Choices {
		dir := dirs[choice.ImportPath]
		why := choice.Reason()
		switch {
		case reach.Everything:
			why = choice.Why
		case choice.Imports == found.module:
			why = "imports " + named(".")
		case choice.Imports != "":
			why = "imports " + strings.TrimPrefix(choice.Imports, found.module+"/")
		}
		step := Step{What: "tests of " + named(dir), Why: why, Command: append(slices.Clone(testArgs), target(dir))}
		changed := choice.Imports == "" && choice.Reads == "" && !reach.Everything
		if slow[choice.ImportPath] {
			skipped, seconds := slowTests(found, dir, times[choice.ImportPath])
			switch {
			case len(skipped) > 0:
				step.What += fmt.Sprintf(" but for %d slower %s", len(skipped), plural(len(skipped), "one", "ones"))
				step.Command = append(slices.Clone(testArgs), "-skip", "^("+strings.Join(skipped, "|")+")$", target(dir))
				pick.Left = append(pick.Left, Deferred{
					Check: fmt.Sprintf("%d %s of %s", len(skipped), plural(len(skipped), "test", "tests"), named(dir)),
					Why:   slowWhy(len(skipped), seconds, changed),
				})
			case !changed && len(times[choice.ImportPath]) == 0:
				pick.Left = append(pick.Left, Deferred{Check: step.What, Why: why + ", and it is a slow package this machine has not timed"})
				continue
			}
		}
		if choice.Imports == "" {
			whole[choice.ImportPath] = !slow[choice.ImportPath]
			own = append(own, step)
		} else {
			importers = append(importers, step)
		}
	}

	switch {
	case reach.Everything:
		pick.Steps = append(pick.Steps, Step{What: "go vet of every package", Why: "go.mod or go.sum changed", Command: []string{"go", "vet", "-p", "2", "./..."}})
	case len(reach.Choices) > 0:
		vet := Step{What: fmt.Sprintf("go vet of %d %s", len(reach.Choices), plural(len(reach.Choices), "package", "packages")), Why: "they build against what changed", Command: []string{"go", "vet", "-p", "2"}}
		for _, choice := range reach.Choices {
			vet.Command = append(vet.Command, target(dirs[choice.ImportPath]))
		}
		pick.Steps = append(pick.Steps, vet)
	}
	for _, guard := range policy.Guards {
		// A quick package the change reaches itself runs whole below, its
		// guard among its tests.
		if importPath, isPackage := importPathOf(dirs, guard.Package); !isPackage || whole[importPath] {
			continue
		}
		step := Step{What: "guard tests of " + named(guard.Package), Why: guard.Why, Command: append(slices.Clone(testArgs), target(guard.Package))}
		if len(guard.Tests) > 0 {
			step.Detail = guard.Tests
			step.Command = append(slices.Clone(testArgs), "-run", "^("+strings.Join(guard.Tests, "|")+")$", target(guard.Package))
		}
		pick.Steps = append(pick.Steps, step)
	}
	pick.Steps = append(pick.Steps, own...)
	boardSteps, boardLeft := boardChecks(found.root, found.changed)
	pick.Steps = append(pick.Steps, boardSteps...)
	pick.Steps = append(pick.Steps, importers...)
	pick.Left = append(pick.Left, boardLeft...)
	if policyErr != nil {
		pick.Left = append(pick.Left, Deferred{Check: "tests of every other package", Why: strings.TrimPrefix(policyErr.Error(), "gatetest: ")})
	}
	if len(reach.Unknown) > 0 {
		subject := reach.Unknown[0] + " is"
		if len(reach.Unknown) > 1 {
			subject = fmt.Sprintf("%s and %d more files are", reach.Unknown[0], len(reach.Unknown)-1)
		}
		pick.Left = append(pick.Left, Deferred{Check: "tests of every other package", Why: subject + " in no package, under no contract and not listed as outside the Go checks"})
	}
	return pick
}

// distances are how many imports each chosen package is from the change:
// none for a package that changed or that a contract names, one for a
// package that imports one of those or a deleted package, and one more for
// each package between.
func distances(choices []Choice) map[string]int {
	chosen := map[string]Choice{}
	for _, choice := range choices {
		chosen[choice.ImportPath] = choice
	}
	far := map[string]int{}
	var from func(importPath string) int
	from = func(importPath string) int {
		choice, isChosen := chosen[importPath]
		if !isChosen || choice.Imports == "" {
			return 0
		}
		if _, isKnown := far[importPath]; !isKnown {
			far[importPath] = 1 + from(choice.Imports)
		}
		return far[importPath]
	}
	for _, choice := range choices {
		far[choice.ImportPath] = from(choice.ImportPath)
	}
	return far
}

// named is a package as a pick names it: its directory from the repository's
// root, as the policy names it.
func named(dir string) string {
	if dir == "." {
		return "the root package"
	}
	return dir
}

// target is a package as go takes it from the repository's root.
func target(dir string) string {
	if dir == "." {
		return "."
	}
	return "./" + dir
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

// importPathOf is the import path of the package in dir, a directory as the
// policy names one.
func importPathOf(dirs map[string]string, dir string) (string, bool) {
	for importPath, found := range dirs {
		if strings.EqualFold(found, dir) {
			return importPath, true
		}
	}
	return "", false
}

// slowWhy says why a pick leaves count tests of a slow package to CI.
func slowWhy(count int, seconds float64, changed bool) string {
	took := time.Duration(seconds * float64(time.Second)).Round(time.Second).String()
	why := "it takes " + took + " here"
	if count > 1 {
		why = fmt.Sprintf("they take %s or longer each here, %s in all", SlowTest, took)
	}
	switch {
	case !changed:
		return why
	case count > 1:
		return why + ", and are in files the change did not touch"
	}
	return why + " and is in a file the change did not touch"
}

// testFunc matches a test's declaration at the start of a line.
var testFunc = regexp.MustCompile(`(?m)^func (Test\w*)\(\w+ \*testing\.T\)`)

// slowTests are the tests of the slow package in dir that a pick skips,
// sorted, with how long they take together by times: each test the package
// declares that times records at SlowTest or longer, but for the tests in a
// test file the change touched, which run however long they take.
func slowTests(found findings, dir string, times map[string]float64) (skipped []string, seconds float64) {
	if len(times) == 0 {
		return nil, 0
	}
	entries, err := os.ReadDir(filepath.Join(found.root, filepath.FromSlash(dir)))
	if err != nil {
		return nil, 0
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") || slices.ContainsFunc(found.changed, func(file string) bool { return filepath.ToSlash(file) == path.Join(dir, name) }) {
			continue
		}
		source, err := fsx.ReadFile(filepath.Join(found.root, filepath.FromSlash(dir), name))
		if err != nil {
			continue
		}
		for _, match := range testFunc.FindAllSubmatch(source, -1) {
			if took := times[string(match[1])]; took >= SlowTest.Seconds() {
				skipped = append(skipped, string(match[1]))
				seconds += took
			}
		}
	}
	slices.Sort(skipped)
	return skipped, seconds
}

// Again is the command that runs tests, some of the tests of the package a
// check tests, once more and by themselves.
func (s Step) Again(tests []string) []string {
	return append(slices.Clone(testArgs), "-run", "^("+strings.Join(tests, "|")+")$", s.Command[len(s.Command)-1])
}
