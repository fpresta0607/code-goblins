package routing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realBrief returns a brief the fleet actually dispatched, copied verbatim
// from data/<id>/brief.md, so the classifier is judged on the text it meets.
func realBrief(t *testing.T, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "briefs", id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestClassifyReadsTheAskNotTheWordAuth(t *testing.T) {
	// pd-signup-existing is an ordinary signup-form task. It names
	// components/auth/EmbeddedAuth.tsx and supabase.auth.signUp, so the old
	// substring rule read it as high-risk security work and handed it the
	// high-risk repair budget.
	brief := realBrief(t, "pd-signup-existing")
	if !strings.Contains(strings.ToLower(brief), "auth") {
		t.Fatal("premise: the brief no longer contains the substring the old rule tripped on")
	}
	a := Classify(brief)
	if a.Class == Security || a.Risk != "normal" {
		t.Errorf("Classify = %+v, want an ordinary login-form task, not security/high", a)
	}
}

func TestClassifyIgnoresTheAuthenticationBoilerplateEveryBriefCarries(t *testing.T) {
	// Every generated brief has an "## Authentication" section, which is why
	// the substring rule classified nearly the whole fleet as security work.
	brief := realBrief(t, "pdocs-welcome-drift")
	if !strings.Contains(brief, "## Authentication") {
		t.Fatal("premise: the brief lost its Authentication section")
	}
	if a := Classify(brief); a.Class == Security {
		t.Errorf("Classify = %+v, want the boilerplate ignored", a)
	}
}

func TestClassifyIgnoresAMigrationMentionedInPassing(t *testing.T) {
	// pdocs-welcome-drift is a drift-detector fix whose root cause names
	// "ten DB triggers (migration 257)" and whose constraints say not to run
	// migrations against prod. Nothing asks for a migration.
	brief := realBrief(t, "pdocs-welcome-drift")
	if !strings.Contains(strings.ToLower(brief), "migration") {
		t.Fatal("premise: the brief no longer mentions a migration")
	}
	a := Classify(brief)
	if a.Class == Migration || a.Risk != "normal" {
		t.Errorf("Classify = %+v, want a normal-risk fix, not migration/high", a)
	}
	if a.Class != Debug {
		t.Errorf("class = %s, want debug for a brief that opens with fix(welcome-briefing)", a.Class)
	}
}

func TestClassifyReadsAnAskedForMigrationAsMigration(t *testing.T) {
	briefs := []string{
		"# Brief x\n\n## Task\n\nCommit the schema as code: add a new migration with the same idempotent ALTER.\n",
		"# Brief x\n\n## Task\n\nMigrate the job cache from Redis to Supabase.\n",
		"# Brief x\n\n## Task\n\nWrite and apply the migration that drops the legacy column.\n",
	}
	for _, brief := range briefs {
		if a := Classify(brief); a.Class != Migration || a.Risk != "high" {
			t.Errorf("Classify(%q) = %+v, want migration/high", brief, a)
		}
	}
}

func TestClassifyLetsTheDeliveryKindDecideScout(t *testing.T) {
	if a := Classify(realBrief(t, "clockin-agent-identity-plan")); a.Class != Scout {
		t.Errorf("Classify = %+v, want scout for a brief whose Delivery says kind: scout", a)
	}
	ship := "# Brief x\n\n## Task\n\nInvestigate why the export stalls and fix it.\n\n## Delivery\n\nkind: ship\nmode: direct-PR\n"
	if a := Classify(ship); a.Class == Scout {
		t.Errorf("Classify = %+v, want a ship never to become a scout because its task says investigate", a)
	}
}

func TestClassifyReadsAnAuditAsScout(t *testing.T) {
	if a := Classify(realBrief(t, "pd-prod-readiness-audit")); a.Class != Scout || a.Risk != "normal" {
		t.Errorf("Classify = %+v, want scout for a report-first audit", a)
	}
}

func TestClassifyReadsARenameAsMechanical(t *testing.T) {
	if a := Classify(realBrief(t, "rename-goblins")); a.Class != Mechanical {
		t.Errorf("Classify = %+v, want mechanical for a pure rename", a)
	}
}

func TestClassifyKeepsABugReportThatMentionsARenameOffTheMechanicalLane(t *testing.T) {
	// siqshift-login-regression suspects "the Clock-In -> SIQshift rename
	// sweep" as a cause; it asks for a root-caused fix, not a rename.
	a := Classify(realBrief(t, "siqshift-login-regression"))
	if a.Class != Debug {
		t.Errorf("Classify = %+v, want debug", a)
	}
}

func TestClassifyDoesNotReadAPathOrACodeSpanAsAnAsk(t *testing.T) {
	// pc-magic-link says to read docs/architecture.md first; it is a
	// sign-in change, not architecture work.
	a := Classify(realBrief(t, "pc-magic-link"))
	if a.Class == Architecture || a.Class == Security || a.Risk != "normal" {
		t.Errorf("Classify = %+v, want a normal-risk build task", a)
	}
	if a := Classify("# Brief x\n\n## Task\n\nRun `scripts/migrations/check.sh` and report.\n"); a.Class == Migration {
		t.Errorf("Classify = %+v, want a code span ignored", a)
	}
}

func TestClassifyDoesNotReadConstraintsAsTheAsk(t *testing.T) {
	brief := "# Brief x\n\n## Task\n\nAdd a button to the settings page.\n\n## Constraints\n\nDo not touch billing, the security scan, or any migration.\n"
	if a := Classify(brief); a.Class != Implementation || a.Risk != "normal" {
		t.Errorf("Classify = %+v, want ordinary implementation", a)
	}
}

func TestClassifyReadsARenameOrAuditInsideAFeatureAsImplementation(t *testing.T) {
	for _, ask := range []string{
		"Add a rename action to the project menu.",
		"Let users rename a workspace to any unique name.",
		"Add an audit log for admin actions.",
	} {
		if a := Classify("# Brief x\n\n## Task\n\n" + ask + "\n"); a.Class != Implementation || a.Risk != "normal" {
			t.Errorf("Classify(%q) = %+v, want ordinary implementation", ask, a)
		}
	}
}

func TestClassifyReadsAGitCheckoutAsOrdinaryWorkAndAPaymentCheckoutAsSecurity(t *testing.T) {
	cases := []struct {
		ask   string
		class TaskClass
		risk  string
	}{
		{"Make cfo cleanup refuse a dirty project checkout.", Implementation, "normal"},
		{"Add Apple Pay to the Stripe checkout flow.", Security, "high"},
		{"Rework the checkout payment flow for saved cards.", Security, "high"},
	}
	for _, test := range cases {
		if a := Classify("# Brief x\n\n## Task\n\n" + test.ask + "\n"); a.Class != test.class || a.Risk != test.risk {
			t.Errorf("Classify(%q) = %+v, want %s/%s", test.ask, a, test.class, test.risk)
		}
	}
}

func TestClassifyCoversEveryDeclaredClass(t *testing.T) {
	cases := []struct {
		ask   string
		class TaskClass
		risk  string
	}{
		{"Security review of the session cookie signing.", Security, "high"},
		{"Add the migration that backfills tenant ids.", Migration, "high"},
		{"Rearchitect the ingest pipeline around a queue.", Architecture, "high"},
		{"Rescue the abandoned branch from gb-old and land it.", Rescue, "high"},
		{"Investigate why the nightly job doubles its rows and report.", Scout, "normal"},
		{"Fix the crash when the upload list is empty.", Debug, "normal"},
		{"Deploy main to production and verify the health endpoint.", Deployment, "normal"},
		{"Review the PR for tenant isolation regressions.", Review, "normal"},
		{"Rename the workspace label to code-goblins everywhere.", Mechanical, "normal"},
		{"Add a dark-mode toggle to the settings page.", Implementation, "normal"},
	}
	for _, test := range cases {
		a := Classify("# Brief x\n\n## Task\n\n" + test.ask + "\n")
		if a.Class != test.class || a.Risk != test.risk {
			t.Errorf("Classify(%q) = %+v, want %s/%s", test.ask, a, test.class, test.risk)
		}
	}
}
