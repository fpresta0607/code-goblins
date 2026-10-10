package projectcheck

import (
	"strings"
	"testing"
)

const twoServices = `{"project":"northwind","services":[
	{"name":"stripe","method":"env","env":["STRIPE_SECRET_KEY"]},
	{"name":"resend","method":"env","env":["RESEND_API_KEY"]}
]}`

// A service the manifest declares and nothing in the repository names is
// injected into every goblin's terminal for no reader.
func TestAConnectorDeclaredAndUsedNowhereIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		"app/pay.py":       "import os\nkey = os.environ[\"STRIPE_SECRET_KEY\"]\n",
		"docs/email.md":    "Set RESEND_API_KEY to send mail.\n",
		"app/test_mail.py": "RESEND_API_KEY = \"re_test\"\n",
		"app/__init__.py":  "",
	})
	f.manifest("auth.json", twoServices)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "connector-unused")
	if finding.Severity != Low || finding.Area != AreaConnectors {
		t.Errorf("connector-unused is %s in %s, want low in connectors", finding.Severity, finding.Area)
	}
	contains(t, "evidence", finding.Evidence, "resend", "RESEND_API_KEY")
	if strings.Contains(finding.Evidence, "stripe") {
		t.Errorf("evidence %q names stripe, which app/pay.py reads", finding.Evidence)
	}
}

// A settings class reads a variable through a lower-case field, so a name
// counts as used however its letters are cased, and under an alias.
func TestAConnectorReadThroughALowerCaseFieldOrAnAliasIsUsed(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		"app/config.py": "class Settings(BaseSettings):\n    resend_api_key: str = \"\"\n",
		"app/pay.ts":    "const key = process.env.STRIPE_KEY\n",
	})
	f.manifest("auth.json", `{"project":"northwind","services":[
		{"name":"stripe","method":"env","env":["STRIPE_SECRET_KEY"],"aliases":{"STRIPE_SECRET_KEY":["STRIPE_KEY"]}},
		{"name":"resend","method":"env","env":["RESEND_API_KEY"]}
	]}`)

	// Act
	report := f.check()

	// Assert
	none(t, report, "connector-unused")
	none(t, report, "connector-undeclared")
	only(t, report, "connectors-examined")
}

// A credential the code reads and no service declares is one no goblin's
// terminal carries and no preflight ever checked.
func TestAConnectorUsedAndUndeclaredIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		"app/pay.py":         "import os\nkey = os.environ[\"STRIPE_SECRET_KEY\"]\n",
		"app/search.py":      "import os\n\nbrave = os.getenv(\"BRAVE_API_KEY\")\nport = os.getenv(\"PORT\")\nllm = os.getenv(\"OPENAI_API_KEY\")\n",
		"web/src/client.ts":  "export const jina = process.env.JINA_API_KEY\nexport const mode = import.meta.env.VITE_MODE\n",
		"cmd/tool/main.go":   "package main\n\nimport \"os\"\n\nvar token = os.Getenv(\"REGISTRY_TOKEN\")\n",
		".env.example":       "STRIPE_SECRET_KEY=\nREGRID_API_KEY=\nPORT=8000\n",
		"app/test_search.py": "import os\nfake = os.getenv(\"FAKE_API_KEY\")\n",
	})
	f.manifest("auth.json", `{"project":"northwind","services":[{"name":"stripe","method":"env","env":["STRIPE_SECRET_KEY"]}]}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "connector-undeclared")
	if finding.Severity != Medium || finding.Area != AreaConnectors {
		t.Errorf("connector-undeclared is %s in %s, want medium in connectors", finding.Severity, finding.Area)
	}
	contains(t, "evidence", finding.Evidence,
		"BRAVE_API_KEY (app/search.py:3)", "JINA_API_KEY (web/src/client.ts:1)", "REGISTRY_TOKEN (cmd/tool/main.go:5)", "REGRID_API_KEY (.env.example)")
	for _, wrong := range []string{"STRIPE_SECRET_KEY", "PORT", "VITE_MODE", "OPENAI_API_KEY", "FAKE_API_KEY"} {
		if strings.Contains(finding.Evidence, wrong) {
			t.Errorf("evidence %q names %s, which is declared, no credential, a harness's own key or read only by a test", finding.Evidence, wrong)
		}
	}
}

// An MCP connector authenticates with the credential its entry names, and a
// goblin receives it only when a service declares that credential.
func TestAnMCPConnectorWhoseCredentialNoServiceDeclaresIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".mcp.json": `{"mcpServers":{
		"docs":{"type":"http","url":"https://mcp.example.com","headers":{"Authorization":"Bearer ${DOCS_MCP_TOKEN}"}},
		"search":{"command":"npx","args":["search-mcp"],"env":{"SEARCH_KEY":"${STRIPE_SECRET_KEY}"}},
		"crm":{"type":"http","url":"https://crm.example.com","bearerTokenEnvVar":"CRM_TOKEN"}
	}}`})
	f.manifest("auth.json", `{"project":"northwind","services":[{"name":"stripe","method":"env","env":["STRIPE_SECRET_KEY"]}]}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "connector-undeclared")
	contains(t, "evidence", finding.Evidence, "DOCS_MCP_TOKEN (.mcp.json server docs)", "CRM_TOKEN (.mcp.json server crm)")
	if strings.Contains(finding.Evidence, "STRIPE_SECRET_KEY") {
		t.Errorf("evidence %q names STRIPE_SECRET_KEY, which is declared", finding.Evidence)
	}
	none(t, report, "connector-unused")
}

// With no manifest a spawn injects nothing and preflights nothing, and every
// credential the code reads is undeclared.
func TestAProjectWithNoAuthManifestIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"app/pay.py": "import os\nkey = os.environ[\"STRIPE_SECRET_KEY\"]\n"})

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "auth-missing")
	if finding.Severity != Medium {
		t.Errorf("auth-missing is %s, want medium", finding.Severity)
	}
	undeclared := only(t, report, "connector-undeclared")
	contains(t, "evidence", undeclared.Evidence, "STRIPE_SECRET_KEY (app/pay.py:2)")
}

func TestAnInvalidAuthManifestIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.manifest("auth.json", `{"project":"northwind","services":[{"name":"stripe","method":"vault"}]}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "auth-invalid")
	if finding.Severity != High {
		t.Errorf("auth-invalid is %s, want high", finding.Severity)
	}
	contains(t, "evidence", finding.Evidence, "vault")
}

// The line that says what was examined is there whatever was found, so a
// search that stopped reading does not read as a clean project.
func TestConnectorsThatAgreePassAndSayWhatWasExamined(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		"app/pay.py":  "import os\nkey = os.environ[\"STRIPE_SECRET_KEY\"]\nmail = os.environ.get(\"RESEND_API_KEY\")\n",
		"app/tool.py": "import os\nprobe = os.environ.get('GOOGLE_REPORT')\n",
	})
	f.manifest("auth.json", `{"project":"northwind","services":[
		{"name":"stripe","method":"env","env":["STRIPE_SECRET_KEY"]},
		{"name":"resend","method":"env","env":["RESEND_API_KEY"]},
		{"name":"google-report","method":"cli","probe":["gcloud","auth","list"]}
	]}`)

	// Act
	report := f.check()

	// Assert
	none(t, report, "connector-unused")
	none(t, report, "connector-undeclared")
	finding := only(t, report, "connectors-examined")
	if finding.Severity != OK {
		t.Errorf("connectors-examined is %s, want ok", finding.Severity)
	}
	contains(t, "says", finding.Says, "3 services", "2 credential names")
	contains(t, "evidence", finding.Evidence, "google-report")
	if !report.Passed(AreaConnectors) {
		t.Errorf("the connectors area did not pass:\n%s", report.Text())
	}
}

const usedThroughTools = `{"project":"northwind","services":[
	{"name":"stripe","method":"env","env":["STRIPE_SECRET_KEY"]},
	{"name":"github","method":"cli","env":["GITHUB_TOKEN"],"probe":["gh","auth","status"]},
	{"name":"vercel","method":"cli","probe":["vercel","whoami"]},
	{"name":"railway","method":"env","env":["RAILWAY_TOKEN"],"login":["railway","login","--browserless"]},
	{"name":"postgres","method":"env","env":["DATABASE_URL"],"note":"direct connection for migrations through psql"},
	{"name":"resend","method":"env","env":["RESEND_API_KEY"]},
	{"name":"mailer","method":"env","env":["MAILER_TOKEN"],"default":true}
]}`

// The fleet uses some services through a command line tool: it pushes with
// gh in every project and no repository names GITHUB_TOKEN. The manifest
// itself says so, by a method of cli or a command that starts the tool, and
// a note says what else uses a service. Only a service that nothing reads
// and whose entry says nothing is reported.
func TestAServiceTheManifestNamesAUserForIsNotUnused(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"app/pay.py": "import os\nkey = os.environ[\"STRIPE_SECRET_KEY\"]\n"})
	f.manifest("auth.json", usedThroughTools)

	// Act
	report := f.check("gh")

	// Assert
	finding := only(t, report, "connector-unused")
	if finding.Severity != Low {
		t.Errorf("connector-unused is %s, want low", finding.Severity)
	}
	contains(t, "says", finding.Says, "2 declared services")
	contains(t, "evidence", finding.Evidence, "resend: RESEND_API_KEY", "mailer: MAILER_TOKEN", "every task whose brief has no credentials line")
	for _, used := range []string{"stripe", "github", "vercel", "railway", "postgres"} {
		if strings.Contains(finding.Evidence, used) {
			t.Errorf("evidence %q names %s, which the repository reads or whose entry names what uses it", finding.Evidence, used)
		}
	}
	examined := only(t, report, "connectors-examined")
	contains(t, "evidence", examined.Evidence, "github (gh, which is on PATH)", "vercel (vercel, which is not on PATH)", "railway (railway", "On a note alone, which this check cannot verify: postgres")
}

// The draft keeps or drops a service by the same one rule, so github is not
// kept for one project and dropped for the next by whether its code happens
// to name the variable.
func TestTheDraftKeepsEveryServiceTheCheckCountsAsUsed(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"app/pay.py": "import os\nkey = os.environ[\"STRIPE_SECRET_KEY\"]\n"})
	f.manifest("auth.json", usedThroughTools)

	// Act
	draft := f.check("gh").Draft

	// Assert
	var kept []string
	for _, service := range draft.Services {
		kept = append(kept, service.Name)
	}
	if got, want := strings.Join(kept, ", "), "stripe, github, vercel, railway, postgres"; got != want {
		t.Errorf("the draft keeps %s, want %s: every declared service but the ones connector-unused names", got, want)
	}
}

// A goblin's terminal is never where a workflow's secret comes from: GitHub
// supplies it. A publishable name is handed to every browser. Neither is a
// credential the fleet should declare, and the line that says what was
// examined names both, so what was left out is seen.
func TestANameOnlyAWorkflowReadsOrAPublishableNameIsNotUndeclared(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{
		".github/workflows/release.yml": "jobs:\n  sign:\n    runs-on: windows-latest\n    steps:\n      - run: signtool sign /p $env:WINDOWS_CERTIFICATE_PASSWORD app.exe\n      - run: node -e \"console.log(process.env.SIGNING_TOKEN)\"\n",
		"web/src/map.ts":                "export const token = process.env.NEXT_PUBLIC_MAPBOX_TOKEN\nexport const key = process.env.NEXT_PUBLIC_SUPABASE_PUBLISHABLE_KEY\nexport const anon = import.meta.env.VITE_SUPABASE_ANON_KEY\n",
		"app/pay.py":                    "import os\nkey = os.environ[\"BRAVE_API_KEY\"]\nsign = os.environ[\"SIGNING_TOKEN\"]\n",
	})
	f.manifest("auth.json", `{"project":"northwind","services":[]}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "connector-undeclared")
	contains(t, "says", finding.Says, "2 credentials")
	contains(t, "evidence", finding.Evidence, "BRAVE_API_KEY (app/pay.py:2)", "SIGNING_TOKEN (app/pay.py:3)")
	for _, wrong := range []string{"WINDOWS_CERTIFICATE_PASSWORD", "NEXT_PUBLIC_MAPBOX_TOKEN", "NEXT_PUBLIC_SUPABASE_PUBLISHABLE_KEY", "VITE_SUPABASE_ANON_KEY"} {
		if strings.Contains(finding.Evidence, wrong) {
			t.Errorf("evidence %q names %s, which only a workflow reads or which is publishable", finding.Evidence, wrong)
		}
	}
	examined := only(t, report, "connectors-examined")
	contains(t, "evidence", examined.Evidence,
		"Left out, since only a workflow reads them and GitHub supplies them there: WINDOWS_CERTIFICATE_PASSWORD",
		"Left out as publishable: NEXT_PUBLIC_MAPBOX_TOKEN, NEXT_PUBLIC_SUPABASE_PUBLISHABLE_KEY, VITE_SUPABASE_ANON_KEY")
}

// A repository keeps its env template under the name it likes. One named
// without the leading dot was not read as an example, so the line said "0
// env examples" and the names in it were never read.
func TestAnEnvTemplateNamedWithoutTheLeadingDotIsReadAsAnExample(t *testing.T) {
	for _, name := range []string{"env.template", "env.example", ".env-example", "example.env", "web/env.sample"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, map[string]string{name: "BRAVE_API_KEY=\nPORT=8000\n"})
			f.manifest("auth.json", `{"project":"northwind","services":[]}`)

			// Act
			report := f.check()

			// Assert
			contains(t, "evidence", only(t, report, "connector-undeclared").Evidence, "BRAVE_API_KEY ("+name+")")
			contains(t, "evidence", only(t, report, "connectors-examined").Evidence, "1 env examples")
			contains(t, "evidence", only(t, report, "env-files-ignored").Evidence, "Examples, which are committed on purpose: "+name)
			none(t, report, "env-file-committed")
			none(t, report, "env-file-not-ignored")
		})
	}
}

// A file that only has env in its name is no env file.
func TestAFileThatOnlyHasEnvInItsNameIsNoEnvFile(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"src/env.ts": "export const BRAVE_API_KEY = ''\n", "environment.yml": "name: app\n", "env.example.md": "BRAVE_API_KEY=\n"})

	// Act
	report := f.check()

	// Assert
	contains(t, "evidence", only(t, report, "connectors-examined").Evidence, "0 env examples")
	ignored := only(t, report, "env-files-ignored")
	contains(t, "says", ignored.Says, "0 of the project's 0 env files")
	if strings.Contains(ignored.Evidence, "Examples") {
		t.Errorf("evidence %q names an example, and the repository has none", ignored.Evidence)
	}
	none(t, report, "env-file-committed")
	none(t, report, "env-file-not-ignored")
}
