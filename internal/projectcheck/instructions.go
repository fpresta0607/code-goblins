package projectcheck

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// instructionFiles are the files an agent reads for a project's commands.
var instructionFiles = []string{"AGENTS.md", "CLAUDE.md"}

// The kinds of command a check follows up, and the words that mark each. A
// deploy is looked for first, since `npm run deploy:test` deploys. An
// install is marked by where its word stands and not by a word anywhere, as
// installs says, and is looked for before a test, since the word that makes
// `npx playwright install` a test is its program's name.
var commandKinds = []struct {
	kind  string
	words []string
}{
	{"deploy", []string{"deploy", "publish", "migrate"}},
	{"install", nil},
	{"test", []string{"test", "pytest", "vitest", "jest", "playwright"}},
	{"lint", []string{"lint", "ruff", "eslint", "vet", "tsc", "typecheck", "fmt", "format", "audit", "check"}},
	{"build", []string{"build", "ci", "install", "compile"}},
}

// startsCommand are the programs a code span has to start with to be read
// as a command of the project rather than a phrase, a name or a setting.
// Prose puts all of those in code spans, so a span starting with any other
// word is passed over.
var startsCommand = map[string]bool{
	"npm": true, "npx": true, "pnpm": true, "yarn": true, "bun": true, "node": true, "deno": true,
	"go": true, "cargo": true, "make": true, "dotnet": true, "mvn": true, "gradle": true,
	"python": true, "python3": true, "py": true, "uv": true, "pip": true, "poetry": true, "tox": true,
	"pytest": true, "vitest": true, "jest": true, "playwright": true,
	"ruff": true, "eslint": true, "tsc": true, "mypy": true, "prettier": true, "golangci-lint": true,
	"bash": true, "sh": true, "pwsh": true, "powershell": true,
	"docker": true, "fly": true, "flyctl": true, "vercel": true, "netlify": true, "railway": true,
	"wrangler": true, "supabase": true, "alembic": true, "terraform": true, "kubectl": true,
}

// shellFences are the languages of a code block whose lines are commands.
var shellFences = map[string]bool{"bash": true, "sh": true, "shell": true, "console": true, "powershell": true, "pwsh": true, "ps1": true, "cmd": true, "bat": true}

var (
	codeSpan = regexp.MustCompile("`([^`\n]+)`")
	// wordBreak splits an argument into the words a kind is read from.
	wordBreak = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

// runsByPath reports whether the first word of a span names a program by
// its path: one under the folder it runs in, or a script or a program file.
func runsByPath(first string) bool {
	extension := strings.ToLower(path.Ext(filepath.ToSlash(first)))
	return strings.HasPrefix(first, "./") || strings.HasPrefix(first, `.\`) || scriptExtensions[extension] || extension == ".exe"
}

// instructionCommand is a command an instruction file names.
type instructionCommand struct {
	kind, text, where string
}

// installs reports whether a command installs dependencies, by the word its
// program takes for that: npm ci, uv sync, go mod download, and install in
// any of the first places a subcommand stands, as in npm install, uv pip
// install, python -m pip install and npx playwright install. The word is
// read as an argument of its own, so npm run test:ci installs nothing.
func installs(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	switch argv[0] + " " + argv[1] {
	case "npm ci", "uv sync", "poetry sync", "dotnet restore", "cargo fetch":
		return true
	}
	if len(argv) > 2 && argv[0] == "go" && argv[1] == "mod" && argv[2] == "download" {
		return true
	}
	return slices.Contains(argv[1:min(len(argv), 4)], "install")
}

// commandKind says whether a command deploys, installs, tests, lints or
// builds, or returns "" for any other.
func commandKind(argv []string) string {
	for _, kind := range commandKinds {
		if kind.kind == "install" && installs(argv) {
			return kind.kind
		}
		for _, arg := range argv {
			for _, part := range wordBreak.Split(strings.ToLower(arg), -1) {
				if slices.Contains(kind.words, part) {
					return kind.kind
				}
			}
		}
	}
	return ""
}

// instructionCommands reads the build, test, lint and deploy commands a
// file's code spans and shell code blocks name, each once, with its line. A
// span with a placeholder in it, such as <id>, is a pattern and not a
// command, and a code block in no shell language is a listing.
func instructionCommands(file string, data []byte) []instructionCommand {
	var commands []instructionCommand
	seen := map[string]bool{}
	fenced, shell := false, false
	for index, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		var spans []string
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "```"):
			fenced = !fenced
			shell = fenced && shellFences[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(line), "```"))]
		case fenced && shell:
			spans = []string{strings.TrimPrefix(strings.TrimSpace(line), "$ ")}
		case fenced:
		default:
			for _, match := range codeSpan.FindAllStringSubmatch(line, -1) {
				spans = append(spans, strings.TrimSpace(match[1]))
			}
		}
		for _, span := range spans {
			argv := strings.Fields(span)
			if len(argv) < 2 || seen[span] || strings.ContainsAny(span, "<>[]{}|") {
				continue
			}
			if !startsCommand[argv[0]] && !runsByPath(argv[0]) {
				continue
			}
			if kind := commandKind(argv); kind != "" {
				seen[span] = true
				commands = append(commands, instructionCommand{kind, span, fmt.Sprintf("%s:%d", file, index+1)})
			}
		}
	}
	return commands
}

// instructions assesses the commands the project's instruction files name
// for building, testing, linting and deploying: that each can run as
// written. It starts none of them. The ones that are there are listed with
// their kind for whoever proves them by running or dry-running each, and a
// deploy is listed apart, since a deploy is never run.
func (c *checker) instructions(ctx context.Context) {
	var commands []instructionCommand
	var read []string
	for _, file := range instructionFiles {
		if data, ok := c.repo.read(ctx, file); ok {
			read = append(read, file)
			commands = append(commands, instructionCommands(file, data)...)
		}
	}
	if len(read) == 0 {
		c.add(AreaInstructions, "instructions-missing", Low,
			"the repository has no instruction file, so an agent has to find its commands by reading it",
			"neither "+strings.Join(instructionFiles, " nor ")+" at "+c.repo.at(),
			"commit an AGENTS.md that names how the project is built, tested, linted and deployed")
		return
	}
	var found, missing, deploys, installed, examples []string
	for _, command := range commands {
		if command.kind == "deploy" {
			deploys = append(deploys, fmt.Sprintf("`%s` (%s)", command.text, command.where))
			continue
		}
		var p proof
		for _, s := range segments(command.text) {
			next := c.prove(ctx, s, inProse)
			p.faults = append(p.faults, next.faults...)
			p.notes = append(p.notes, next.notes...)
			p.examples = append(p.examples, next.examples...)
		}
		switch {
		case len(p.faults) > 0:
			missing = append(missing, fmt.Sprintf("%s `%s`: %s", command.where, command.text, strings.Join(p.faults, ", ")))
		case len(p.examples) > 0:
			examples = append(examples, fmt.Sprintf("`%s` (%s, %s)", command.text, command.where, strings.Join(p.examples, ", ")))
		case command.kind == "install":
			installed = append(installed, fmt.Sprintf("`%s` (%s)", command.text, command.where))
		default:
			found = append(found, fmt.Sprintf("%s `%s` (%s)", command.kind, command.text, strings.Join(append([]string{command.where}, p.notes...), ", ")))
			c.draftTest(ctx, command)
		}
	}
	at := ". Read at " + c.repo.at()
	if len(missing) > 0 {
		c.add(AreaInstructions, "instruction-command-missing", High,
			"the instructions name a command that cannot run as written, so an agent that follows them fails",
			strings.Join(missing, ". ")+at,
			"correct the command in the instruction file, or restore what it names")
	}
	if len(found) > 0 {
		c.add(AreaInstructions, "instruction-commands-found", OK,
			count(len(found), "build, test and lint command")+" of the instructions can run as written, and each still has to be run or dry-run to prove it",
			strings.Join(found, ", ")+at, "")
	}
	if len(installed) > 0 {
		c.add(AreaInstructions, "instruction-install-not-run", OK,
			count(len(installed), "command")+" of the instructions install dependencies, which a worktree's own install step does, and are not run to prove them",
			strings.Join(installed, ", ")+at, "")
	}
	if len(examples) > 0 {
		c.add(AreaInstructions, "instruction-example-paths", OK,
			count(len(examples), "command")+" of the instructions name a path made up to show the shape of a command, and are not judged as missing files",
			strings.Join(examples, ", ")+". A path counts as made up only when its file name holds a placeholder word and no commit "+c.repo.ref+" can reach ever held it"+at, "")
	}
	if len(deploys) > 0 {
		c.add(AreaInstructions, "instruction-deploy-never-run", OK,
			count(len(deploys), "command")+" of the instructions deploy, publish or migrate, and are never run to prove them",
			strings.Join(deploys, ", ")+at, "")
	}
	if len(commands) == 0 {
		c.add(AreaInstructions, "instruction-commands-none", Low,
			"the instructions name no build, test, lint or deploy command, so an agent has to find them by reading the repository",
			"no such command in a code span of "+strings.Join(read, " or ")+at,
			"name the project's build, test, lint and deploy commands in its instruction file")
	}
}

// draftTest keeps a test command of the instructions for the draft's fast
// tier when it can run as written at the root. Prose does not say where a
// command runs, so one that only a package file in a folder below defines
// is passed over: a record's command runs at the root.
func (c *checker) draftTest(ctx context.Context, command instructionCommand) {
	parts := segments(command.text)
	if command.kind != "test" || rootCommands(parts) == nil {
		return
	}
	for _, s := range parts {
		if len(c.prove(ctx, s, inWorktree).faults) > 0 {
			return
		}
	}
	c.proven.instructionTests = append(c.proven.instructionTests, rootCommands(parts)...)
	if file, _, _ := strings.Cut(command.where, ":"); !slices.Contains(c.proven.instructionFiles, file) {
		c.proven.instructionFiles = append(c.proven.instructionFiles, file)
	}
}
