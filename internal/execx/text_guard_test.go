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
	"unicode/utf8"
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
// pipeline.StartOf and the merge train do. A short text the program's own
// source fixes is not free text, so a flag such a text follows passes: one
// line of at most fixedTextLimit characters, written out or a constant of the
// same file, as the description of the label a merge train makes is. What a
// person or a model wrote, and any text the guard cannot read in that file,
// does not pass. The guard knows a flag by its name alone, so text handed
// over as a bare argument, or by a flag it does not list, gets past it. Test
// files and tests/, a test fixture's own program, are not fleet programs.
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
			violations = append(violations, relative+":"+strconv.Itoa(flag.line)+": names "+flag.name+", which puts its text on a command line; write the text to a file and name the file, or give one short line the program itself fixes as a constant of this file")
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
		{"a fixed text by a literal", `run("gh", "label", "create", name, "--description", "A merge train's own pull request")`, nil},
		{"a fixed text by a constant of the file", `run("gh", "label", "create", name, "--description", labelSays, "--color", "8B949E")`, nil},
		{"a fixed text as long as a short one may be", `run("gh", "label", "create", "--description", "` + strings.Repeat("a", fixedTextLimit) + `")`, nil},
		{"a fixed text too long to be short", `run("gh", "label", "create", "--description", "` + strings.Repeat("a", fixedTextLimit+1) + `")`, []string{"--description"}},
		{"a fixed text of two lines", `run("gh", "label", "create", "--description", "one\ntwo")`, []string{"--description"}},
		{"a constant with passed-in text added", `run("gh", "label", "create", "--description", labelSays+text)`, []string{"--description"}},
		{"a constant's name taken by passed-in text", `labelSays := text; run("gh", "label", "create", "--description", labelSays)`, []string{"--description"}},
		{"a variable that holds a fixed text", `var says = "fixed"; run("gh", "label", "create", "--description", says)`, []string{"--description"}},
		{"a flag with nothing after it", `args = append(args, "--message")`, []string{"--message"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			source := "package example\n\nconst labelSays = \"A label the program makes\"\n\nfunc example(args []string, intent, text, body, file string) {\n" + test.body + "\n}\n"

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

// fixedTextLimit is how many characters a fixed text may have and still be
// short, which is as long as GitHub lets a label's description be.
const fixedTextLimit = 100

// textFlag is one place a file names a flag of textFlags.
type textFlag struct {
	name string
	line int
}

// textFlagsNamed parses a fleet file's source and returns each string in it
// that is, whole, a flag of textFlags, but for one a short fixed text
// follows in the same list of arguments.
func textFlagsNamed(relative string, source []byte) ([]textFlag, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, relative, source, 0)
	if err != nil {
		return nil, err
	}
	beforeFixedText := map[token.Pos]bool{}
	mark := func(arguments []ast.Expr) {
		for index := 0; index+1 < len(arguments); index++ {
			if isShortFixedText(arguments[index+1]) {
				beforeFixedText[arguments[index].Pos()] = true
			}
		}
	}
	var found []textFlag
	// A list is visited before the strings in it, so each string's mark is
	// set by the time the string is read.
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.CallExpr:
			mark(node.Args)
		case *ast.CompositeLit:
			mark(node.Elts)
		case *ast.BasicLit:
			if node.Kind != token.STRING || beforeFixedText[node.Pos()] {
				return true
			}
			if text, err := strconv.Unquote(node.Value); err == nil && slices.Contains(textFlags, text) {
				found = append(found, textFlag{name: text, line: fileSet.Position(node.Pos()).Line})
			}
		}
		return true
	})
	return found, nil
}

// isShortFixedText reports whether an argument is a text the file's own
// source fixes: a string written out, or a constant the file declares as
// one, of a single line and at most fixedTextLimit characters. A variable, a
// call, a text put together and a name another file declares are not: the
// guard cannot see what they hold, so it takes them for free text.
func isShortFixedText(argument ast.Expr) bool {
	if name, isName := argument.(*ast.Ident); isName && name.Obj != nil && name.Obj.Kind == ast.Con {
		if declared, isDeclared := name.Obj.Decl.(*ast.ValueSpec); isDeclared {
			for index, declaredName := range declared.Names {
				if declaredName.Name == name.Name && index < len(declared.Values) {
					argument = declared.Values[index]
				}
			}
		}
	}
	literal, isLiteral := argument.(*ast.BasicLit)
	if !isLiteral || literal.Kind != token.STRING {
		return false
	}
	text, err := strconv.Unquote(literal.Value)
	return err == nil && !strings.ContainsAny(text, "\r\n") && utf8.RuneCountInString(text) <= fixedTextLimit
}
