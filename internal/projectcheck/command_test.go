package projectcheck

import (
	"strings"
	"testing"
)

// A gate file and an instruction file are read at the default branch, so the
// package script a command of theirs runs is looked up at that commit too. A
// folder that was fetched and never pulled answered for an older repository:
// two false highs on one project, whose folder was 158 commits behind.
func TestAPackageScriptIsLookedUpAtTheCommitItsCommandCameFrom(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"package.json": `{"scripts":{"old":"node old.js"}}`})
	f.write("package.json", `{"scripts":{"type-check":"tsc --noEmit","test:cms":"node --test"}}`)
	f.write(".no-mistakes.yaml", "commands:\n  lint: \"npm run type-check\"\n")
	f.write("CLAUDE.md", "Test the content with `npm run test:cms`.\n")
	f.commit("the scripts the gate and the instructions name")
	f.lag()

	// Act
	report := f.check("npm")

	// Assert
	none(t, report, "gate-command-missing")
	none(t, report, "instruction-command-missing")
	contains(t, "evidence", only(t, report, "gate-commands-found").Evidence, "npm run type-check")
	contains(t, "evidence", only(t, report, "instruction-commands-found").Evidence, "script test:cms of package.json")
}

// The other way round, a folder's own copy must not vouch for a script the
// default branch does not have: a worktree is cut from the default branch.
func TestAPackageScriptOnlyTheFoldersCopyDefinesIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		".no-mistakes.yaml": "commands:\n  lint: \"npm run lint\"\n",
		"package.json":      `{"scripts":{"build":"vite build"}}`,
	})
	f.write("package.json", `{"scripts":{"build":"vite build","lint":"eslint ."}}`)

	// Act
	report := f.check("npm")

	// Assert
	finding := only(t, report, "gate-command-missing")
	contains(t, "evidence", finding.Evidence, "no script lint in package.json")
}

// A file a command names is judged the same way. A copy that only the branch
// the folder sits on tracks is not in a worktree, and a file the default
// branch gained since the folder was last pulled is.
func TestAFileACommandNamesIsJudgedByTheDefaultBranch(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		"AGENTS.md":            "Check with `python scripts/old_check.py`, test with `python scripts/new_test.py` and `./scripts/test.sh --all`.\n",
		"scripts/old_check.py": "print('old')\n",
	})
	f.git("rm", "--quiet", "scripts/old_check.py")
	f.write("scripts/new_test.py", "print('new')\n")
	f.write("scripts/test.sh", "#!/bin/sh\n")
	f.commit("the default branch drops one script and gains two")
	f.lag()

	// Act
	report := f.check("python")

	// Assert
	finding := only(t, report, "instruction-command-missing")
	contains(t, "evidence", finding.Evidence, "scripts/old_check.py", "the branch the folder is on")
	for _, there := range []string{"new_test.py", "scripts/test.sh"} {
		if strings.Contains(finding.Evidence, there) {
			t.Errorf("evidence %q names %s, which the default branch tracks", finding.Evidence, there)
		}
	}
	contains(t, "evidence", only(t, report, "instruction-commands-found").Evidence, "scripts/new_test.py", "./scripts/test.sh --all")
}

// What a command writes does not have to exist before it runs. The target of
// go build -o was read as a package folder, which is half of a false high.
func TestAFileACommandWritesIsNotOneThatMustExist(t *testing.T) {
	for _, test := range []struct{ name, command string }{
		{"the target of -o", "go build -o ./bin/app ./cmd/app"},
		{"the target of a flag named for output", "npx esbuild src/app.ts --outfile dist/app.js"},
		{"the target of a redirect", "node scripts/generate.mjs > src/generated.ts"},
		{"the target of a redirect with no space", "node scripts/generate.mjs >src/generated.ts"},
		{"the destination of a copy", "cp scripts/hook.sh hooks/pre-commit.sh"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, map[string]string{
				".no-mistakes.yaml":    "commands:\n  format: \"" + test.command + "\"\n",
				"cmd/app/main.go":      "package main\n\nfunc main() {}\n",
				"src/app.ts":           "export {}\n",
				"scripts/generate.mjs": "console.log('')\n",
				"scripts/hook.sh":      "#!/bin/sh\n",
			})

			// Act
			report := f.check("go", "npx", "node", "cp")

			// Assert
			none(t, report, "gate-command-missing")
		})
	}
}

// The rule passes over what is written and nothing else: a package or a
// script the same command reads is still reported when it is not there.
func TestWhatACommandReadsBesideWhatItWritesIsStillReported(t *testing.T) {
	for _, test := range []struct{ name, command, missing, written string }{
		{"the package of go build -o", "go build -o ./bin/app ./cmd/gone", "cmd/gone", "bin/app"},
		{"the script before a redirect", "node scripts/gone.mjs > src/generated.ts", "scripts/gone.mjs", "src/generated.ts"},
		{"the source of a copy", "cp scripts/gone.sh hooks/pre-commit.sh", "scripts/gone.sh", "hooks/pre-commit.sh"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, map[string]string{".no-mistakes.yaml": "commands:\n  format: \"" + test.command + "\"\n"})

			// Act
			report := f.check("go", "node", "cp")

			// Assert
			finding := only(t, report, "gate-command-missing")
			contains(t, "evidence", finding.Evidence, test.missing)
			if strings.Contains(strings.ReplaceAll(finding.Evidence, test.command, ""), test.written) {
				t.Errorf("evidence %q names %s, which the command writes", finding.Evidence, test.written)
			}
		})
	}
}

// A file git does not track is in a worktree only when the worktree is
// given it or its install step makes it. `.venv/Scripts/python.exe` was
// judged by the Overlord's own folder: fine where he had made a virtual
// environment, missing where he had not, and neither says what a goblin's
// worktree will hold.
func TestAFileGitDoesNotTrackIsJudgedByWhatAWorktreeWillHave(t *testing.T) {
	const venv = ".venv/Scripts/python.exe scripts/pytest_changed.py"
	const vitest = "node_modules/.bin/vitest.cmd run"
	for _, test := range []struct {
		name, command string
		tracked       map[string]string
		held          []string
		worktree      string
		missing       string
		note          string
	}{
		{name: "the folder holds a virtual environment no install step of a worktree makes", command: venv, held: []string{".venv/Scripts/python.exe"}, missing: "no install step of a worktree makes .venv"},
		{name: "the folder holds none and the manifest's install makes one", command: venv, worktree: `{"project":"northwind","dependencies":{"install":["uv venv","uv pip install -r requirements.txt"]}}`, tracked: map[string]string{"requirements.txt": "pytest\n"}, note: "made by the worktree's install step, uv venv"},
		{name: "the folder holds none and the lockfile's install makes one", command: venv, tracked: map[string]string{"uv.lock": "version = 1\n"}, note: "made by the worktree's install step, uv sync --locked"},
		{name: "an install command that names the folder makes it", command: venv, worktree: `{"project":"northwind","dependencies":{"install":["python -m venv .venv"]}}`, note: "made by the worktree's install step, python -m venv .venv"},
		{name: "packages the lockfile's install makes", command: vitest, tracked: map[string]string{"package-lock.json": "{}\n"}, note: "made by the worktree's install step, npm ci"},
		{name: "packages with no lockfile and no install", command: vitest, held: []string{"node_modules/.bin/vitest.cmd"}, missing: "no install step of a worktree makes node_modules"},
		{name: "packages the manifest shares from the folder", command: vitest, held: []string{"node_modules/.bin/vitest.cmd"}, worktree: `{"project":"northwind","dependencies":{"strategy":"link","paths":["node_modules"]}}`, note: "shared from the checkout by worktree.json"},
		{name: "packages the manifest shares and the folder does not hold", command: vitest, worktree: `{"project":"northwind","dependencies":{"strategy":"link","paths":["node_modules"]}}`, missing: "worktree.json shares node_modules"},
		{name: "a strategy of none installs nothing", command: vitest, tracked: map[string]string{"package-lock.json": "{}\n"}, worktree: `{"project":"northwind","dependencies":{"strategy":"none"}}`, missing: "no install step of a worktree makes node_modules"},
		{name: "a script only the folder holds", command: "python scripts/local_test.py", held: []string{"scripts/local_test.py"}, missing: "a worktree is not given it"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			tracked := map[string]string{"AGENTS.md": "Test with `" + test.command + "`.\n", "scripts/pytest_changed.py": "print('changed')\n", ".gitignore": ".venv/\nnode_modules/\nscripts/local_test.py\n"}
			for name, content := range test.tracked {
				tracked[name] = content
			}
			f := newFixture(t, tracked)
			for _, name := range test.held {
				f.write(name, "held by the folder alone\n")
			}
			if test.worktree != "" {
				f.manifest("worktree.json", test.worktree)
			}

			// Act
			report := f.check("uv", "python")

			// Assert
			if test.missing != "" {
				finding := only(t, report, "instruction-command-missing")
				contains(t, "evidence", finding.Evidence, test.missing)
				return
			}
			none(t, report, "instruction-command-missing")
			contains(t, "evidence", only(t, report, "instruction-commands-found").Evidence, test.note)
		})
	}
}

// The record's tiers are commands a worktree runs too, so a tier that
// starts a program the install makes is found by the same rule.
func TestARecordTierThatStartsAProgramTheInstallMakesIsFound(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"uv.lock": "version = 1\n", "scripts/gate_test.py": "print('gate')\n"})
	f.manifest("project.json", `{"project":"northwind","verification":{"fast":[[".venv/Scripts/python.exe","scripts/gate_test.py"]]}}`)

	// Act
	report := f.check()

	// Assert
	none(t, report, "tier-command-missing")
	contains(t, "evidence", only(t, report, "tier-commands-found").Evidence, "made by the worktree's install step, uv sync --locked")
}
