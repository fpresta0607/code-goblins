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
	"strconv"
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
	tallies := map[string]*skipTally{}
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		sha, subject, ok := strings.Cut(line, "\x1f")
		if !ok || !strings.HasPrefix(subject, GateCommitPrefix) {
			continue
		}
		diff, err := run(ctx, git, dir, "-c", "core.quotePath=false", "show", "--format=", "--no-color", "--unified=0", "--find-renames", sha)
		if err != nil {
			return Result{}, err
		}
		result.Commits++
		commit := gateCommit{sha: sha[:min(len(sha), 8)], subject: subject}
		if err := tallySkips(ctx, git, dir, sha, diff, commit, tallies); err != nil {
			return Result{}, err
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
	skips, err := skipIncreases(ctx, git, dir, tallies)
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

// skipTally is the lines that skip or narrow tests the gate commits added
// to one file minus those they removed, the texts of the ones they added and
// did not take out again, and the last gate commit that touched the file.
type skipTally struct {
	count  int
	added  map[string]int
	commit gateCommit
}

// tallySkips adds one gate commit's diff to the tallies, carrying a file's
// tally to its new path when the commit renames it. A removed or added line
// counts only when it skips or narrows tests in code, judged on the file as
// it stood before and after the commit (markerLines), so skip text inside a
// string or comment literal is never a skip.
func tallySkips(ctx context.Context, git execx.Runner, dir, sha, diff string, commit gateCommit, tallies map[string]*skipTally) error {
	var tally *skipTally
	var before, after map[int]bool
	oldLine, newLine := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			source, file := diffPaths(line)
			tally = tallies[source]
			if tally == nil {
				tally = &skipTally{added: map[string]int{}}
			}
			delete(tallies, source)
			tally.commit = commit
			tallies[file] = tally
			var err error
			before, after = nil, nil
			if isTestFile(source) {
				if before, err = markedAt(ctx, git, dir, sha+"^", source); err != nil {
					return err
				}
			}
			if isTestFile(file) {
				if after, err = markedAt(ctx, git, dir, sha, file); err != nil {
					return err
				}
			}
		case strings.HasPrefix(line, "@@ "):
			oldLine, newLine = hunkStarts(line)
		case strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ "):
		case strings.HasPrefix(line, "+"):
			if after[newLine] {
				tally.count++
				tally.added[strings.TrimSpace(line[1:])]++
			}
			newLine++
		case strings.HasPrefix(line, "-"):
			if before[oldLine] {
				tally.count--
				if text := strings.TrimSpace(line[1:]); tally.added[text] > 0 {
					tally.added[text]--
				}
			}
			oldLine++
		}
	}
	return nil
}

// markedAt is the set of line numbers in file at rev that skip or narrow
// tests in code; a file rev does not have marks none.
func markedAt(ctx context.Context, git execx.Runner, dir, rev, file string) (map[int]bool, error) {
	result, err := git.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: []string{"-c", "core.quotePath=false", "show", rev + ":" + file}})
	if err != nil {
		return nil, fmt.Errorf("testguard: git show %s:%s: %w", rev, file, err)
	}
	marked := map[int]bool{}
	if result.ExitCode != 0 {
		return marked, nil
	}
	for _, line := range markerLines(file, string(result.Stdout)) {
		marked[line.number] = true
	}
	return marked, nil
}

// hunkStarts reads the first old and new line numbers of a unified diff hunk
// header, "@@ -a[,b] +c[,d] @@".
func hunkStarts(header string) (int, int) {
	var oldStart, newStart int
	fields := strings.Fields(header)
	if len(fields) >= 3 {
		oldStart, _ = strconv.Atoi(strings.SplitN(strings.TrimPrefix(fields[1], "-"), ",", 2)[0])
		newStart, _ = strconv.Atoi(strings.SplitN(strings.TrimPrefix(fields[2], "+"), ",", 2)[0])
	}
	return oldStart, newStart
}

// markedLine is one line that skips or narrows tests in code: its number,
// its text trimmed, and what it does.
type markedLine struct {
	number int
	text   string
	what   string
}

// markerLines lists a file's lines that skip or narrow tests, judged on the
// code alone (codeLines).
func markerLines(file, content string) []markedLine {
	original := strings.Split(content, "\n")
	var marked []markedLine
	for index, code := range codeLines(file, content) {
		if what := marker(code); what != "" && index < len(original) {
			marked = append(marked, markedLine{number: index + 1, text: strings.TrimSpace(original[index]), what: what})
		}
	}
	return marked
}

// skipIncreases reports every test file whose gate commits added more lines
// that skip or narrow tests than they removed while HEAD still has some of
// the skip lines they added, listing those. It counts each gate commit's own
// diff rather than matching the lines a gate commit added because the
// adversary is the gate's own fixer: a later gate commit can reword a skip an
// earlier one added, or move its file, and neither leaves the added line at
// HEAD. Merges and the goblin's commits are never read, so a skip main or the
// goblin added or removed is its author's call. Two limits are accepted: a
// gate commit that removes an existing skip and adds its own in the same file
// nets zero and is not reported, and a gate-touched file that a goblin commit
// or a merge moves is not followed, since only gate commits are read and the
// fixer cannot cause it.
func skipIncreases(ctx context.Context, git execx.Runner, dir string, tallies map[string]*skipTally) ([]Removal, error) {
	files := make([]string, 0, len(tallies))
	for file, tally := range tallies {
		if tally.count > 0 && isTestFile(file) {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	var removals []Removal
	for _, file := range files {
		exists, err := git.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: []string{"cat-file", "-e", "HEAD:" + file}})
		if err != nil {
			return nil, fmt.Errorf("testguard: git cat-file: %w", err)
		}
		if exists.ExitCode != 0 {
			continue
		}
		content, err := run(ctx, git, dir, "show", "HEAD:"+file)
		if err != nil {
			return nil, err
		}
		tally := tallies[file]
		var standing []string
		for _, line := range markerLines(file, content) {
			if tally.added[line.text] > 0 {
				tally.added[line.text]--
				standing = append(standing, line.what+line.text)
			}
		}
		if len(standing) == 0 {
			continue
		}
		lines := "lines"
		if tally.count == 1 {
			lines = "line"
		}
		removals = append(removals, Removal{Commit: tally.commit.sha, Subject: tally.commit.subject, File: file, What: fmt.Sprintf("gate commits added %d more skip %s than they removed, still at HEAD: %s", tally.count, lines, strings.Join(standing, "; "))})
	}
	return removals, nil
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

// grep lists the lines at HEAD that hold a removed test's name, with their
// files. git grep exits 1 when
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
// under a new name in the same file. Skips are counted across the gate
// commits instead (skipIncreases), since a later commit can reword one.
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
