package projectcheck

import (
	"context"
	"encoding/json"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// testSetupPrefixes start the name of a file a test runner loads before the
// tests, where a project pins what its tests may see.
var testSetupPrefixes = []string{"jest.setup.", "vitest.setup.", "setupTests.", "vitest.config.", "jest.config.", "playwright.config.", "global-setup.", "globalSetup.", ".mocharc.", "cypress.config."}

// testSetupSections are the files that are a test setup only when they hold
// the section a test runner reads, each with the text that starts it.
var testSetupSections = map[string]string{"pyproject.toml": "[tool.pytest", "setup.cfg": "[tool:pytest]"}

// isTestSetup reports whether a tracked file is one a test runner loads
// before the tests, by its name alone.
func isTestSetup(name string) bool {
	base := path.Base(name)
	switch base {
	case "conftest.py", "pytest.ini", "tox.ini", ".env.test", "phpunit.xml", "phpunit.xml.dist", "spec_helper.rb", "rails_helper.rb", "test_helper.rb":
		return true
	}
	for _, prefix := range testSetupPrefixes {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}

// preloadFlags are the flags whose argument is a file a runner loads before
// the tests.
var preloadFlags = map[string]bool{"--import": true, "--require": true, "-r": true}

// runsScripts are the programs that run the script file they are handed,
// and the wrappers that start one of those.
var runsScripts = map[string]bool{
	"python": true, "python3": true, "py": true, "node": true, "tsx": true, "ts-node": true, "deno": true, "bun": true,
	"bash": true, "sh": true, "pwsh": true, "powershell": true, "ruby": true,
	"uv": true, "npx": true, "poetry": true, "cross-env": true, "env": true,
}

// bareRunners are the test runners whose suites have no setup file: each
// test file sets what it needs, in a process or a package of its own, so
// nothing pins a variable for a whole run.
var bareRunners = []string{"node --test", "tsx --test", "go test", "cargo test", "dotnet test", "unittest"}

// testSetup is what the check reads of how the project's tests start: the
// places a project pins what its tests may see.
type testSetup struct {
	// files maps each file read as test setup to what it holds: a file a
	// runner loads before the tests, and the script or a preloaded file of
	// a test command.
	files map[string]string
	// commands maps each test command and each package script one runs to
	// its text, since a command can set a variable in front of what it runs.
	commands map[string]string
	// suites says, for each runner whose suites have no setup file, which
	// test commands start it.
	suites []string
}

// pinnedBy returns the place of the test setup that names a variable as a
// word of its own, or "" when none does.
func (s testSetup) pinnedBy(variable string) string {
	word := regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(variable) + `([^A-Za-z0-9_]|$)`)
	for _, places := range []map[string]string{s.files, s.commands} {
		for _, name := range sortedKeys(places) {
			if word.MatchString(places[name]) {
				return name
			}
		}
	}
	return ""
}

// read says what was read as test setup, for a line's evidence. A suite
// with no setup file is said to be one, so a setup the check could not find
// does not read the same as a suite that has none.
func (s testSetup) read() string {
	var parts []string
	if len(s.files) > 0 {
		parts = append(parts, strings.Join(sortedKeys(s.files), ", "))
	}
	parts = append(parts, s.suites...)
	if len(parts) == 0 {
		return orNone(nil)
	}
	return strings.Join(parts, ". ")
}

// testSetup reads how the project's tests start. It reads the files a test
// runner loads before the tests, by name, and then each test command: the
// gate's own, or with none, the ones the workflows run and the instruction
// files name, since an agent that chooses the test step chooses among those.
// A command is followed through the package script it runs to the commands
// that script comes to, and of each it reads the script it runs and the
// files it loads before the tests. A test file a command names is no setup:
// one that reads a variable has not pinned it.
func (c *checker) testSetup(ctx context.Context, test string) testSetup {
	setup := testSetup{files: map[string]string{}, commands: map[string]string{}}
	for _, name := range sortedKeys(c.repo.tracked) {
		section, sectioned := testSetupSections[path.Base(name)]
		if !isTestSetup(name) && !sectioned {
			continue
		}
		if data, ok := c.repo.read(ctx, name); ok && strings.Contains(string(data), section) {
			setup.files[name] = string(data)
		}
	}
	commands := map[string]string{}
	if test != "" {
		commands[test] = gateFileName + " commands.test"
	} else {
		for _, command := range c.proven.workflowTests {
			commands[strings.Join(command, " ")] = "a workflow's test command"
		}
		for _, file := range instructionFiles {
			if data, ok := c.repo.read(ctx, file); ok {
				for _, command := range instructionCommands(file, data) {
					// A shell block ends a command with a comment.
					text, _, _ := strings.Cut(command.text, " #")
					if _, known := commands[strings.TrimSpace(text)]; command.kind == "test" && !known {
						commands[strings.TrimSpace(text)] = command.where
					}
				}
			}
		}
	}
	starts := map[string][]string{}
	for _, line := range sortedKeys(commands) {
		setup.commands[commands[line]+" `"+line+"`"] = line
		runs, parts := c.followScripts(ctx, line, setup.commands)
		said := "`" + line + "`"
		if runs != "" {
			said += " runs `" + runs + "`"
		}
		var text []string
		for _, s := range parts {
			text = append(text, strings.Join(s.argv, " "))
			for _, name := range append(preloads(s.argv), scriptRun(s.argv)) {
				if name == "" {
					continue
				}
				file := path.Join(s.dir, filepath.ToSlash(name))
				if data, ok := c.repo.read(ctx, file); ok {
					setup.files[file] = string(data)
				}
			}
		}
		started := runnersIn(strings.Join(text, "\n"))
		for _, runner := range bareRunners {
			if started[runner] {
				starts[runner] = append(starts[runner], said)
			}
		}
	}
	for _, runner := range bareRunners {
		if len(starts[runner]) > 0 {
			setup.suites = append(setup.suites, runner+" has no setup file, so each test file sets what it needs: "+strings.Join(starts[runner], ", "))
		}
	}
	return setup
}

// preloads returns the files a command loads before the tests, by the flags
// a runner takes for that.
func preloads(argv []string) []string {
	var files []string
	for index, arg := range argv {
		flag, value, joined := strings.Cut(arg, "=")
		switch {
		case preloadFlags[flag] && joined:
			files = append(files, value)
		case preloadFlags[arg] && index+1 < len(argv):
			files = append(files, argv[index+1])
		}
	}
	return files
}

// scriptRun returns the script file a command hands an interpreter to run,
// or "" when it runs none: the first script file after the interpreter and
// its wrappers. What follows that file are the script's own arguments, and
// a file handed to a test runner, to a module or to --test is a test, so
// neither is returned.
func scriptRun(argv []string) string {
	for index := 0; index < len(argv); index++ {
		arg := argv[index]
		name := strings.TrimSuffix(strings.ToLower(path.Base(filepath.ToSlash(arg))), ".exe")
		switch {
		case preloadFlags[arg]:
			index++
		case arg == "-m" || arg == "-c" || arg == "-e" || arg == "--eval" || arg == "--test":
			return ""
		case strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") || runsScripts[name]:
		case scriptExtensions[strings.ToLower(path.Ext(arg))]:
			return arg
		case index == 0 || startsCommand[name]:
			return ""
		}
	}
	return ""
}

// followScripts follows a command line through the package scripts it runs,
// three deep at most, to the commands those come to. It returns the text of
// the scripts the line itself runs, joined as the line joins them and empty
// when it runs none, and keeps the text of each script it passed through in
// texts, under the script's name.
func (c *checker) followScripts(ctx context.Context, line string, texts map[string]string) (runs string, parts []segment) {
	var own []string
	var follow func(s segment, depth int)
	follow = func(s segment, depth int) {
		if dir, script := packageScript(s); script != "" && depth < 3 {
			if text := c.scriptText(ctx, dir, script); text != "" {
				if depth == 0 {
					own = append(own, text)
				}
				texts[path.Join(dir, "package.json")+" script "+script] = text
				for _, next := range segments(text) {
					next.dir = path.Join(dir, next.dir)
					follow(next, depth+1)
				}
				return
			}
		}
		parts = append(parts, s)
	}
	for _, s := range segments(line) {
		follow(s, 0)
	}
	return strings.Join(own, " && "), parts
}

// scriptText returns what a script of the package file in dir runs, as the
// default branch has it, or "" when there is no such script.
func (c *checker) scriptText(ctx context.Context, dir, script string) string {
	data, ok := c.repo.read(ctx, path.Join(dir, "package.json"))
	if !ok {
		return ""
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	return manifest.Scripts[script]
}
