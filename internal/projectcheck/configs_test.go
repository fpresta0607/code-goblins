package projectcheck

import (
	"strings"
	"testing"
)

// An env file git does not ignore is one `git add --all` away from the
// repository's history, wherever in the checkout it sits.
func TestAnEnvFileGitDoesNotIgnoreIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".gitignore": "/.env\n", ".env.example": "PORT=\n"})
	f.write(".env", "PORT=8000\n")
	f.write(".env.local", "PORT=8001\n")
	f.write("web/.env", "PORT=8002\n")

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "env-file-not-ignored")
	if finding.Severity != High || finding.Area != AreaConfigs {
		t.Errorf("env-file-not-ignored is %s in %s, want high in configs", finding.Severity, finding.Area)
	}
	contains(t, "evidence", finding.Evidence, ".env.local", "web/.env")
	for _, exempt := range []string{".env.example", " .env "} {
		if strings.Contains(" "+finding.Evidence+" ", exempt) {
			t.Errorf("evidence %q names %q, which is ignored or an example", finding.Evidence, strings.TrimSpace(exempt))
		}
	}
	if strings.Contains(finding.Fix, "rotate") {
		t.Errorf("fix %q says rotate, and no file here is tracked or holds a credential", finding.Fix)
	}
	ignored := only(t, report, "env-files-ignored")
	contains(t, "evidence", ignored.Evidence, ".env")
}

// A committed env file that holds a credential is the same fault after the
// fact, and the worse one: git cannot ignore a file it tracks, and the
// repository's history holds what the file held.
func TestATrackedEnvFileHoldingACredentialIsCriticalAndSaysRotate(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".env": "PORT=8000\nDATABASE_URL=postgres://app:hunter2hunter2@db.internal.example.com:5432/app\n"})

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "env-file-not-ignored")
	if finding.Severity != Critical {
		t.Errorf("env-file-not-ignored is %s, want critical", finding.Severity)
	}
	contains(t, "evidence", finding.Evidence, ".env", "tracked", "DATABASE_URL")
	contains(t, "fix", finding.Fix, "git rm --cached", "rotate")
	none(t, report, "env-file-committed")
	if strings.Contains(report.Text(), "hunter2") || strings.Contains(report.Text(), "db.internal.example.com") {
		t.Errorf("the report repeats part of a value:\n%s", report.Text())
	}
}

// A tracked env file is read at the default branch, which is what the
// repository publishes, and in the folder, which is what the next commit
// would publish. A folder that lags must not answer alone for either.
func TestATrackedEnvFileIsReadAtTheDefaultBranchAndInTheFolder(t *testing.T) {
	for _, test := range []struct{ name, first, second string }{
		{"the default branch gained a credential the folder's copy lacks", "PORT=8000\n", "PORT=8000\nDATABASE_URL=postgres://app:hunter2hunter2@db.internal.example.com:5432/app\n"},
		{"the folder's copy holds a credential the default branch dropped", "PORT=8000\nDATABASE_URL=postgres://app:hunter2hunter2@db.internal.example.com:5432/app\n", "PORT=8000\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, map[string]string{".env.demo": test.first})
			f.write(".env.demo", test.second)
			f.commit("the demo file changes")
			f.lag()

			// Act
			report := f.check()

			// Assert
			finding := only(t, report, "env-file-not-ignored")
			if finding.Severity != Critical {
				t.Errorf("env-file-not-ignored is %s, want critical", finding.Severity)
			}
			contains(t, "evidence", finding.Evidence, ".env.demo", "DATABASE_URL")
		})
	}
}

// A demo env file committed on purpose holds a loopback address, a port and
// placeholders. It was a high finding whose fix told the reader to rotate
// credentials the file never held. What a tracked env file holds decides, not
// its name: with no credential in it, it is a low line that says so.
func TestACommittedEnvFileThatHoldsNoCredentialIsLowAndNeverSaysRotate(t *testing.T) {
	for _, name := range []string{".env.demo", ".env", "web/.env.development"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, map[string]string{
				name: "# Committed on purpose: the local demo stack.\nNEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321\nPORT=3000\nPLAID_ENV=sandbox\nPLAID_SECRET=demo-plaid-secret\n",
			})

			// Act
			report := f.check()

			// Assert
			none(t, report, "env-file-not-ignored")
			finding := only(t, report, "env-file-committed")
			if finding.Severity != Low || finding.Area != AreaConfigs {
				t.Errorf("env-file-committed is %s in %s, want low in configs", finding.Severity, finding.Area)
			}
			contains(t, "evidence", finding.Evidence, name, "4 variables")
			if strings.Contains(strings.ToLower(report.Text()), "rotate") {
				t.Errorf("the report tells the reader to rotate credentials no file held:\n%s", report.Text())
			}
			if !report.Passed(AreaConfigs) {
				t.Errorf("the configs area did not pass:\n%s", report.Text())
			}
		})
	}
}

// A credential in a file git does not ignore is the worst of it, and the
// report names the variable without ever repeating its value.
func TestAnUnignoredEnvFileHoldingACredentialIsCriticalAndNeverPrintsIt(t *testing.T) {
	// Arrange
	secret := "sk_live_" + strings.Repeat("a1B2", 8)
	f := newFixture(t, nil)
	f.write(".env", "STRIPE_SECRET_KEY="+secret+"\nPORT=8000\n")

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "env-file-not-ignored")
	if finding.Severity != Critical {
		t.Errorf("env-file-not-ignored is %s, want critical", finding.Severity)
	}
	contains(t, "evidence", finding.Evidence, "STRIPE_SECRET_KEY")
	if strings.Contains(report.Text(), secret) || strings.Contains(report.Text(), "a1B2") {
		t.Errorf("the report repeats a credential's value:\n%s", report.Text())
	}
}

func TestAProjectWhoseEnvFilesAreAllIgnoredPasses(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".gitignore": ".env*\n!.env.example\n", ".env.example": "PORT=\n"})
	f.write(".env", "PORT=8000\n")
	f.write("web/.env.local", "PORT=8002\n")

	// Act
	report := f.check()

	// Assert
	none(t, report, "env-file-not-ignored")
	finding := only(t, report, "env-files-ignored")
	contains(t, "evidence", finding.Evidence, ".env", "web/.env.local")
}

// A worktree manifest the loader refuses stops the project's next spawn.
func TestAnInvalidWorktreeManifestIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.manifest("worktree.json", `{"project":"northwind","dependencies":{"strategy":"copy"}}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "worktree-invalid")
	if finding.Severity != High {
		t.Errorf("worktree-invalid is %s, want high", finding.Severity)
	}
	contains(t, "evidence", finding.Evidence, "copy")
}

// A worktree manifest agrees with the repository when what it links is in
// the checkout, and what it installs with is a program this machine has
// reading files the repository holds.
func TestAWorktreeManifestThatDisagreesWithTheRepositoryIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"requirements.txt": "flask\n"})
	f.write(".env", "PORT=8000\n")
	f.manifest("worktree.json", `{"project":"northwind","link":[".env","secrets.toml"],"dependencies":{"install":["uv pip install -r requirements.txt -r requirements-dev.txt","poetry install"]}}`)

	// Act
	report := f.check("uv")

	// Assert
	link := only(t, report, "worktree-link-missing")
	contains(t, "evidence", link.Evidence, "secrets.toml")
	if strings.Contains(link.Evidence, ".env") {
		t.Errorf("evidence %q names .env, which the checkout holds", link.Evidence)
	}
	install := only(t, report, "worktree-install-missing")
	if install.Severity != High {
		t.Errorf("worktree-install-missing is %s, want high", install.Severity)
	}
	contains(t, "evidence", install.Evidence, "poetry", "requirements-dev.txt")
	if strings.Contains(install.Evidence, "requirements.txt is") {
		t.Errorf("evidence %q names requirements.txt, which the repository holds", install.Evidence)
	}
}

func TestAWorktreeManifestThatAgreesWithTheRepositoryPasses(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"requirements.txt": "flask\n"})
	f.write(".env", "PORT=8000\n")
	f.manifest("worktree.json", `{"project":"northwind","link":[".env"],"dependencies":{"install":["uv pip install -r requirements.txt"]}}`)

	// Act
	report := f.check("uv")

	// Assert
	none(t, report, "worktree-link-missing")
	none(t, report, "worktree-install-missing")
	only(t, report, "worktree-agrees")
}

// A services manifest agrees with the repository when its compose file is
// there, holds the services it names, and its check is a program that exists.
func TestAServicesManifestThatDisagreesWithTheRepositoryIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"docker-compose.dev.yml": "services:\n  backend:\n    image: app\n  worker:\n    image: app\n"})
	f.manifest("services.json", `{"project":"northwind","compose":"docker-compose.dev.yml","env_file":".env.docker.local","services":["backend","cache"],"check":["nosuchshell","-File","guard.ps1"],"memory_estimate_gb":2}`)

	// Act
	report := f.check()

	// Assert
	services := only(t, report, "services-not-in-compose")
	if services.Severity != High {
		t.Errorf("services-not-in-compose is %s, want high", services.Severity)
	}
	contains(t, "evidence", services.Evidence, "cache", "docker-compose.dev.yml")
	env := only(t, report, "services-env-file-missing")
	contains(t, "evidence", env.Evidence, ".env.docker.local")
	check := only(t, report, "services-check-missing")
	contains(t, "evidence", check.Evidence, "nosuchshell")
}

func TestAServicesManifestNamingAMissingComposeFileIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.manifest("services.json", `{"project":"northwind","compose":"compose.yml","services":["backend"],"memory_estimate_gb":2}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "services-compose-missing")
	if finding.Severity != High {
		t.Errorf("services-compose-missing is %s, want high", finding.Severity)
	}
	contains(t, "evidence", finding.Evidence, "compose.yml")
}

// An env file a local stack starts from can name production, and a services
// manifest with no check starts the stack without anything having looked.
func TestAServicesManifestWithAnEnvFileAndNoCheckIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"compose.yml": "services:\n  backend:\n    image: app\n"})
	f.write(".env.docker.local", "PORT=8000\n")
	f.manifest("services.json", `{"project":"northwind","compose":"compose.yml","env_file":".env.docker.local","services":["backend"],"memory_estimate_gb":2}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "services-no-check")
	if finding.Severity != Medium {
		t.Errorf("services-no-check is %s, want medium", finding.Severity)
	}
	none(t, report, "services-not-in-compose")
	none(t, report, "services-env-file-missing")
}

func TestAServicesManifestThatAgreesWithTheRepositoryPasses(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"compose.yml": "services:\n  backend:\n    image: app\n", "guard.ps1": "exit 0\n"})
	f.write(".env.docker.local", "PORT=8000\n")
	f.manifest("services.json", `{"project":"northwind","compose":"compose.yml","env_file":".env.docker.local","services":["backend"],"check":["pwsh","-File","guard.ps1"],"memory_estimate_gb":2}`)

	// Act
	report := f.check("pwsh")

	// Assert
	for _, check := range []string{"services-not-in-compose", "services-env-file-missing", "services-check-missing", "services-no-check", "services-compose-missing"} {
		none(t, report, check)
	}
	only(t, report, "services-agree")
}

// A folder with a .git of its own is another repository, such as a clone or
// a worktree kept inside the checkout, and its env files are not this
// project's to answer for.
func TestEnvFilesOfARepositoryNestedInTheCheckoutAreNotExamined(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.write("clones/other/.git/HEAD", "ref: refs/heads/main\n")
	f.write("clones/other/.env", "PORT=8000\n")

	// Act
	report := f.check()

	// Assert
	none(t, report, "env-file-not-ignored")
	finding := only(t, report, "env-files-ignored")
	if strings.Contains(finding.Evidence, "clones/other/.env") {
		t.Errorf("evidence %q names an env file of a nested repository", finding.Evidence)
	}
}

// A manifest that names no link shares the default env files, and the line
// that passes it says which of them every goblin's worktree receives.
func TestAWorktreeManifestWithNoLinkSaysWhichDefaultEnvFilesItShares(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".gitignore": ".env*\n"})
	f.write(".env", "PORT=8000\n")
	f.write(".env.docker.local", "PORT=8000\n")
	f.manifest("worktree.json", `{"project":"northwind","dependencies":{"strategy":"none"}}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "worktree-agrees")
	contains(t, "evidence", finding.Evidence, "default", ".env, .env.docker.local")
}

// A manifest whose link is empty shares nothing, and the line says so
// rather than ending on a list with nothing in it.
func TestAWorktreeManifestThatSharesNothingSaysSo(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.manifest("worktree.json", `{"project":"northwind","link":[]}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "worktree-agrees")
	contains(t, "evidence", finding.Evidence, "it shares nothing into a worktree")
}
