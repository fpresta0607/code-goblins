package projectcheck

import (
	"strings"
	"testing"
)

// envFixture is a repository whose worktrees are given an env file that a
// test setup loads, so each production value in it is on the line.
func envFixture(t *testing.T, env string) fixture {
	t.Helper()
	f := newFixture(t, map[string]string{".gitignore": ".env\n", ".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n", "conftest.py": loadsEnv})
	f.write(".env", env)
	f.manifest("worktree.json", `{"project":"northwind","link":[".env"]}`)
	return f
}

// A names-only read of five env files found six credentials the check had
// not counted. Its rule wanted a value that looks random under a name that
// ends in one of six words, so a short password and a name ending in PASS
// were passed over. A value under a credential's name, in a file git does
// not publish, is a credential unless it reads as a placeholder.
func TestAPlainValueUnderACredentialsNameIsACredential(t *testing.T) {
	for _, test := range []struct{ name, variable, value string }{
		{"a short password", "ADMIN_BASIC_PASSWORD", "hunter2"},
		{"a secret made of plain words", "CRON_SECRET", "nightly-sweep-two"},
		{"a name that ends in PASS", "SMTP_PASS", "hunter2hunter2"},
		{"a name that ends in PWD", "DB_PWD", "hunter2hunter2"},
		{"a passphrase", "SIGNING_PASSPHRASE", "correct horse battery"},
		{"access codes", "PILOT_ACCESS_CODES", "alpha7,bravo9"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := envFixture(t, test.variable+"="+test.value+"\nPORT=8000\n")

			// Act
			report := f.check("pytest")

			// Assert
			finding := only(t, report, "test-reaches-production")
			contains(t, "evidence", finding.Evidence, test.variable+" (a credential by its name)")
			if strings.Contains(report.Text(), test.value) {
				t.Errorf("the report repeats a value:\n%s", report.Text())
			}
		})
	}
}

// A placeholder spends nothing, so it is no production value. It is named
// all the same, as a value the rules passed over, so a reader can see what
// the count left out.
func TestAPlaceholderUnderACredentialsNameIsPassedOverAndNamed(t *testing.T) {
	// Arrange
	f := envFixture(t, "RESEND_API_KEY=your-key-here\nADMIN_PASSWORD=changeme\nDB_PASSWORD=postgres\nCRON_SECRET=<set me>\nOPENAI_API_KEY=\n")

	// Act
	report := f.check("pytest")

	// Assert
	none(t, report, "test-reaches-production")
	examined := only(t, report, "test-env-examined")
	contains(t, "evidence", examined.Evidence, "a placeholder under a credential's name: ADMIN_PASSWORD, CRON_SECRET, DB_PASSWORD, RESEND_API_KEY")
	if strings.Contains(examined.Evidence, "OPENAI_API_KEY") {
		t.Errorf("evidence %q names OPENAI_API_KEY, which holds no value", examined.Evidence)
	}
}

// An address counted only when its name held a word for a data store. One
// that carries a login is a credential wherever it is filed, and one that
// only points at another host is named as passed over, since the rule
// cannot say what answers there.
func TestAnAddressIsJudgedByWhatItCarriesAndWhereItPoints(t *testing.T) {
	for _, test := range []struct {
		name, line string
		reason     string
		passedOver string
	}{
		{"a login inside an address whose name says no data store", "APPFOLIO_FEED_URL=https://feed:hunter2hunter2@feeds.example.com/listings", "an address that carries a login", ""},
		{"a token in the query of an address", "REPORT_URL=https://reports.example.com/run?token=a1B2a1B2a1B2a1B2", "an address that carries a login", ""},
		{"an address on another host with no login", "APPFOLIO_FEED_URL=https://feeds.example.com/listings", "", "an address on another host whose name says no data store: APPFOLIO_FEED_URL"},
		{"a login inside an address on this machine", "QUEUE_URL=amqp://guest:hunter2hunter2@localhost:5672/", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			variable, _, _ := strings.Cut(test.line, "=")
			f := envFixture(t, test.line+"\n")

			// Act
			report := f.check("pytest")

			// Assert
			examined := only(t, report, "test-env-examined")
			for _, part := range []string{"hunter2", "a1B2", "feeds.example.com", "reports.example.com"} {
				if strings.Contains(report.Text(), part) {
					t.Errorf("the report repeats part of a value, %q:\n%s", part, report.Text())
				}
			}
			if test.reason != "" {
				contains(t, "evidence", only(t, report, "test-reaches-production").Evidence, variable+" ("+test.reason+")")
				return
			}
			none(t, report, "test-reaches-production")
			if test.passedOver != "" {
				contains(t, "evidence", examined.Evidence, test.passedOver)
				return
			}
			if strings.Contains(examined.Evidence, variable) {
				t.Errorf("evidence %q names %s, which points at this machine", examined.Evidence, variable)
			}
		})
	}
}

// The address of a production project was skipped as public by its name, so
// it was never shown beside the service key that opens it. It is no
// credential alone, and it is named beside a key of the same service.
func TestAPublicAddressIsNamedBesideTheKeyThatOpensIt(t *testing.T) {
	const address = "NEXT_PUBLIC_SUPABASE_URL=https://abcdefghijklmnopqrst.supabase.example.co\n"
	for _, test := range []struct {
		name, key string
		beside    bool
	}{
		{"beside the service key", "SUPABASE_SERVICE_ROLE_KEY=" + jwt("service_role"), true},
		{"beside the anon key alone", "NEXT_PUBLIC_SUPABASE_ANON_KEY=" + jwt("anon"), false},
		{"beside the key of another service", "OPENAI_API_KEY=sk-proj-" + strings.Repeat("a1B2", 8), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := envFixture(t, address+test.key+"\n")

			// Act
			report := f.check("pytest")

			// Assert
			if strings.Contains(report.Text(), "abcdefghijklmnopqrst") {
				t.Errorf("the report repeats a host:\n%s", report.Text())
			}
			if test.beside {
				contains(t, "evidence", only(t, report, "test-reaches-production").Evidence, "SUPABASE_SERVICE_ROLE_KEY (a credential, which opens the host NEXT_PUBLIC_SUPABASE_URL names)")
				return
			}
			if strings.Contains(report.Text(), "NEXT_PUBLIC_SUPABASE_URL names") {
				t.Errorf("a public address is named beside a key that does not open it:\n%s", report.Text())
			}
		})
	}
}

// The check knew eight variables that say which environment a program runs
// as, and PLAID_ENV was not among them. A switch is known by the word its
// name ends in, and it says production under a public name too.
func TestAnEnvironmentSwitchIsKnownByTheWordItsNameEndsIn(t *testing.T) {
	for _, test := range []struct {
		line       string
		production bool
	}{
		{"PLAID_ENV=production", true},
		{"STRIPE_MODE=live", true},
		{"NEXT_PUBLIC_APP_ENV=production", true},
		{"DEPLOY_STAGE=prod", true},
		{"PLAID_ENV=sandbox", false},
		{"NODE_ENV=development", false},
		{"LOG_LEVEL=production", false},
		{"PRODUCTION_NOTES=live", false},
	} {
		t.Run(test.line, func(t *testing.T) {
			// Arrange
			variable, _, _ := strings.Cut(test.line, "=")
			f := envFixture(t, test.line+"\n")

			// Act
			report := f.check("pytest")

			// Assert
			if !test.production {
				none(t, report, "test-reaches-production")
				return
			}
			finding := only(t, report, "test-reaches-production")
			contains(t, "evidence", finding.Evidence, variable+" (names production)")
			if finding.Severity != Critical {
				t.Errorf("test-reaches-production is %s for an environment set to production, want critical", finding.Severity)
			}
		})
	}
}

// No rule that reads a name and the shape of a value can be closed, so the
// line says its count is a floor, and the line that says what was examined
// names each variable whose value was passed over, under why.
func TestTheCountOfProductionValuesSaysItIsAFloorAndNamesWhatWasPassedOver(t *testing.T) {
	// Arrange
	f := envFixture(t, "DATABASE_URL=postgres://app:hunter2hunter2@db.internal.example.com:5432/app\n"+
		"SESSION_SALT=q7Lm2Xc9Rt4Vb8Kn3Zp6Wd1Hs5Jf0Gy\n"+
		"DOCS_URL=https://docs.example.com\n"+
		"ADMIN_PASSWORD=changeme\n"+
		"PORT=8000\nFEATURE_FLAGS=on\n")

	// Act
	report := f.check("pytest")

	// Assert
	finding := only(t, report, "test-reaches-production")
	contains(t, "evidence", finding.Evidence, "The count is a floor", "3 more variables hold a value the rules pass over")
	examined := only(t, report, "test-env-examined")
	contains(t, "evidence", examined.Evidence,
		"long random text under a name the rules take for no credential: SESSION_SALT",
		"an address on another host whose name says no data store: DOCS_URL",
		"a placeholder under a credential's name: ADMIN_PASSWORD")
	for _, plain := range []string{"PORT", "FEATURE_FLAGS"} {
		if strings.Contains(examined.Evidence, plain) {
			t.Errorf("evidence %q names %s, which holds a plain setting", examined.Evidence, plain)
		}
	}
	if strings.Contains(report.Text(), "q7Lm2Xc9") || strings.Contains(report.Text(), "docs.example.com") {
		t.Errorf("the report repeats part of a value:\n%s", report.Text())
	}
}

// What a tracked env file holds decides its line. A value git already
// publishes is no secret by standing under a credential's name, and an
// environment set to production is nothing to rotate, so neither makes a
// committed demo file a critical finding again.
func TestATrackedEnvFileIsNotCriticalForAPlainValueOrAnEnvironmentSwitch(t *testing.T) {
	for _, test := range []struct{ name, content string }{
		{"plain text under a credential's name", "PLAID_SECRET=demo-secret-for-local\nPORT=8000\n"},
		{"an environment set to production", "NODE_ENV=production\nPORT=8000\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, map[string]string{".env.demo": test.content})

			// Act
			report := f.check()

			// Assert
			none(t, report, "env-file-not-ignored")
			contains(t, "evidence", only(t, report, "env-file-committed").Evidence, ".env.demo")
			if strings.Contains(report.Text(), "rotate") {
				t.Errorf("a line tells the reader to rotate a credential the file never held:\n%s", report.Text())
			}
		})
	}
}

// A tracked env file that holds a real key is still the worst of it.
func TestATrackedEnvFileThatHoldsARealKeyIsStillCritical(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".env.demo": "OPENAI_API_KEY=sk-proj-" + strings.Repeat("a1B2", 8) + "\n"})

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "env-file-not-ignored")
	if finding.Severity != Critical {
		t.Errorf("env-file-not-ignored is %s, want critical", finding.Severity)
	}
	contains(t, "evidence", finding.Evidence, "OPENAI_API_KEY")
}

// A name the code reads and no service declares is a credential by the same
// words, so a mail password under SMTP_PASS is one the fleet does not carry.
func TestACredentialNamedByAWordTheCheckDidNotKnowIsOneTheCodeReads(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"src/mail.ts": "const pass = process.env.SMTP_PASS\nconst codes = process.env.PILOT_ACCESS_CODES\nconst level = process.env.ZIP_CODES\n"})
	f.manifest("auth.json", `{"project":"northwind","services":[]}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "connector-undeclared")
	contains(t, "evidence", finding.Evidence, "SMTP_PASS (src/mail.ts:1)", "PILOT_ACCESS_CODES (src/mail.ts:2)")
	if strings.Contains(finding.Evidence, "ZIP_CODES") {
		t.Errorf("evidence %q names ZIP_CODES, which is no credential", finding.Evidence)
	}
}
