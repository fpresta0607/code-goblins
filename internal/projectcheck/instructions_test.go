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
