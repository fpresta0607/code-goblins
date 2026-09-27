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
	first := ""
	touched := map[string]gateCommit{}
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
		if first == "" {
			first = sha
		}
		commit := gateCommit{sha: sha[:min(len(sha), 8)], subject: subject}
		for _, header := range strings.Split(diff, "\n") {
			if strings.HasPrefix(header, "diff --git ") {
				source, target := diffPaths(strings.TrimSuffix(header, "\r"))
				touched[source], touched[target] = commit, commit
			}
		}
		for _, found := range Scan(diff) {
			standing, err := stillStanding(ctx, git, dir, found)
			if err != nil {
				return Result{}, err
			}
			if standing {
				result.Removals = append(result.Removals, Removal{Commit: commit.sha, Subject: subject, File: found.File, What: found.What})
			}
		}
	}
	if first == "" {
		return result, nil
	}
	skips, err := skipIncreases(ctx, git, dir, first, touched)
	if err != nil {
		return Result{}, err
	}
	result.Removals = append(result.Removals, skips...)
	return result, nil
}

// gateCommit names the gate commit that last touched a file.
type gateCommit struct {
	sha     string
	subject string
}

// skipIncreases counts, in each test file a gate commit touched, the lines
// that skip or narrow tests at HEAD against the same file as it stood before
// the branch's first gate commit, following a rename between the two, and
// reports every file that gained some with the lines that were not there
// before. It counts rather than matching the lines a gate commit added
// because the adversary is the gate's own fixer: a later gate commit can
// reword a skip an earlier one added, or move its file, and neither leaves the
// added line at HEAD. A skip the branch had before its first gate commit, or
// its goblin added in a file no gate commit touched, is its author's call.
func skipIncreases(ctx context.Context, git execx.Runner, dir, first string, touched map[string]gateCommit) ([]Removal, error) {
	before := first + "^"
	status, err := run(ctx, git, dir, "diff", "--name-status", "-z", "--find-renames", before, "HEAD")
	if err != nil {
		return nil, err
	}
	var removals []Removal
	fields := strings.Split(status, "\x00")
	for index := 0; index < len(fields); {
		code := fields[index]
		if code == "" {
			break
		}
		origin, file := "", ""
		switch code[0] {
		case 'R':
			if index+2 >= len(fields) {
				return nil, fmt.Errorf("testguard: git diff --name-status: a rename without both paths")
			}
			origin, file = fields[index+1], fields[index+2]
			index += 3
		default:
			if index+1 >= len(fields) {
				return nil, fmt.Errorf("testguard: git diff --name-status: a change without its path")
			}
			file = fields[index+1]
			if code[0] != 'A' {
				origin = file
			}
			index += 2
		}
		commit, gated := touched[file]
		if !gated && origin != "" {
			commit, gated = touched[origin]
		}
		if code[0] == 'D' || !gated || !isTestFile(file) {
			continue
		}
		var earlier []string
		if origin != "" {
			content, err := run(ctx, git, dir, "show", before+":"+origin)
			if err != nil {
				return nil, err
			}
			earlier = markerLines(content)
		}
		content, err := run(ctx, git, dir, "show", "HEAD:"+file)
		if err != nil {
			return nil, err
		}
		now := markerLines(content)
		if len(now) <= len(earlier) {
			continue
		}
		left := map[string]int{}
		for _, line := range earlier {
			left[line]++
		}
		var added []string
		for _, line := range now {
			if left[line] > 0 {
				left[line]--
				continue
			}
			added = append(added, marker(line)+line)
		}
		lines := "lines"
		if len(now)-len(earlier) == 1 {
			lines = "line"
		}
		removals = append(removals, Removal{Commit: commit.sha, Subject: commit.subject, File: file, What: fmt.Sprintf("%d more skip %s than before the branch's first gate commit: %s", len(now)-len(earlier), lines, strings.Join(added, "; "))})
	}
	return removals, nil
}

// markerLines are a file's lines that skip or narrow tests, trimmed.
func markerLines(content string) []string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		if line = strings.TrimSpace(line); marker(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// stillStanding reports whether HEAD still lacks what a gate commit removed:
// the deleted test file, or a test declared under the removed test's exact
// name anywhere in a test file. A later commit that puts the test back, which
// is what a fix turn on this gate does, resolves it.
func stillStanding(ctx context.Context, git execx.Runner, dir string, found Finding) (bool, error) {
	switch {
	case found.Test != "":
		matches, err := grep(ctx, git, dir, "-e", found.Test, "HEAD")
		if err != nil {
			return false, err
		}
		for _, match := range matches {
			if name, ok := testName(match.line); ok && name == found.Test && isTestFile(match.file) {
				return false, nil
			}
		}
		return true, nil
	default:
		result, err := git.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: []string{"cat-file", "-e", "HEAD:" + found.File}})
		if err != nil {
			return false, fmt.Errorf("testguard: git cat-file: %w", err)
		}
		return result.ExitCode != 0, nil
	}
}

type grepMatch struct {
	file string
	line string
}

// grep lists the lines at HEAD that hold a fixed string, a removed test's
// name or an added skip line, with their files. git grep exits 1 when
// nothing matches, which is an answer rather than a failure.
func grep(ctx context.Context, git execx.Runner, dir string, args ...string) ([]grepMatch, error) {
	result, err := git.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: append([]string{"grep", "-z", "-F"}, args...)})
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
	var matches []grepMatch
	for _, output := range strings.Split(strings.TrimSpace(string(result.Stdout)), "\n") {
		path, line, ok := strings.Cut(output, "\x00")
		if file, isHead := strings.CutPrefix(path, "HEAD:"); ok && isHead {
			matches = append(matches, grepMatch{file: file, line: strings.TrimSuffix(line, "\r")})
		}
	}
	return matches, nil
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

// Finding is one deleted test or test file in a diff. Test is the removed
// test's name; a deleted test file carries none.
type Finding struct {
	File string
	What string
	Test string
}

// Scan reads one commit's unified diff and reports the test files it
// deleted, and the tests it removed without adding back under the same name
// anywhere in the commit (so a test moved or reordered is not a removal) or
// under a new name in the same file. Skips are counted across the branch
// instead (skipIncreases), since a later commit can reword one.
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
			var source string
			source, file = diffPaths(line)
			deleted = false
			if isTestFile(source) && !isTestFile(file) {
				findings = append(findings, Finding{File: source, What: "deleted the test file, renaming it to " + file})
			}
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
// the filler words every description shares. An acronym stays one word, so
// TestPRMerge is pr and merge: two names that share only an acronym's letters
// are not one test reworded.
func nameWords(name string) map[string]bool {
	isUpper := func(r rune) bool { return r >= 'A' && r <= 'Z' }
	isLower := func(r rune) bool { return r >= 'a' && r <= 'z' }
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && isUpper(r) {
			previous := runes[i-1]
			nextIsLower := i+1 < len(runes) && isLower(runes[i+1])
			if isLower(previous) || previous >= '0' && previous <= '9' || isUpper(previous) && nextIsLower {
				b.WriteByte(' ')
			}
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

// diffPaths reads the two paths a "diff --git a/<old> b/<new>" header names.
func diffPaths(header string) (string, string) {
	source, target, ok := strings.Cut(strings.TrimPrefix(header, "diff --git a/"), " b/")
	if !ok {
		return "", ""
	}
	return source, target
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
	regexp.MustCompile("^\\s*(?:it|test|describe)(?:\\.\\w+)*\\s*\\(\\s*(?:\"([^\"]+)\"|'([^']+)'|`([^`]+)`)"),
	regexp.MustCompile(`^\s*(?:It|Describe|Context)\s+(?:"([^"]+)"|'([^']+)')`),
}

// testName reads the name a declaration gives its test. A quoted name runs
// to the closing quote of its own kind, so an apostrophe inside double quotes
// is part of it.
func testName(line string) (string, bool) {
	for _, declaration := range testDeclarations {
		if match := declaration.FindStringSubmatch(line); match != nil {
			for _, name := range match[1:] {
				if name != "" {
					return name, true
				}
			}
		}
	}
	return "", false
}

// marker says what a line that skips or narrows tests does, and is empty for
// any other line. An only does not skip the test it marks: it focuses the
// file on that test, which skips every other test in it.
func marker(line string) string {
	switch {
	case onlyMarker.MatchString(line):
		return "focused the file on one test, which skips every other test in it: "
	case skipMarker.MatchString(line):
		return "added a skip: "
	}
	return ""
}

// skipMarker matches a line that skips tests: Go's Skip calls, pytest's and
// unittest's skips and expected failures, JavaScript's skip, todo and
// x-prefixed variants, and Pester's -Skip.
var skipMarker = regexp.MustCompile(`\b[tb]\.Skip(?:f|Now)?\(|@pytest\.mark\.(?:skip|skipif|xfail)\b|\bpytest\.skip\(|@unittest\.skip|\.skipTest\(|\b(?:it|test|describe)\.(?:skip|todo)\(|\bx(?:it|test|describe)\(|\s-Skip\b`)

// onlyMarker matches JavaScript's only variants.
var onlyMarker = regexp.MustCompile(`\b(?:it|test|describe)\.only\(`)
