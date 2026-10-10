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
