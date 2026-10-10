package projectcheck

import (
	"strings"
	"testing"
)

const instructions = "# northwind\n\n" +
	"Build: `npm ci` and `npm run build` in `frontend`, then `go build ./cmd/app`.\n" +
	"Test with `go test ./internal/billing/...` and `python scripts/check.py`.\n" +
	"Lint with `ruff check .`.\n" +
	"Deploy with `flyctl deploy --remote-only`.\n" +
	"Run `cfo spawn <id> --project <p>` to dispatch, and read `docs/guide.md`.\n"

// An instruction file that names a command the repository no longer has
// sends every agent that reads it down a path that fails.
func TestAnInstructionCommandThatIsNotTrueIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		"AGENTS.md":             instructions,
		"frontend/package.json": `{"scripts":{"build":"vite build"}}`,
		"cmd/app/main.go":       "package main\n\nfunc main() {}\n",
	})

	// Act
	report := f.check("npm", "go", "python", "flyctl")

	// Assert
	finding := only(t, report, "instruction-command-missing")
	if finding.Severity != High || finding.Area != AreaInstructions {
		t.Errorf("instruction-command-missing is %s in %s, want high in instructions", finding.Severity, finding.Area)
	}
	contains(t, "evidence", finding.Evidence, "AGENTS.md:4", "internal/billing", "scripts/check.py", "AGENTS.md:5", "ruff is not on PATH")
	for _, fine := range []string{"npm run build", "go build", "flyctl", "cfo spawn"} {
		if strings.Contains(finding.Evidence, fine) {
			t.Errorf("evidence %q names %q, which is there, a deploy or no build, test or lint command", finding.Evidence, fine)
		}
	}
}

// The commands that are there are listed with their kind, which is what a
// reader runs or dry-runs to prove them, and a deploy is marked as never run.
func TestInstructionCommandsThatAreThereAreListedForProofAndADeployIsNeverRun(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		"CLAUDE.md":             "Build: `npm ci` and `npm run build` in `frontend`, then `go build ./cmd/app`.\nDeploy with `flyctl deploy --remote-only`.\n",
		"frontend/package.json": `{"scripts":{"build":"vite build"}}`,
		"cmd/app/main.go":       "package main\n\nfunc main() {}\n",
	})

	// Act
	report := f.check("npm", "go", "flyctl")

	// Assert
	none(t, report, "instruction-command-missing")
	finding := only(t, report, "instruction-commands-found")
	contains(t, "evidence", finding.Evidence, "build `npm run build` (CLAUDE.md:1, script build of frontend/package.json)", "build `go build ./cmd/app`")
	deploy := only(t, report, "instruction-deploy-never-run")
	if deploy.Severity != OK {
		t.Errorf("instruction-deploy-never-run is %s, want ok", deploy.Severity)
	}
	contains(t, "evidence", deploy.Evidence, "flyctl deploy --remote-only", "CLAUDE.md:2")
	if !report.Passed(AreaInstructions) {
		t.Errorf("the instructions area did not pass:\n%s", report.Text())
	}
}

// An instruction file is prose: a code span can hold a phrase, a cmdlet or
// a name, and a plain code block a directory tree. None of those is a
// command of the project, and a block marked as shell is.
func TestProseAndListingsInAnInstructionFileAreNotCommands(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		"AGENTS.md": "The card shows `installing its dependencies first`, and the drive is made with `Format-Volume -DevDrive`.\n" +
			"Run `cfo install --projects-root` once.\n\n" +
			"```\napp/        core/ (config), tests/\nscripts/    migrations/ (ordered), pytest_changed.py (diff-scoped gate)\n```\n\n" +
			"```bash\n$ go test ./internal/gone/...\n```\n",
	})

	// Act
	report := f.check("go")

	// Assert
	finding := only(t, report, "instruction-command-missing")
	contains(t, "evidence", finding.Evidence, "AGENTS.md:10", "internal/gone")
	for _, prose := range []string{"installing", "Format-Volume", "cfo install", "migrations", "core/"} {
		if strings.Contains(report.Only([]string{AreaInstructions}).Text(), prose) {
			t.Errorf("the instructions area reads %q as a command:\n%s", prose, report.Only([]string{AreaInstructions}).Text())
		}
	}
}

// A repository with neither file gives an agent nothing to follow.
func TestARepositoryWithNoInstructionFileIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "instructions-missing")
	if finding.Severity != Low {
		t.Errorf("instructions-missing is %s, want low", finding.Severity)
	}
}

// An instruction file shows how to run one test file with a name it made
// up, and that path was a false high. A path counts as an example only when
// its file name is made of a placeholder word and no commit the default
// branch can reach ever held it, and it is still listed, on a line of its
// own.
func TestAMadeUpExamplePathInAnInstructionFileIsNotAMissingFile(t *testing.T) {
	for _, example := range []string{
		"npx tsx --test tests/budgeting/someFile.test.ts",
		"python tests/test_example.py",
		"npx vitest run src/components/MyComponent.test.ts",
		"go test ./internal/foo/...",
	} {
		t.Run(example, func(t *testing.T) {
			// Arrange
			f := newFixture(t, map[string]string{
				"CLAUDE.md":                        "To run a single test file: `" + example + "`\n",
				"tests/budgeting/rollover.test.ts": "export {}\n",
			})

			// Act
			report := f.check("npx", "python", "go")

			// Assert
			none(t, report, "instruction-command-missing")
			finding := only(t, report, "instruction-example-paths")
			if finding.Severity != OK || finding.Area != AreaInstructions {
				t.Errorf("instruction-example-paths is %s in %s, want ok in instructions", finding.Severity, finding.Area)
			}
			contains(t, "evidence", finding.Evidence, example, "CLAUDE.md:1")
		})
	}
}

// The example rule cannot hide a file that is really missing: one the
// repository once held and lost, and one whose name is no placeholder, are
// both still a high finding.
func TestAnExampleNameDoesNotHideAFileThatIsReallyMissing(t *testing.T) {
	for _, test := range []struct {
		name, command, missing string
		onceHeld               bool
	}{
		{"a deleted file with a placeholder name", "npx tsx --test tests/someFile.test.ts", "tests/someFile.test.ts", true},
		{"a file that never was, with a real name", "npx tsx --test tests/rollover.test.ts", "tests/rollover.test.ts", false},
		{"a placeholder folder above a real name", "python example/billing_test.py", "example/billing_test.py", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			tracked := map[string]string{"CLAUDE.md": "Run `" + test.command + "`.\n"}
			if test.onceHeld {
				tracked[test.missing] = "export {}\n"
			}
			f := newFixture(t, tracked)
			if test.onceHeld {
				f.git("rm", "--quiet", test.missing)
				f.commit("the file is deleted and the instructions still name it")
			}

			// Act
			report := f.check("npx", "python")

			// Assert
			finding := only(t, report, "instruction-command-missing")
			if finding.Severity != High {
				t.Errorf("instruction-command-missing is %s, want high", finding.Severity)
			}
			contains(t, "evidence", finding.Evidence, test.missing)
			none(t, report, "instruction-example-paths")
		})
	}
}

// An install is what a worktree's own install step does, and running one to
// prove it changes the machine, so an install is listed apart from the
// commands to prove by running. One that names a file the repository does
// not have is still a command that cannot run as written.
func TestAnInstallCommandIsListedApartFromTheCommandsToProve(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		"CLAUDE.md": "CI runs `npm ci`, `npx playwright install --with-deps --only-shell chromium` and `npm run test:ci`.\n" +
			"Set up with `uv pip install -r requirements.txt` or `python -m pip install -r requirements-dev.txt`.\n",
		"package.json":     `{"scripts":{"test:ci":"node --test"}}`,
		"requirements.txt": "flask\n",
	})

	// Act
	report := f.check("npm", "npx", "uv", "python")

	// Assert
	found := only(t, report, "instruction-commands-found")
	contains(t, "says", found.Says, "1 build, test and lint command")
	contains(t, "evidence", found.Evidence, "test `npm run test:ci`")
	installs := only(t, report, "instruction-install-not-run")
	if installs.Severity != OK {
		t.Errorf("instruction-install-not-run is %s, want ok", installs.Severity)
	}
	contains(t, "evidence", installs.Evidence, "`npm ci` (CLAUDE.md:1)", "`npx playwright install --with-deps --only-shell chromium` (CLAUDE.md:1)", "`uv pip install -r requirements.txt` (CLAUDE.md:2)")
	missing := only(t, report, "instruction-command-missing")
	contains(t, "evidence", missing.Evidence, "requirements-dev.txt")
}
