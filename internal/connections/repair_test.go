package connections

import (
	"strings"
	"testing"
)

// A repair is planned from the project's manifest and only when the project's
// item offers the action: a sign-in page the manifest names, a variable it
// declares, or a service's login command; never a command, a page or a variable
// the request brings.
func TestRepairPlanOffersOnlyWhatTheItemListsAndTheManifestDeclares(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	e.manifest(t, "acme", `[
		{"name":"hosted","method":"oauth","url":"https://login.acme.example/start"},
		{"name":"plain","method":"oauth","url":"http://login.acme.example/start"},
		{"name":"keyed","method":"env","env":["ACME_KEY","OPENAI_API_KEY"]},
		{"name":"tool","method":"cli","login":["acme-cli","login"]}
	]`)
	project := Project{Name: "acme", Level: Level{Items: []Item{
		{ID: "service:hosted", Actions: []string{"login", "cli"}},
		{ID: "service:plain", Actions: []string{"login"}},
		{ID: "service:keyed", Actions: []string{"store:ACME_KEY", "store:OPENAI_API_KEY", "store:LOOSE_KEY"}},
		{ID: "service:tool", Actions: []string{"cli"}},
		{ID: "mcp:remote", Actions: []string{"store:ACME_KEY"}},
		{ID: "service:unknown", Actions: []string{"login"}},
	}}}
	cases := []struct {
		name, connection, action string
		want                     Repair
		wantError                string
	}{
		{"sign-in page", "service:hosted", "login", Repair{URL: "https://login.acme.example/start"}, ""},
		{"login command", "service:tool", "cli", Repair{Service: "tool"}, ""},
		{"store a declared variable", "service:keyed", "store:ACME_KEY", Repair{Credential: "ACME_KEY"}, ""},
		{"store for an MCP server", "mcp:remote", "store:ACME_KEY", Repair{Credential: "ACME_KEY"}, ""},
		{"action the item does not list", "service:tool", "login", Repair{}, "Refresh this connection"},
		{"connection the project does not list", "service:other", "login", Repair{}, "Refresh this connection"},
		{"page that is not https", "service:plain", "login", Repair{}, "no longer available"},
		{"billing key", "service:keyed", "store:OPENAI_API_KEY", Repair{}, "no longer available"},
		{"variable the manifest does not declare", "service:keyed", "store:LOOSE_KEY", Repair{}, "no longer available"},
		{"service the manifest does not declare", "service:unknown", "login", Repair{}, "no longer available"},
		{"login command the service has no", "service:hosted", "cli", Repair{}, "no longer available"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Act
			plan, err := e.RepairPlan(project, test.connection, test.action)

			// Assert
			if test.wantError == "" && err != nil || test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
			if plan != test.want {
				t.Fatalf("plan = %+v, want %+v", plan, test.want)
			}
		})
	}
}
