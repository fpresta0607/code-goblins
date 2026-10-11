package gatetest

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// boardDir is the folder of the board, the repository's frontend, from its
// root. CI checks it in a job of its own.
const boardDir = "frontend"

var (
	// opensFixture matches a fixture page a browser spec opens, by its name.
	opensFixture = regexp.MustCompile(`fixtures/([\w-]+)\.html`)
	// importsHelper matches a file beside the specs that a spec imports.
	importsHelper = regexp.MustCompile(`from "\./([\w-]+)"`)
)

// boardChecks are the board's checks a change picks, and what of the board
// it leaves to CI: nothing of either unless a file under the board changed.
// A change picks the type check, the lint and the unit tests, which are
// quick, and the browser specs that cover it, in this order: a spec that
// changed, a spec that opens a fixture page that changed, a spec that
// imports a changed file beside the specs, and a spec that shares a word of
// its name with a changed source, as run-terminal.spec.ts does with
// RunCard.tsx. Most specs open the whole board, so what a spec imports does
// not say what it covers, and its name is what it has. The other specs are
// left to CI, which runs them all in four jobs at once where this machine
// has one browser and 4 GB to give them. A board that is not installed runs
// nothing: npm can exit 0 where it found no program to run.
func boardChecks(root string, changed []string) (steps []Step, left []Deferred) {
	var files []string
	for _, file := range changed {
		if rest, under := strings.CutPrefix(filepath.ToSlash(file), boardDir+"/"); under {
			files = append(files, rest)
		}
	}
	if len(files) == 0 {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(root, boardDir, "node_modules")); err != nil {
		return nil, []Deferred{{
			Check: "the board's type check, lint, unit tests and browser specs",
			Why:   boardDir + "/node_modules is missing here, so run npm ci in " + boardDir + " and this again to check the board before you push",
		}}
	}
	why := "a file under " + boardDir + " changed"
	steps = []Step{
		{What: "the board's type check", Why: why, Dir: boardDir, Command: []string{"npm", "run", "typecheck"}},
		{What: "the board's lint", Why: why, Dir: boardDir, Command: []string{"npm", "run", "lint"}},
		{What: "the board's unit tests", Why: why, Dir: boardDir, Command: []string{"npm", "test"}},
	}

	entries, _ := os.ReadDir(filepath.Join(root, boardDir, "tests"))
	// Each rule's specs come before the next rule's, so the specs surest to
	// cover the change run first.
	picked := make([][]string, 4)
	others := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".spec.ts") {
			continue
		}
		source, _ := fsx.ReadFile(filepath.Join(root, boardDir, "tests", name))
		rule, reason := covers(name, string(source), files)
		if rule < 0 {
			others++
			continue
		}
		picked[rule] = append(picked[rule], "tests/"+name+" ("+reason+")")
	}
	if specs := slices.Concat(picked...); len(specs) > 0 {
		step := Step{
			What:    fmt.Sprintf("%d browser %s", len(specs), plural(len(specs), "spec", "specs")),
			Why:     "the change touched or names them",
			Detail:  specs,
			Dir:     boardDir,
			Command: []string{"npx", "playwright", "test", "--reporter=line"},
		}
		for _, spec := range specs {
			file, _, _ := strings.Cut(spec, " ")
			step.Command = append(step.Command, file)
		}
		steps = append(steps, step)
	}
	if others > 0 {
		left = append(left, Deferred{
			Check: fmt.Sprintf("the %d other browser %s", others, plural(others, "spec", "specs")),
			Why:   "the change touched no file of theirs and names none of them",
		})
	}
	return steps, append(left, Deferred{
		Check: "the board's build, the test that cfo embeds it and the licence check",
		Why:   "CI's frontend job runs them",
	})
}

// covers says which rule has the browser spec name, whose text is source,
// cover a change to files, paths from the board's folder, and why in the
// pick's words: 0 when it changed, 1 when it opens a changed fixture page, 2
// when it imports a changed file beside the specs, 3 when it shares a word of
// its name with a changed source, and -1 when none holds.
func covers(name, source string, files []string) (rule int, why string) {
	if slices.Contains(files, "tests/"+name) {
		return 0, "changed"
	}
	for _, match := range opensFixture.FindAllStringSubmatch(source, -1) {
		if slices.ContainsFunc(files, func(file string) bool {
			return path.Dir(file) == "tests/fixtures" && strings.TrimSuffix(path.Base(file), path.Ext(file)) == match[1]
		}) {
			return 1, "opens the changed fixture " + match[1]
		}
	}
	for _, match := range importsHelper.FindAllStringSubmatch(source, -1) {
		if helper := "tests/" + match[1] + ".ts"; slices.Contains(files, helper) {
			return 2, "imports the changed " + helper
		}
	}
	own := words(name)
	for _, file := range files {
		if path.Dir(file) != "src" || strings.HasSuffix(file, ".test.ts") {
			continue
		}
		if slices.ContainsFunc(words(path.Base(file)), func(word string) bool { return slices.Contains(own, word) }) {
			return 3, "named like the changed " + file
		}
	}
	return -1, ""
}

// words are the words of a file's name, in lower case, without what kind of
// file it is: RunCard.tsx is run and card, and run-terminal.spec.ts is run
// and terminal. A word under three letters is left out: it is a letter of an
// abbreviation written in capitals, which names nothing by itself.
func words(name string) []string {
	name, _, _ = strings.Cut(name, ".")
	var found []string
	start := 0
	end := func(index int) {
		if index-start >= 3 {
			found = append(found, strings.ToLower(name[start:index]))
		}
	}
	for index, letter := range name {
		switch {
		case !unicode.IsLetter(letter) && !unicode.IsDigit(letter):
			end(index)
			start = index + 1
		case unicode.IsUpper(letter):
			end(index)
			start = index
		}
	}
	end(len(name))
	return found
}
