// Package testguard finds the tests a no-mistakes gate's own fix commits
// deleted or skipped. On 2026-09-24 a gate's test step fixed a failing live
// test on PrecisionDocs #1272 by deleting it, as an automatic fix no review
// saw. A gate may still do that, but only as an ask-user decision, and a
// repository gate that runs this check is what turns it into one.
package testguard

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// GateCommitPrefix begins the subject of every commit a no-mistakes step
// makes, such as "no-mistakes(test): ...". A goblin's own commits carry no
// such prefix, and deleting a test there is the author's call, not a gate's.
const GateCommitPrefix = "no-mistakes("

// Removal is one test a gate commit deleted or skipped.
type Removal struct {
	Commit  string
	Subject string
	File    string
	What    string
}

func (r Removal) String() string {
	return fmt.Sprintf("%s %s: %s: %s", r.Commit, r.Subject, r.File, r.What)
}

// Result is what one check examined and what it found. Commits counts the
// gate commits read, so a check that read nothing says so rather than
// passing silently.
type Result struct {
	Base     string
	Commits  int
	Removals []Removal
}

// Check reads the gate commits between the branch's merge base with the
// default branch and HEAD in dir, and lists every test they deleted or
// skipped.
func Check(ctx context.Context, git execx.Runner, dir string) (Result, error) {
	base, err := mergeBase(ctx, git, dir)
	if err != nil {
		return Result{}, err
	}
	log, err := run(ctx, git, dir, "log", "--reverse", "--format=%H%x1f%s", base+"..HEAD")
	if err != nil {
		return Result{}, err
	}
	result := Result{Base: base}
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		sha, subject, ok := strings.Cut(line, "\x1f")
		if !ok || !strings.HasPrefix(subject, GateCommitPrefix) {
			continue
		}
		diff, err := run(ctx, git, dir, "show", "--format=", "--no-color", "--unified=0", "--find-renames", sha)
		if err != nil {
			return Result{}, err
		}
		result.Commits++
		for _, found := range Scan(diff) {
			standing, err := stillStanding(ctx, git, dir, found)
			if err != nil {
				return Result{}, err
			}
			if standing {
				result.Removals = append(result.Removals, Removal{Commit: sha[:min(len(sha), 8)], Subject: subject, File: found.File, What: found.What})
			}
		}
	}
	return result, nil
}

// stillStanding reports whether HEAD still lacks what a gate commit removed:
// the deleted test file, the removed test anywhere in a test file, or still
// carries the skip it added. A later commit that puts the test back, which
// is what a fix turn on this gate does, resolves it.
func stillStanding(ctx context.Context, git execx.Runner, dir string, found Finding) (bool, error) {
	switch {
	case found.Test != "":
		files, err := grep(ctx, git, dir, found.Test)
		if err != nil {
			return false, err
		}
		for _, file := range files {
			if isTestFile(file) {
				return false, nil
			}
		}
		return true, nil
	case found.Skip != "":
		files, err := grep(ctx, git, dir, found.Skip, "--", found.File)
		return len(files) > 0, err
	default:
		result, err := git.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: []string{"cat-file", "-e", "HEAD:" + found.File}})
		if err != nil {
			return false, fmt.Errorf("testguard: git cat-file: %w", err)
		}
		return result.ExitCode != 0, nil
	}
}

// grep lists the files at HEAD that hold text as a fixed string. git grep
// exits 1 when nothing matches, which is an answer rather than a failure.
func grep(ctx context.Context, git execx.Runner, dir, text string, pathspec ...string) ([]string, error) {
	args := append([]string{"grep", "-l", "-F", "-e", text, "HEAD"}, pathspec...)
	result, err := git.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: args})
	if err != nil {
		return nil, fmt.Errorf("testguard: git grep: %w", err)
	}
	switch result.ExitCode {
	case 0:
	case 1:
		return nil, nil
	default:
		return nil, fmt.Errorf("testguard: git grep exited %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(result.Stdout)), "\n") {
		if file, ok := strings.CutPrefix(line, "HEAD:"); ok {
			files = append(files, file)
		}
	}
	return files, nil
}

// mergeBase finds where the branch left the default branch: origin/HEAD
// where the clone records one, else origin/main.
func mergeBase(ctx context.Context, git execx.Runner, dir string) (string, error) {
	for _, ref := range []string{"origin/HEAD", "origin/main"} {
		if base, err := run(ctx, git, dir, "merge-base", "HEAD", ref); err == nil && strings.TrimSpace(base) != "" {
			return strings.TrimSpace(base), nil
		}
	}
	return "", errors.New("testguard: no merge base with origin/HEAD or origin/main, so the gate's commits cannot be told from the default branch")
}

func run(ctx context.Context, git execx.Runner, dir string, args ...string) (string, error) {
	result, err := git.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: args})
	if err != nil {
		return "", fmt.Errorf("testguard: git %s: %w", strings.Join(args, " "), err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("testguard: git %s exited %d: %s", strings.Join(args, " "), result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return string(result.Stdout), nil
}

// Finding is one deleted or skipped test in a diff. Test is the removed
// test's name and Skip the skip line added, whichever applies; a deleted test
// file carries neither.
type Finding struct {
	File string
	What string
	Test string
	Skip string
}

// Scan reads one commit's unified diff and reports the test files it
// deleted, the tests it removed without adding back under the same name
// anywhere in the commit (so a test moved or reordered is not a removal) or
// under a new name in the same file, and the skips it added.
func Scan(diff string) []Finding {
	var findings []Finding
	removed := map[string]string{}
	added := map[string]bool{}
	addedIn := map[string][]string{}
	file, deleted := "", false
	for _, line := range strings.Split(diff, "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			file, deleted = diffTarget(line), false
			continue
		case strings.HasPrefix(line, "deleted file mode"):
			deleted = true
			if isTestFile(file) {
				findings = append(findings, Finding{File: file, What: "deleted the test file"})
			}
			continue
		case strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ "):
			continue
		}
		if !isTestFile(file) {
			continue
		}
		switch {
		case strings.HasPrefix(line, "-"):
			if name, ok := testName(line[1:]); ok && !deleted {
				removed[name] = file
			}
		case strings.HasPrefix(line, "+"):
			if name, ok := testName(line[1:]); ok {
				added[name] = true
				addedIn[file] = append(addedIn[file], name)
			}
			if skipMarker.MatchString(line[1:]) {
				skip := strings.TrimSpace(line[1:])
				findings = append(findings, Finding{File: file, What: "added a skip: " + skip, Skip: skip})
			}
		}
	}
	// A test that is gone under its name was renamed rather than deleted when
	// its file gained a new test whose name shares at least half the words of
	// the old one: rewording a test's description is a review fix. A new test
	// on another subject does not stand in for a deleted one.
	lost := map[string][]string{}
	for name, file := range removed {
		if !added[name] {
			lost[file] = append(lost[file], name)
		}
	}
	files := make([]string, 0, len(lost))
	for file := range lost {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		sort.Strings(lost[file])
		taken := map[string]bool{}
		for _, name := range lost[file] {
			if renamed(name, addedIn[file], removed, taken) {
				continue
			}
			findings = append(findings, Finding{File: file, What: "removed the test " + name, Test: name})
		}
	}
	return findings
}

// renamed pairs a removed test with a new test in the same file, one the
// commit did not also remove and no earlier pairing took, whose name shares
// at least half the words of the shorter of the two names.
func renamed(name string, candidates []string, removed map[string]string, taken map[string]bool) bool {
	words := nameWords(name)
	for _, candidate := range candidates {
		if _, alsoRemoved := removed[candidate]; alsoRemoved || taken[candidate] {
			continue
		}
		other := nameWords(candidate)
		shared := 0
		for word := range words {
			if other[word] {
				shared++
			}
		}
		if smaller := min(len(words), len(other)); smaller > 0 && shared*2 >= smaller {
			taken[candidate] = true
			return true
		}
	}
	return false
}

// nameWords splits a test name into its lowercased words, at underscores,
// spaces and punctuation and at camelCase boundaries, leaving out "test" and
// the filler words every description shares.
func nameWords(name string) map[string]bool {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	words := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(b.String()), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		switch word {
		case "test", "the", "a", "an", "and", "is", "to", "of", "in", "it", "on", "when":
			continue
		}
		words[word] = true
	}
	return words
}

// diffTarget reads the path a "diff --git a/<old> b/<new>" header names.
func diffTarget(header string) string {
	_, target, ok := strings.Cut(header, " b/")
	if !ok {
		return ""
	}
	return target
}

// isTestFile recognizes the test files of the languages the fleet writes:
// Go, Python, JavaScript and TypeScript, and PowerShell's Pester.
func isTestFile(file string) bool {
	if file == "" {
		return false
	}
	base := strings.ToLower(path.Base(file))
	lower := "/" + strings.ToLower(file)
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.HasSuffix(base, ".py") && (strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") || strings.Contains(lower, "/tests/")):
		return true
	case strings.HasSuffix(base, ".tests.ps1"):
		return true
	}
	for _, marker := range []string{".test.", ".spec."} {
		if strings.Contains(base, marker) {
			return true
		}
	}
	return strings.Contains(lower, "/__tests__/")
}

// testDeclarations name a test where it is declared: a Go test, benchmark,
// fuzz or example function, a Python test function or class, a JavaScript
// it, test or describe, and a Pester It, Describe or Context.
var testDeclarations = []*regexp.Regexp{
	regexp.MustCompile(`^\s*func\s+((?:Test|Benchmark|Fuzz|Example)\w*)\s*\(`),
	regexp.MustCompile(`^\s*(?:async\s+)?def\s+(test\w*)\s*\(`),
	regexp.MustCompile(`^\s*class\s+(Test\w*)\b`),
	regexp.MustCompile("^\\s*(?:it|test|describe)(?:\\.\\w+)*\\s*\\(\\s*['\"`](.+?)['\"`]"),
	regexp.MustCompile(`^\s*(?:It|Describe|Context)\s+['"](.+?)['"]`),
}

func testName(line string) (string, bool) {
	for _, declaration := range testDeclarations {
		if match := declaration.FindStringSubmatch(line); match != nil {
			return match[1], true
		}
	}
	return "", false
}

// skipMarker matches a line that skips or narrows tests: Go's Skip calls,
// pytest's and unittest's skips and expected failures, JavaScript's skip,
// todo, x-prefixed and only variants, and Pester's -Skip.
var skipMarker = regexp.MustCompile(`\b[tb]\.Skip(?:f|Now)?\(|@pytest\.mark\.(?:skip|skipif|xfail)\b|\bpytest\.skip\(|@unittest\.skip|\.skipTest\(|\b(?:it|test|describe)\.(?:skip|todo|only)\(|\bx(?:it|test|describe)\(|\s-Skip\b`)
