package projectcheck

import (
	"strings"
	"testing"
)

// production is one production value, an address that carries a login.
const production = "DATABASE_URL=postgres://app:hunter2hunter2@db.internal.example.com:5432/app\n"

// loadedBy is a repository whose worktrees are given an env file that holds
// a production value, with the tracked files that do or do not load it.
func loadedBy(t *testing.T, envFile string, tracked map[string]string) fixture {
	t.Helper()
	files := map[string]string{".gitignore": ".env*\n", ".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n"}
	for name, content := range tracked {
		files[name] = content
	}
	f := newFixture(t, files)
	f.write(envFile, production)
	f.manifest("worktree.json", `{"project":"northwind","link":["`+envFile+`"]}`)
	return f
}

// The line said "a test run can read production" whenever an env file was in
// the worktree. In four of six projects no test loads the file: what does is
// the development server, the application's own settings loader, or scripts
// that load it and write to production. The line now says what loads the
// file where a tracked file shows it, and claims a test only where a test,
// its setup or the application's own code loads it.
func TestTheLineSaysWhatLoadsTheEnvFile(t *testing.T) {
	for _, test := range []struct {
		name    string
		tracked map[string]string
		check   string
		named   []string
	}{
		{
			name:    "the test setup",
			tracked: map[string]string{"conftest.py": loadsEnv},
			check:   "test-reaches-production",
			named:   []string{"a test or its setup loads", "conftest.py"},
		},
		{
			name:    "a test file",
			tracked: map[string]string{"tests/db.test.ts": "import 'dotenv/config'\n"},
			check:   "test-reaches-production",
			named:   []string{"a test or its setup loads", "tests/db.test.ts"},
		},
		{
			name:    "the application's own settings loader",
			tracked: map[string]string{"app/config.py": "from pydantic_settings import BaseSettings, SettingsConfigDict\n\n\nclass Settings(BaseSettings):\n    model_config = SettingsConfigDict(env_file=\".env\")\n"},
			check:   "test-reaches-production",
			named:   []string{"the application's own code loads", "a test that starts the application", "app/config.py"},
		},
		{
			name:    "scripts alone",
			tracked: map[string]string{"scripts/backfill.ts": "import 'dotenv/config'\n", "scripts/cancel.ts": "import dotenv from 'dotenv'\n\ndotenv.config()\n"},
			check:   "worktree-holds-production",
			named:   []string{"no test loads", "2 scripts", "scripts/backfill.ts", "scripts/cancel.ts"},
		},
		{
			name:    "a package script",
			tracked: map[string]string{"package.json": `{"scripts":{"seed":"node --env-file=.env scripts/seed.mjs"}}`, "scripts/seed.mjs": "console.log('seed')\n"},
			check:   "worktree-holds-production",
			named:   []string{"no test loads", "package.json script seed"},
		},
		{
			name:    "the development server of a framework",
			tracked: map[string]string{"package.json": `{"dependencies":{"next":"15.0.0","react":"19.0.0"}}`},
			check:   "worktree-holds-production",
			named:   []string{"no test loads", "the development server and the build of next"},
		},
		{
			name:    "the local stack",
			tracked: map[string]string{"docker-compose.yml": "services:\n  api:\n    image: northwind\n    env_file: .env\n"},
			check:   "worktree-holds-production",
			named:   []string{"no test loads", "docker-compose.yml"},
		},
		{
			name:  "nothing the check knows",
			check: "worktree-holds-production",
			named: []string{"nothing this check knows loads"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := loadedBy(t, ".env", test.tracked)

			// Act
			report := f.check("pytest")

			// Assert
			finding := only(t, report, test.check)
			if finding.Area != AreaGate {
				t.Errorf("%s is in %s, want gate", test.check, finding.Area)
			}
			contains(t, "line", finding.Text(), append(test.named, "DATABASE_URL")...)
			for _, other := range []string{"test-reaches-production", "worktree-holds-production"} {
				if other != test.check {
					none(t, report, other)
				}
			}
			if strings.Contains(report.Text(), "hunter2") {
				t.Errorf("the report repeats a value:\n%s", report.Text())
			}
		})
	}
}

// A loader loads the file it names. One that names another env file does
// not load this one, and one that names none loads .env, which is what the
// libraries do.
func TestALoaderLoadsTheEnvFileItNames(t *testing.T) {
	for _, test := range []struct {
		name, envFile string
		tracked       map[string]string
		check         string
	}{
		{"a loader that names this file", ".env.local", map[string]string{"src/env.ts": "import dotenv from 'dotenv'\n\ndotenv.config({ path: '.env.local' })\n"}, "test-reaches-production"},
		{"a loader that names another file", ".env.local", map[string]string{"conftest.py": "from dotenv import load_dotenv\n\nload_dotenv(\".env.test\")\n"}, "worktree-holds-production"},
		{"a loader that names none, beside a file it does not load by default", ".env.local", map[string]string{"conftest.py": loadsEnv}, "worktree-holds-production"},
		{"a loader that names none, beside the file it loads by default", ".env", map[string]string{"conftest.py": loadsEnv}, "test-reaches-production"},
		{"a framework beside a file it loads by its own rule", ".env.local", map[string]string{"package.json": `{"devDependencies":{"vite":"6.0.0"}}`}, "worktree-holds-production"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := loadedBy(t, test.envFile, test.tracked)

			// Act
			report := f.check("pytest")

			// Assert
			contains(t, "evidence", only(t, report, test.check).Evidence, test.envFile)
		})
	}
}

// A document that mentions a loader, a lockfile and a list of dependencies
// load nothing.
func TestAMentionOfALoaderLoadsNothing(t *testing.T) {
	// Arrange
	f := loadedBy(t, ".env", map[string]string{
		"README.md":         "Copy `.env.example` to `.env`. The app loads it with dotenv.\n",
		"package-lock.json": `{"packages":{"node_modules/dotenv":{"version":"16.4.5"}}}`,
		"requirements.txt":  "python-dotenv==1.0.1\n",
		"docs/setup.md":     "Run with `node --env-file=.env server.js`.\n",
	})

	// Act
	report := f.check("pytest")

	// Assert
	contains(t, "evidence", only(t, report, "worktree-holds-production").Evidence, "nothing this check knows loads")
}

// A file no test loads is not behind the test step, so how bad the line is
// follows what the file holds and not who chooses the test step. A setup
// that names a variable clears nothing for a script either, so every
// production value is counted.
func TestAnEnvFileNoTestLoadsIsJudgedByWhatItHolds(t *testing.T) {
	for _, test := range []struct {
		name, env string
		gate      string
		severity  Severity
		count     string
	}{
		{"a remote store, with the test step an agent's choice", production, "", High, "1 production value"},
		{"a live key", production + "STRIPE_SECRET_KEY=sk_live_" + "a1B2a1B2a1B2a1B2a1B2a1B2\n", "pytest -q", Critical, "2 production values"},
		{"a variable the test setup names", production, "pytest -q", High, "1 production value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			files := map[string]string{".gitignore": ".env\n", "scripts/backfill.ts": "import 'dotenv/config'\n", "pytest.ini": "[pytest]\nenv =\n    DATABASE_URL=sqlite://\n"}
			if test.gate != "" {
				files[".no-mistakes.yaml"] = "commands:\n  test: \"" + test.gate + "\"\n"
			}
			f := newFixture(t, files)
			f.write(".env", test.env)
			f.manifest("worktree.json", `{"project":"northwind","link":[".env"]}`)

			// Act
			report := f.check("pytest")

			// Assert
			finding := only(t, report, "worktree-holds-production")
			if finding.Severity != test.severity {
				t.Errorf("worktree-holds-production is %s, want %s", finding.Severity, test.severity)
			}
			contains(t, "says", finding.Says, test.count)
			contains(t, "evidence", finding.Evidence, "DATABASE_URL")
		})
	}
}
