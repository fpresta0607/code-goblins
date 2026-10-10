package projectcheck

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/project"
)

// gateFileName is the settings file of the no-mistakes gate, which the gate
// reads from the default branch.
const gateFileName = ".no-mistakes.yaml"

// gateFile is what a check reads of the gate's settings.
type gateFile struct {
	Agent    yaml.Node         `yaml:"agent"`
	AutoFix  yaml.Node         `yaml:"auto_fix"`
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
		c.add(AreaGate, "gate-file-missing", High,
			"the repository has no gate settings, so cfo pipeline run refuses every gate run of the project at its start, and a gate started any other way leaves every step to an agent's choice, the test step included",
			"no "+gateFileName+" at "+c.repo.at()+". A gate's start answers: "+pipelineWords(pipeline.ErrGateFileRequired),
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
	c.gateStart(data, gate)
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
			faults = append(faults, c.prove(ctx, s, inWorktree).faults...)
		}
		if len(faults) > 0 {
			missing = append(missing, fmt.Sprintf("%s %q: %s", command.name, command.command, strings.Join(faults, ", ")))
			continue
		}
		found = append(found, fmt.Sprintf("%s %q", command.name, command.command))
		if command.name == "commands.test" {
			c.proven.gateTest = rootCommands(segments(command.command))
		}
	}
	if len(missing) > 0 {
		c.add(AreaGate, "gate-command-missing", High,
			"a command the gate names cannot run as written, so its step fails or an agent chooses what runs instead",
			strings.Join(missing, ". ")+". Read at "+c.repo.asRead(),
			"name a command the repository has in "+gateFileName)
	}
	if len(found) > 0 {
		c.add(AreaGate, "gate-commands-found", OK,
			count(len(found), "command")+" of the gate can run as written: programs, files and scripts are there",
			strings.Join(found, ", ")+". Read at "+c.repo.asRead(), "")
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

// gateStart says whether a gate's start takes the gate file under the home's
// pipeline policy: the automatic fix counts it sets, the form the pipeline's
// own reader wants of it, and the agent it pins. Each verdict is the
// pipeline's own, from the function that start asks. A file that sets no
// count and pins no agent, and that the pipeline's reader takes, says nothing.
func (c *checker) gateStart(data []byte, gate gateFile) {
	file := gateFileName + " at " + c.repo.asRead()
	policy, err := pipeline.Load(c.PolicyFile)
	if err != nil {
		unread := c.PolicyFile + " could not be read as a policy"
		if c.PolicyFile == "" {
			unread = "this run was given no policy file"
		}
		const fix = "run cfo install to write the home's pipeline policy, and run the check again"
		if gate.AutoFix.Kind != 0 {
			c.add(AreaGate, "gate-limits-unjudged", Low,
				"the gate file sets automatic fix counts and the home has no pipeline policy this check can read, so whether a gate run takes them is not known",
				"auto_fix in "+file+". "+unread, fix)
		}
		if gate.Agent.Kind != 0 {
			c.add(AreaGate, "gate-agent-unjudged", Low,
				"the gate file pins an agent and the home has no pipeline policy this check can read, so whether a gate run takes the pin is not known",
				"agent "+strings.Join(agentNames(gate.Agent), ", ")+" in "+file+". "+unread, fix)
		}
		return
	}
	version := fmt.Sprintf("Policy version %d of %s", policy.Version, c.PolicyFile)
	switch refusal := pipeline.CheckRepoConfig(data, policy); {
	case refusal != nil:
		c.add(AreaGate, "gate-file-refused", High,
			"the home's pipeline policy refuses the gate file, so cfo pipeline run refuses every gate run of the project at its start",
			file+". "+version+" answers: "+pipelineWords(refusal),
			"correct what the answer names in "+gateFileName)
	case gate.AutoFix.Kind != 0:
		c.add(AreaGate, "gate-limits-read", OK,
			"the gate file sets automatic fix counts, and the home's pipeline policy takes them",
			"auto_fix in "+file+". "+version+" holds each count to its own as a ceiling", "")
	}
	if gate.Agent.Kind != 0 {
		c.gateAgent(data, gate.Agent, policy, version)
	}
}

// agentNames are the agents a gate file pins, as its field names them.
func agentNames(agent yaml.Node) []string {
	var names []string
	if agent.Decode(&names) != nil {
		var name string
		if agent.Decode(&name) != nil {
			name = "of a kind this check does not read"
		}
		names = []string{name}
	}
	return names
}

// pipelineWords are a refusal of the pipeline as a line of a report carries
// it: without the package's own prefix, and without a semicolon, which no
// line of a report holds.
func pipelineWords(refusal error) string {
	return strings.ReplaceAll(strings.TrimPrefix(refusal.Error(), "pipeline: "), ";", ",")
}

// gateAgent says whether the home's pipeline policy takes the agent a gate
// file pins. The pipeline is given the gate file as both the task's branch
// and the default branch have it, since a new task's branch starts as the
// default branch.
func (c *checker) gateAgent(data []byte, agent yaml.Node, policy pipeline.Policy, version string) {
	pinned := "agent " + strings.Join(agentNames(agent), ", ") + " in " + gateFileName + " at " + c.repo.asRead()
	switch refusal := pipeline.CheckRepoAgent(data, data, policy); {
	case refusal != nil:
		c.add(AreaGate, "gate-agent-refused", High,
			"the gate file pins an agent the home's pipeline policy refuses, so cfo pipeline run refuses every gate run of the project at its start",
			pinned+". "+version+" answers: "+pipelineWords(refusal),
			"take the agent field out of "+gateFileName+", or set it to the agent the policy names")
	case policy.Version >= 6:
		c.add(AreaGate, "gate-agent-read", OK,
			"the gate file pins an agent, and the home's pipeline policy does not refuse it",
			pinned+". "+version+" replaces a repository's agent with the task's own harness, so the pin decides nothing", "")
	default:
		c.add(AreaGate, "gate-agent-read", OK,
			"the gate file pins an agent, and the home's pipeline policy does not refuse it",
			pinned+". "+version+" takes it, since it is the policy's own gate agent", "")
	}
}

// runners are the test runners a command or a workflow can be seen to
// start, each as the words that start it.
var runners = []string{"go test", "go vet", "pytest", "vitest", "jest", "playwright", "cargo test", "npm test", "dotnet test", "mvn test", "gradle test", "rspec", "phpunit", "node --test", "tsx --test", "unittest"}

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

// rootCommands returns the parts of a command line as the commands a record
// holds, one for each part, or nothing when a part runs under a cd: a
// record's command is one program with its arguments, run at the root.
func rootCommands(parts []segment) []project.Command {
	var commands []project.Command
	for _, part := range parts {
		if part.dir != "" {
			return nil
		}
		commands = append(commands, part.argv)
	}
	return commands
}

// workflowFile is what a check reads of a workflow: the commands its steps
// run, and the folder a step, a job or the whole file runs them in.
type workflowFile struct {
	Defaults workflowDefaults `yaml:"defaults"`
	Jobs     map[string]struct {
		Defaults workflowDefaults `yaml:"defaults"`
		Steps    []struct {
			Run string `yaml:"run"`
			Dir string `yaml:"working-directory"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

type workflowDefaults struct {
	Run struct {
		Dir string `yaml:"working-directory"`
	} `yaml:"run"`
}

// workflowTests returns the test commands a workflow runs at the root of the
// repository that can run as written here: one for each line of a step that
// is a plain command, with no variable, pipe or other shell construct in it.
func (c *checker) workflowTests(ctx context.Context, data []byte) []project.Command {
	var workflow workflowFile
	if yaml.Unmarshal(data, &workflow) != nil {
		return nil
	}
	var tests []project.Command
	for _, job := range sortedKeys(workflow.Jobs) {
		for _, step := range workflow.Jobs[job].Steps {
			if step.Dir != "" || workflow.Jobs[job].Defaults.Run.Dir != "" || workflow.Defaults.Run.Dir != "" {
				continue
			}
			for _, line := range strings.Split(step.Run, "\n") {
				if strings.ContainsAny(line, "<>[]{}|$;`\\=#") {
					continue
				}
				for _, s := range segments(line) {
					if s.dir == "" && (startsCommand[s.argv[0]] || runsByPath(s.argv[0])) && commandKind(s.argv) == "test" && len(c.prove(ctx, s, inWorktree).faults) == 0 {
						tests = append(tests, s.argv)
					}
				}
			}
		}
	}
	return tests
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
			if tests := c.workflowTests(ctx, data); len(tests) > 0 {
				c.proven.workflowTests = append(c.proven.workflowTests, tests...)
				c.proven.workflowFiles = append(c.proven.workflowFiles, name)
			}
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
	read := "Workflows read at " + c.repo.asRead() + ": " + strings.Join(files, ", ") + ", which run " + orNone(sortedKeys(inCI))
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
