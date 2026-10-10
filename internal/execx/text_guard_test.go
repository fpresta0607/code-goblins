package execx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// textFlags are the flags a program takes free text by: an intent, a
// response's instructions, a pull request's body, a comment, a message, a
// prompt. Text given that way is on the program's command line.
var textFlags = []string{"--intent", "--instructions", "--body", "--comment", "--message", "--prompt", "--notes", "--description"}

// textOnCommandLines are the places a fleet program still names such a flag,
// each with why its text cannot go by a file or standard input.
var textOnCommandLines = map[string]string{
	"internal/pipeline/intent.go --intent":       "a no-mistakes before 1.86.0 has no --intent-file, and install.ps1 pins 1.75.1",
	"internal/pipeline/driver.go --instructions": "no-mistakes takes a response's instructions on its command line only",
	"internal/train/finish.go --comment":         "gh pr close takes its comment on its command line only, and the comment is one line the train writes",
}

// A long text reaches a program by a file or by standard input, never on its
// command line. On 2026-10-02 Microsoft Defender detected a gate run's intent,
// several thousand characters of prose that named a shell, on the command
// lines of no-mistakes and node as Trojan:Win32/ClickFix.DQ!MTB, the lure
// that has a person paste such a line, and Windows refused some of those
// starts. So no fleet program names a flag of textFlags outside
// textOnCommandLines: it writes the text to a file and names the file, as
// pipeline.StartOf and the merge train do. The guard knows a flag by its
// name alone, so text handed over as a bare argument, or by a flag it does
// not list, gets past it. Test files and tests/, a test fixture's own
// program, are not fleet programs.
func TestNoFleetProgramPutsFreeTextOnACommandLine(t *testing.T) {
	root := repositoryRoot(t)
	files, known := 0, map[string]int{}
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".worktrees", "node_modules", "testdata", "frontend":
				return filepath.SkipDir
			}
			if relative == "tests" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files++
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found, err := textFlagsNamed(relative, source)
		if err != nil {
			return err
		}
		for _, flag := range found {
			key := relative + " " + flag.name
			if _, isKnown := textOnCommandLines[key]; isKnown {
				known[key]++
				continue
			}
			violations = append(violations, relative+":"+strconv.Itoa(flag.line)+": names "+flag.name+", which puts its text on a command line; write the text to a file and name the file")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A guard that stops reading files approves everything, so it counts
	// what it read: the fleet has well over a hundred files.
	if files < 100 {
		t.Fatalf("the guard read %d Go files under %s; it is not reading the repository", files, root)
	}
	for key := range textOnCommandLines {
		if known[key] == 0 {
			t.Errorf("the guard no longer sees %s; remove it from textOnCommandLines if it is gone", key)
		}
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

func TestTextFlagsNamedReportsEveryFreeTextFlag(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want []string
	}{
		{"an intent in a command line", `_ = []string{"axi", "run", "--intent", intent}`, []string{"--intent"}},
		{"instructions appended", `args = append(args, "--instructions", text)`, []string{"--instructions"}},
		{"a body and a comment", `run("gh", "pr", "create", "--body", body); run("gh", "pr", "close", "--comment", text)`, []string{"--body", "--comment"}},
		{"an intent by file", `_ = []string{"axi", "run", "--intent-file", file}`, nil},
		{"a body by file", `run("gh", "pr", "create", "--body-file", file)`, nil},
		{"a flag of the fleet's own command", `flags.StringVar(&intent, "intent", "", "task intent")`, nil},
		{"a flag named in a sentence", `_ = "use cfo pipeline run --intent <text>"`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			source := "package example\n\nfunc example(args []string, intent, text, body, file string) {\n" + test.body + "\n}\n"

			// Act
			found, err := textFlagsNamed("internal/example/example.go", []byte(source))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, flag := range found {
				names = append(names, flag.name)
			}
			if !slices.Equal(names, test.want) {
				t.Errorf("reported %q, want %q", names, test.want)
			}
		})
	}
}

// textFlag is one place a file names a flag of textFlags.
type textFlag struct {
	name string
	line int
}

// textFlagsNamed parses a fleet file's source and returns each string in it
// that is, whole, a flag of textFlags.
func textFlagsNamed(relative string, source []byte) ([]textFlag, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, relative, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var found []textFlag
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		if text, err := strconv.Unquote(literal.Value); err == nil && slices.Contains(textFlags, text) {
			found = append(found, textFlag{name: text, line: fileSet.Position(literal.Pos()).Line})
		}
		return true
	})
	return found, nil
}
