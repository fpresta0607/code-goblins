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
