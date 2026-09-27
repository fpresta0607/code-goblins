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
// default branch and HEAD in dir, leaving out those already on origin/<branch>
// for the run's own branch, or rebased copies of them, and lists every test
// they deleted or skipped.
func Check(ctx context.Context, git execx.Runner, dir string) (Result, error) {
	base, err := mergeBase(ctx, git, dir)
	if err != nil {
		return Result{}, err
	}
	pushed, err := pushedPatches(ctx, git, dir, base)
	if err != nil {
		return Result{}, err
	}
	log, err := run(ctx, git, dir, "log", "--reverse", "--format=%H%x1f%s%x1f"+authorFormat, base+"..HEAD")
	if err != nil {
		return Result{}, err
	}
	result := Result{Base: base}
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		sha, rest, _ := strings.Cut(line, "\x1f")
		subject, author, ok := strings.Cut(rest, "\x1f")
		if !ok || !strings.HasPrefix(subject, GateCommitPrefix) {
			continue
		}
		diff, err := run(ctx, git, dir, append([]string{"show", "--format=", sha}, diffFlags...)...)
		if err != nil {
			return Result{}, err
		}
		if pushed[patchKey(author, diff)] {
			continue
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

var diffFlags = []string{"--no-color", "--unified=0", "--find-renames"}

// authorFormat is the author and author date, which a rebase keeps.
const authorFormat = "%an <%ae> %ai"

// pushedPatches collects the patches of the gate commits already on
// origin/<branch>, the branch currentBranch names. Everything pushed there
// passed this check, kept or approved, so such a commit is not read again,
// nor is a rebased copy of one with the same author, author date and change.
// Another branch's commits are never counted: the same deletion approved
// there is not approved here. With no branch named, the map is empty.
func pushedPatches(ctx context.Context, git execx.Runner, dir, base string) (map[string]bool, error) {
	patches := map[string]bool{}
	branch, err := currentBranch(ctx, git, dir, base)
	if err != nil || branch == "" {
		return patches, err
	}
	remote := "refs/remotes/origin/" + branch
	exists, err := run(ctx, git, dir, "for-each-ref", "--format=%(refname)", remote)
	if err != nil || strings.TrimSpace(exists) != remote {
		return patches, err
	}
	commits, err := logDiffs(ctx, git, dir, "%s%x1f"+authorFormat, remote, "^"+base, "--fixed-strings", "--grep="+GateCommitPrefix)
	if err != nil {
		return nil, err
	}
	for _, commit := range commits {
		subject, author, _ := strings.Cut(commit.header, "\x1f")
		if strings.HasPrefix(subject, GateCommitPrefix) {
			patches[patchKey(author, commit.diff)] = true
		}
	}
	return patches, nil
}

// currentBranch names the branch HEAD is on. A no-mistakes run worktree has
// a detached HEAD that its rebase step may have rewritten, so there it is the
// one local branch whose every commit past base has a patch-equivalent copy
// among HEAD's commits past base, as git cherry would judge it, but without
// comparing against the default branch's history, where a stale branch's
// merged changes would also qualify it. It is empty when no single branch
// qualifies, and then every gate commit is read.
func currentBranch(ctx context.Context, git execx.Runner, dir, base string) (string, error) {
	if name, err := run(ctx, git, dir, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		return strings.TrimSpace(name), nil
	}
	inHead, err := logDiffs(ctx, git, dir, "", "HEAD", "^"+base)
	if err != nil {
		return "", err
	}
	copies := map[string]bool{}
	for _, commit := range inHead {
		copies[patchKey("", commit.diff)] = true
	}
	onBranches, err := logDiffs(ctx, git, dir, "%S", "--branches", "^"+base, "--source")
	if err != nil {
		return "", err
	}
	copied := map[string]bool{}
	for _, commit := range onBranches {
		branch := commit.header
		if _, seen := copied[branch]; !seen {
			copied[branch] = true
		}
		copied[branch] = copied[branch] && copies[patchKey("", commit.diff)]
	}
	var names []string
	for branch, isCopy := range copied {
		if isCopy {
			names = append(names, branch)
		}
	}
	if len(names) == 1 {
		return names[0], nil
	}
	return "", nil
}

type loggedDiff struct {
	header string
	diff   string
}

// logDiffs lists the non-merge commits git log selects with revs, each with
// its header in format and its diff.
func logDiffs(ctx context.Context, git execx.Runner, dir, format string, revs ...string) ([]loggedDiff, error) {
	args := append([]string{"log", "--no-merges", "--format=%x00" + format, "-p"}, diffFlags...)
	log, err := run(ctx, git, dir, append(args, revs...)...)
	if err != nil {
		return nil, err
	}
	var commits []loggedDiff
	for _, commit := range strings.Split(log, "\x00")[1:] {
		header, diff, _ := strings.Cut(commit, "\n")
		commits = append(commits, loggedDiff{header: header, diff: diff})
	}
	return commits, nil
}

// patchKey is a commit's author and diff without what a rebase changes, its
// blob ids and hunk line numbers, so a rebased copy has the key of its
// original while another commit making the same edit does not.
func patchKey(author, diff string) string {
	var b strings.Builder
	b.WriteString(author)
	b.WriteByte('\n')
	for _, line := range strings.Split(diff, "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case line == "" || strings.HasPrefix(line, "index "):
			continue
		case strings.HasPrefix(line, "@@"):
			line = "@@"
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// stillStanding reports whether HEAD still lacks what a gate commit removed:
// the deleted test file, a test declared under the removed test's exact name
// anywhere in a test file, or still carries the skip it added. A later commit
// that puts the test back, which is what a fix turn on this gate does,
// resolves it.
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
	case found.Skip != "":
		matches, err := grep(ctx, git, dir, "-e", found.Skip, "HEAD", "--", found.File)
		return len(matches) > 0, err
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

// Finding is one deleted or skipped test in a diff. Test is the removed
// test's name and Skip the skip or only line added, whichever applies; a
// deleted test file carries neither.
type Finding struct {
	File string
	What string
	Test string
	Skip string
}

// Scan reads one commit's unified diff and reports the test files it
// deleted, the tests it removed without adding back under the same name
// anywhere in the commit (so a test moved or reordered is not a removal) or
// under a new name in the same file, and the skips it added beyond those it
// removed from the same file, so an edited skip is not a new one.
func Scan(diff string) []Finding {
	var findings []Finding
	removed := map[string]string{}
	added := map[string]bool{}
	addedIn := map[string][]string{}
	type markerKey struct{ file, what string }
	var markerOrder []markerKey
	addedMarkers := map[markerKey][]Finding{}
	removedMarkers := map[markerKey]int{}
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
			if what := marker(line[1:]); what != "" {
				removedMarkers[markerKey{file, what}]++
			}
		case strings.HasPrefix(line, "+"):
			if name, ok := testName(line[1:]); ok {
				added[name] = true
				addedIn[file] = append(addedIn[file], name)
			}
			if what := marker(line[1:]); what != "" {
				key := markerKey{file, what}
				if len(addedMarkers[key]) == 0 {
					markerOrder = append(markerOrder, key)
				}
				skip := strings.TrimSpace(line[1:])
				addedMarkers[key] = append(addedMarkers[key], Finding{File: file, What: what + skip, Skip: skip})
			}
		}
	}
	for _, key := range markerOrder {
		markers := addedMarkers[key]
		findings = append(findings, markers[min(removedMarkers[key], len(markers)):]...)
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
