package projectcheck

import (
	"strings"
	"testing"
)

const productionEnv = "STRIPE_SECRET_KEY=sk_live_" + "a1B2a1B2a1B2a1B2a1B2a1B2\n" +
	"DATABASE_URL=postgres://app:hunter2hunter2@db.internal.example.com:5432/app\n" +
	"APP_ENV=production\n" +
	"SENTRY_DSN=https://0123456789abcdef@o1.ingest.example.io/1\n" +
	"REDIS_URL=redis://localhost:6379/0\n" +
	"FRONTEND_URL=https://app.example.com\n" +
	"PORT=8000\n"

// Both recorded harms of this class came from an env file a test run could
// read: a gate test step that spent production keys, and a local test run
// that paged production Sentry. A goblin's worktree shares the checkout's
// env files, so what they hold is what its tests start with.
func TestATestStepThatCanReachProductionIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		".gitignore":        ".env\n",
		".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n",
		"conftest.py":       "import os\n\nos.environ[\"SENTRY_DSN\"] = \"\"\n",
	})
	f.write(".env", productionEnv)

	// Act
	report := f.check("pytest")

	// Assert
	finding := only(t, report, "test-reaches-production")
	if finding.Severity != Critical || finding.Area != AreaGate {
		t.Errorf("test-reaches-production is %s in %s, want critical in gate", finding.Severity, finding.Area)
	}
	contains(t, "evidence", finding.Evidence, ".env", "STRIPE_SECRET_KEY", "DATABASE_URL", "APP_ENV", "conftest.py")
	for _, wrong := range []string{"SENTRY_DSN (", "REDIS_URL", "FRONTEND_URL", "PORT"} {
		if strings.Contains(finding.Evidence, wrong) {
			t.Errorf("evidence %q names %s, which the test setup pins or which is no production value", finding.Evidence, strings.TrimSuffix(wrong, " ("))
		}
	}
	for _, secret := range []string{"a1B2", "hunter2", "db.internal.example.com", "0123456789abcdef"} {
		if strings.Contains(report.Text(), secret) {
			t.Errorf("the report repeats part of a value, %q:\n%s", secret, report.Text())
		}
	}
}

// With no test command of the repository's own, an agent chooses what the
// test step runs, so nothing stands between it and the env file: a remote
// database alone is then as bad as it gets.
func TestAnAgentChosenTestStepBesideAProductionEnvFileIsCritical(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".gitignore": ".env.docker.local\n"})
	f.write(".env.docker.local", "DATABASE_URL=postgres://app:hunter2hunter2@db.internal.example.com:5432/app\n")

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "test-reaches-production")
	if finding.Severity != Critical {
		t.Errorf("test-reaches-production is %s, want critical", finding.Severity)
	}
	contains(t, "evidence", finding.Evidence, ".env.docker.local", "DATABASE_URL", "agent")
}

// A remote service with the repository's own test command in front of it is
// high, not critical: the command bounds what runs.
func TestARemoteServiceBehindTheRepositorysOwnTestCommandIsHigh(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".gitignore": ".env\n", ".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n"})
	f.write(".env", "DATABASE_URL=postgres://app:hunter2hunter2@db.internal.example.com:5432/app\n")

	// Act
	report := f.check("pytest")

	// Assert
	finding := only(t, report, "test-reaches-production")
	if finding.Severity != High {
		t.Errorf("test-reaches-production is %s, want high", finding.Severity)
	}
}

// The line that passes says what it examined, so an env file it stopped
// reading does not read as a safe one.
func TestATestStepWhoseEnvFileIsPinnedOrLocalPassesAndSaysWhatWasExamined(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		".gitignore":        ".env\n",
		".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n",
		"conftest.py":       "import os\n\nfor name in (\"STRIPE_SECRET_KEY\", \"DATABASE_URL\", \"SENTRY_DSN\"):\n    os.environ[name] = \"\"\nos.environ[\"APP_ENV\"] = \"test\"\n",
	})
	f.write(".env", productionEnv)

	// Act
	report := f.check("pytest")

	// Assert
	none(t, report, "test-reaches-production")
	finding := only(t, report, "test-env-examined")
	if finding.Severity != OK {
		t.Errorf("test-env-examined is %s, want ok", finding.Severity)
	}
	contains(t, "says", finding.Says, "7 variables", "4 production values")
	contains(t, "evidence", finding.Evidence, ".env", "conftest.py")
}

// A worktree manifest that shares no env file keeps the checkout's own out
// of every goblin's worktree, so no test run of a goblin's reads it.
func TestAnEnvFileNoWorktreeSharesIsNotATestRunsToRead(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".gitignore": ".env\n", ".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n"})
	f.write(".env", productionEnv)
	f.manifest("worktree.json", `{"project":"northwind","link":[]}`)

	// Act
	report := f.check("pytest")

	// Assert
	none(t, report, "test-reaches-production")
	finding := only(t, report, "test-env-examined")
	contains(t, "says", finding.Says, "0 variables")
}

// A test key and a placeholder spend nothing, and neither does a
// publishable key, which a browser is handed by design.
func TestTestKeysAndPlaceholdersAreNoProductionValues(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".gitignore": ".env\n", ".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n"})
	f.write(".env", "STRIPE_SECRET_KEY=sk_test_"+"a1B2a1B2a1B2a1B2a1B2a1B2\nRESEND_API_KEY=your-key-here\nOPENAI_API_KEY=\nAPP_ENV=development\n"+
		"VITE_STRIPE_PUBLISHABLE_KEY=pk_live_"+"a1B2a1B2a1B2a1B2a1B2a1B2\n")

	// Act
	report := f.check("pytest")

	// Assert
	none(t, report, "test-reaches-production")
}
