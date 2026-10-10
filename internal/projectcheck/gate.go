package projectcheck

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// gateFileName is the settings file of the no-mistakes gate, which the gate
// reads from the default branch.
const gateFileName = ".no-mistakes.yaml"

// gateFile is what a check reads of the gate's settings.
type gateFile struct {
	Commands map[string]string `yaml:"commands"`
	Gates    []struct {
		Name    string `yaml:"name"`
		Command string `yaml:"command"`
	} `yaml:"gates"`
}

// gate assesses the project's verification gate: that its settings file is
// on the default branch, that each command it names can run as written, that
// the test step is the repository's own command, and that CI runs the test
// runners the gate does. It returns the gate's test command, empty when an
// agent chooses the step.
func (c *checker) gate(ctx context.Context) string {
	data, ok := c.repo.read(ctx, gateFileName)
	if !ok {
		c.add(AreaGate, "gate-file-missing", Medium,
			"the repository has no gate settings, so an agent chooses every step of the gate, the test step included",
			"no "+gateFileName+" at "+c.repo.at(),
			"commit a "+gateFileName+" whose commands.test is the project's own test command")
		c.workflows(ctx, "")
		return ""
	}
	var gate gateFile
	if err := yaml.Unmarshal(data, &gate); err != nil {
		c.add(AreaGate, "gate-file-invalid", High,
			"the gate's settings do not parse, so the gate runs without them",
			fmt.Sprintf("%s at %s: %v", gateFileName, c.repo.at(), err), "correct the file")
		return ""
	}
	type named struct{ name, command string }
	var commands []named
	for _, step := range sortedKeys(gate.Commands) {
		commands = append(commands, named{"commands." + step, gate.Commands[step]})
	}
	for _, own := range gate.Gates {
		commands = append(commands, named{"gates." + own.Name, own.Command})
	}
	var found, missing []string
	for _, command := range commands {
		var faults []string
		for _, s := range segments(command.command) {
			faults = append(faults, c.prove(ctx, s, false).faults...)
		}
		if len(faults) > 0 {
			missing = append(missing, fmt.Sprintf("%s %q: %s", command.name, command.command, strings.Join(faults, ", ")))
			continue
		}
		found = append(found, fmt.Sprintf("%s %q", command.name, command.command))
		if parts := segments(command.command); command.name == "commands.test" && len(parts) == 1 && parts[0].dir == "" {
			c.proven.gateTest = parts[0].argv
		}
	}
	if len(missing) > 0 {
		c.add(AreaGate, "gate-command-missing", High,
			"a command the gate names cannot run as written, so its step fails or an agent chooses what runs instead",
			strings.Join(missing, ". ")+". Read at "+c.repo.at(),
			"name a command the repository has in "+gateFileName)
	}
	if len(found) > 0 {
		c.add(AreaGate, "gate-commands-found", OK,
			count(len(found), "command")+" of the gate can run as written: programs, files and scripts are there",
			strings.Join(found, ", ")+". Read at "+c.repo.at(), "")
	}
	test := strings.TrimSpace(gate.Commands["test"])
	if test == "" {
		c.add(AreaGate, "gate-test-unbounded", Medium,
			"the gate names no test command, so an agent chooses what its test step runs",
			gateFileName+" at "+c.repo.at()+" has no commands.test",
			"set commands.test in "+gateFileName+" to the project's own test command")
	}
	c.workflows(ctx, test)
	return test
}

// runners are the test runners a command or a workflow can be seen to
// start, each as the words that start it.
var runners = []string{"go test", "go vet", "pytest", "vitest", "jest", "playwright", "cargo test", "npm test", "dotnet test", "mvn test", "gradle test", "rspec", "phpunit"}

// runnersIn returns the runners a text names, a runner counting only where
// no letter or digit touches it, so pytest_changed.py names pytest and
// majestic does not name jest.
func runnersIn(text string) map[string]bool {
	found := map[string]bool{}
	for _, runner := range runners {
		pattern := regexp.MustCompile(`(^|[^A-Za-z0-9])` + strings.ReplaceAll(regexp.QuoteMeta(runner), " ", `\s+`) + `([^A-Za-z0-9]|$)`)
		if pattern.MatchString(text) {
			found[runner] = true
		}
	}
	return found
}

// workflows checks that the repository has a workflow for the gate's ci
// step to wait for, and that its workflows run the test runners the gate's
// test command starts.
func (c *checker) workflows(ctx context.Context, test string) {
	var files []string
	var text strings.Builder
	for _, name := range sortedKeys(c.repo.tracked) {
		if extension := path.Ext(name); path.Dir(name) == ".github/workflows" && (extension == ".yml" || extension == ".yaml") {
			files = append(files, name)
			data, _ := c.repo.read(ctx, name)
			text.Write(data)
			text.WriteByte('\n')
		}
	}
	if len(files) == 0 {
		c.add(AreaGate, "ci-missing", High,
			"the repository has no workflow, so the gate's ci step waits for a run that never starts",
			"no workflow file under .github/workflows at "+c.repo.at(),
			"commit a workflow that runs the project's tests on a pull request")
		return
	}
	if test == "" {
		return
	}
	// The gate's test command is the repository's own program more often
	// than a runner, so what it starts is read from the scripts it names.
	source := test
	for _, s := range segments(test) {
		for _, arg := range s.argv {
			if scriptExtensions[strings.ToLower(path.Ext(arg))] {
				data, _ := c.repo.read(ctx, path.Join(s.dir, arg))
				source += "\n" + string(data)
			}
		}
	}
	if strings.HasPrefix(test, "cfo gate test") {
		source += "\ngo vet\ngo test"
	}
	inGate, inCI := runnersIn(source), runnersIn(text.String())
	read := "Workflows read at " + c.repo.at() + ": " + strings.Join(files, ", ") + ", which run " + orNone(sortedKeys(inCI))
	if len(inGate) == 0 {
		c.add(AreaGate, "gate-ci-unknown", Low,
			"the gate's test command starts no test runner this check knows, so whether CI runs the same cannot be told",
			fmt.Sprintf("commands.test %q. %s", test, read),
			"compare the command with the workflows by hand")
		return
	}
	var alone []string
	for _, runner := range sortedKeys(inGate) {
		if !inCI[runner] {
			alone = append(alone, runner)
		}
	}
	if len(alone) > 0 {
		c.add(AreaGate, "gate-ci-differs", Medium,
			"the gate tests with a runner no workflow runs, so CI does not check again what the gate checked",
			fmt.Sprintf("commands.test %q starts %s, which no workflow runs. %s", test, strings.Join(alone, ", "), read),
			"run the same tests in a workflow, or take them out of the gate's test command")
		return
	}
	c.add(AreaGate, "gate-ci-agree", OK,
		"CI runs every test runner the gate's test command starts: "+strings.Join(sortedKeys(inGate), ", "),
		fmt.Sprintf("commands.test %q. %s", test, read), "")
}

// orNone joins names, or says none.
func orNone(names []string) string {
	if len(names) == 0 {
		return "none this check knows"
	}
	return strings.Join(names, ", ")
}
