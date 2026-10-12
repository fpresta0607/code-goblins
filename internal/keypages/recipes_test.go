package keypages

import (
	"strings"
	"testing"

	codegoblins "github.com/fpresta0607/code-goblins"
)

func TestTheShippedRecipesParseAndNameStripe(t *testing.T) {
	recipes, err := Parse(codegoblins.KeyPages)
	if err != nil {
		t.Fatal(err)
	}

	stripe, err := recipes.Provider("stripe")
	if err != nil {
		t.Fatal(err)
	}
	if stripe.Name != "Stripe" || len(stripe.Hosts) == 0 {
		t.Fatalf("stripe = %+v, want its name and hosts", stripe)
	}
}

func TestRecipesAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
		want string
	}{
		{"an unknown field", `{"providers":[{"id":"stripe","name":"Stripe","hosts":["stripe.com"],"keys":"all"}]}`, `unknown field "keys"`},
		{"an id with capitals", `{"providers":[{"id":"Stripe","name":"Stripe","hosts":["stripe.com"]}]}`, "lowercase letters and digits"},
		{"a provider listed twice", `{"providers":[{"id":"stripe","name":"Stripe","hosts":["stripe.com"]},{"id":"stripe","name":"Stripe","hosts":["stripe.com"]}]}`, "listed twice"},
		{"no name", `{"providers":[{"id":"stripe","name":" ","hosts":["stripe.com"]}]}`, "has no name"},
		{"no hosts", `{"providers":[{"id":"stripe","name":"Stripe","hosts":[]}]}`, "names no hosts"},
		{"a wildcard in the middle", `{"providers":[{"id":"stripe","name":"Stripe","hosts":["dash.*.stripe.com"]}]}`, "not a host name"},
		{"a bare wildcard", `{"providers":[{"id":"stripe","name":"Stripe","hosts":["*"]}]}`, "not a host name"},
		{"a host with a port", `{"providers":[{"id":"stripe","name":"Stripe","hosts":["stripe.com:443"]}]}`, "not a host name"},
		{"a single label", `{"providers":[{"id":"stripe","name":"Stripe","hosts":["localhost"]}]}`, "not a host name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.json))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse = %v, want an error saying %q", err, tc.want)
			}
		})
	}
}

func TestAProviderWithNoRecipeNamesTheOnesThereAre(t *testing.T) {
	recipes := Recipes{Providers: []Provider{{ID: "stripe"}, {ID: "resend"}}}

	_, err := recipes.Provider("openrouter")

	if err == nil || !strings.Contains(err.Error(), "stripe, resend") {
		t.Fatalf("Provider = %v, want an error naming stripe and resend", err)
	}
}
