package codegoblins

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// shellProgram is cmd, named alone or by its path.
var shellProgram = regexp.MustCompile(`(?i)(^|[\\/])cmd(\.exe)?$`)

// pingProgram is ping, named alone or by its path.
var pingProgram = regexp.MustCompile(`(?i)(^|[\\/])ping(\.exe)?$`)

// pingWord is ping named in a command line.
var pingWord = regexp.MustCompile(`(?i)\bping\b`)

// pingScan is what scanShellPings read and found: the test files, the
// starts of ping in them, directly or through cmd, and each start through
// cmd it reports.
type pingScan struct {
	files    int
	starts   int
	findings []string
}

// scanShellPings reads every Go test file under root for a start of ping
// through cmd.
func scanShellPings(root string) pingScan {
	var scan pingScan
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		scan.files++
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		starts, findings, err := shellPings(filepath.ToSlash(relative), source)
		if err != nil {
			scan.findings = append(scan.findings, fmt.Sprintf("%s does not parse, so its starts went unread: %v", filepath.ToSlash(relative), err))
			return nil
		}
		scan.starts += starts
		scan.findings = append(scan.findings, findings...)
		return nil
	})
	if err != nil {
		scan.findings = append(scan.findings, err.Error())
	}
	return scan
}

// shellPings parses a Go test file and counts each start of ping in it: a
// call of Command or CommandContext whose program is ping, or is cmd with a
// command line that names ping, each as a literal. It reports, as
// path:line, each start through cmd that is not handed straight to
// StartTree, which ends a process with everything it started.
func shellPings(relative string, source []byte) (int, []string, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, relative, source, parser.SkipObjectResolution)
	if err != nil {
		return 0, nil, err
	}
	starts := 0
	var findings []string
	treed := map[ast.Expr]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if calleeName(call) == "StartTree" {
			for _, argument := range call.Args {
				treed[argument] = true
			}
			return true
		}
		program, commandLine := startedProgram(call)
		switch {
		case pingProgram.MatchString(program):
			starts++
		case shellProgram.MatchString(program) && slices.ContainsFunc(commandLine, pingWord.MatchString):
			starts++
			if !treed[call] {
				findings = append(findings, fmt.Sprintf("%s:%d: starts ping through %s, and ending the %s leaves the ping running: start ping itself", relative, fileSet.Position(call.Pos()).Line, program, program))
			}
		}
		return true
	})
	return starts, findings, nil
}

// startedProgram is the program a call of Command or CommandContext starts
// and the arguments it hands it, each as its literal's text, or "" where it
// is no literal. A call of anything else starts no program.
func startedProgram(call *ast.CallExpr) (string, []string) {
	var texts []string
	for _, argument := range call.Args {
		literal, ok := argument.(*ast.BasicLit)
		text := ""
		if ok && literal.Kind == token.STRING {
			text, _ = strconv.Unquote(literal.Value)
		}
		texts = append(texts, text)
	}
	switch name := calleeName(call); {
	case name == "Command" && len(texts) > 0:
		return texts[0], texts[1:]
	case name == "CommandContext" && len(texts) > 1:
		return texts[1], texts[2:]
	}
	return "", nil
}

// calleeName is the name a call names its function by, without its package
// or receiver.
func calleeName(call *ast.CallExpr) string {
	switch function := call.Fun.(type) {
	case *ast.Ident:
		return function.Name
	case *ast.SelectorExpr:
		return function.Sel.Name
	}
	return ""
}

func TestShellPingsReportsEachPingStartedThroughCmd(t *testing.T) {
	for _, test := range []struct {
		name       string
		body       string
		isStart    bool
		isReported bool
	}{
		{"through cmd", `_ = exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL").Start()`, true, true},
		{"through cmd.exe with its words apart", `_ = exec.Command("cmd.exe", "/d", "/c", "ping", "-n", "60", "127.0.0.1").Start()`, true, true},
		{"through cmd under a context", `_ = exec.CommandContext(ctx, "cmd", "/c", "ping -n 30 127.0.0.1 >NUL").Start()`, true, true},
		{"through cmd by its path", `_ = execx.Command("C:\\Windows\\System32\\cmd.exe", "/c", "PING.EXE -n 30 127.0.0.1").Start()`, true, true},
		{"directly", `_ = exec.Command("ping", "-n", "30", "127.0.0.1").Start()`, true, false},
		{"directly by its path", `_ = exec.Command("C:\\Windows\\System32\\PING.EXE", "-n", "30", "127.0.0.1").Start()`, true, false},
		{"through cmd into a tree", `tree, _ := execx.StartTree(execx.Command("cmd.exe", "/d", "/c", "ping -n 30 127.0.0.1 >nul")); _ = tree`, true, false},
		{"cmd running something else", `_ = exec.Command("cmd", "/c", "exit 0").Run()`, false, false},
		{"a command line no call starts", `_ = []string{"cmd", "/d", "/c", "start", "/b", "ping", "-n", "60", "127.0.0.1"}`, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			source := "package example\n\nfunc TestExample(t *testing.T) {\n" + test.body + "\n}\n"

			// Act
			starts, findings, err := shellPings("internal/example/example_test.go", []byte(source))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if isStart := starts == 1; isStart != test.isStart {
				t.Errorf("counted %d starts of ping, want a start = %v", starts, test.isStart)
			}
			if isReported := len(findings) > 0; isReported != test.isReported {
				t.Errorf("reported %q, want reported = %v", findings, test.isReported)
			}
		})
	}
}

// A test that needs a live process to stand in for another starts ping
// itself, never through cmd. A test ends the process it started, and ending
// a cmd leaves the ping it started running for as long as its count. A cfo
// verify or cfo gate turn run lets what a command leaves running linger ten
// seconds and then names it as the run's leftover and ends it, so each ping
// -n 30 or -n 60 that tests left this way was named by a run that ended
// soon after (#1117, #1207). A start handed straight to StartTree is ended
// with everything it started. The scan reads literals only, so a program or
// a command line held in a name gets past it.
func TestNoTestStartsPingThroughCmd(t *testing.T) {
	// Act
	scan := scanShellPings(".")

	// Assert
	if scan.files < 100 {
		t.Fatalf("the scan read %d test files, so it is not reading this repository's tests", scan.files)
	}
	if scan.starts == 0 {
		t.Fatalf("the scan found no start of ping in %d test files, so it no longer sees one", scan.files)
	}
	for _, finding := range scan.findings {
		t.Error(finding)
	}
}
