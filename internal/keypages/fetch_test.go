package keypages

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	codegoblins "github.com/fpresta0607/code-goblins"
)

func TestFetchRecipeSelectsOnlyReviewedStripeTestKeys(t *testing.T) {
	recipes, err := Parse(codegoblins.KeyPages)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"STRIPE_TEST_SECRET_KEY", "STRIPE_TEST_PUBLISHABLE_KEY"}

	provider, keys, err := recipes.Fetch("https://dashboard.stripe.com/test/apikeys", names)

	if err != nil || provider.ID != "stripe" || len(keys) != 2 {
		t.Fatalf("reviewed fetch selection = %s, %d keys, %v", provider.ID, len(keys), err)
	}
	for _, key := range keys {
		if !slices.Contains(names, key.Name) || key.Row == "" || key.Prefix != "sk_test_" && key.Prefix != "pk_test_" {
			t.Fatalf("unexpected reviewed key: %+v", key)
		}
	}
	if page, err := provider.AccountPage("acct_fixture"); err != nil || page != "https://dashboard.stripe.com/acct_fixture/test/apikeys" {
		t.Fatalf("account page = %q, %v", page, err)
	}
}

func TestFetchRecipeLeavesNamesItDoesNotReadForAPaste(t *testing.T) {
	recipes, err := Parse(codegoblins.KeyPages)
	if err != nil {
		t.Fatal(err)
	}

	_, keys, err := recipes.Fetch("https://dashboard.stripe.com/test/apikeys", []string{"STRIPE_WEBHOOK_SECRET", "STRIPE_TEST_SECRET_KEY"})

	if err != nil || len(keys) != 1 || keys[0].Name != "STRIPE_TEST_SECRET_KEY" {
		t.Fatalf("keys = %+v, %v, want only the secret key the recipe reads", keys, err)
	}
}

func TestFetchRecipeRefusesUnreviewedPagesNamesAndAccounts(t *testing.T) {
	recipes, err := Parse(codegoblins.KeyPages)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, page string
		names      []string
	}{
		{"live page", "https://dashboard.stripe.com/apikeys", []string{"STRIPE_TEST_SECRET_KEY"}},
		{"different host", "https://elsewhere.example/test/apikeys", []string{"STRIPE_TEST_SECRET_KEY"}},
		{"query", "https://dashboard.stripe.com/test/apikeys?account=other", []string{"STRIPE_TEST_SECRET_KEY"}},
		{"fragment", "https://dashboard.stripe.com/test/apikeys#other", []string{"STRIPE_TEST_SECRET_KEY"}},
		{"credentials", "https://user@dashboard.stripe.com/test/apikeys", []string{"STRIPE_TEST_SECRET_KEY"}},
		{"port", "https://dashboard.stripe.com:9000/test/apikeys", []string{"STRIPE_TEST_SECRET_KEY"}},
		{"no names", "https://dashboard.stripe.com/test/apikeys", nil},
		{"live name", "https://dashboard.stripe.com/test/apikeys", []string{"STRIPE_LIVE_SECRET_KEY"}},
		{"webhook", "https://dashboard.stripe.com/test/apikeys", []string{"STRIPE_WEBHOOK_SECRET"}},
		{"duplicate name", "https://dashboard.stripe.com/test/apikeys", []string{"STRIPE_TEST_SECRET_KEY", "STRIPE_TEST_SECRET_KEY"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := recipes.Fetch(test.page, test.names); err == nil {
				t.Fatal("unreviewed fetch was accepted")
			}
		})
	}
	provider, err := recipes.Provider("stripe")
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"", "other", "acct_/../live", "acct_a?other", "acct_a#other", "acct_a/another"} {
		if _, err := provider.AccountPage(account); err == nil {
			t.Fatal("an unreviewed account address was accepted")
		}
	}
}

func TestFetchRecipeRejectsDangerousAndAmbiguousControls(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Provider)
	}{
		{"roll", func(p *Provider) { p.Keys[0].Reveal = "Roll test key" }},
		{"delete", func(p *Provider) { p.Keys[0].Reveal = "Delete key" }},
		{"revoke", func(p *Provider) { p.Keys[0].Reveal = "Revoke key" }},
		{"create", func(p *Provider) { p.Keys[0].Reveal = "Create secret key" }},
		{"empty row", func(p *Provider) { p.Keys[0].Row = "" }},
		{"duplicate row", func(p *Provider) { p.Keys[1].Row = p.Keys[0].Row }},
		{"duplicate name", func(p *Provider) { p.Keys[1].Name = p.Keys[0].Name }},
		{"invalid name", func(p *Provider) { p.Keys[0].Name = "not a name" }},
		{"live prefix", func(p *Provider) { p.Keys[0].Prefix = "sk_live_" }},
		{"missing account check", func(p *Provider) { p.AccountPath = "" }},
		{"unanchored account check", func(p *Provider) { p.AccountPath = "(acct_[a-z]+)" }},
		{"multiple account captures", func(p *Provider) { p.AccountPath = "^/(acct_([a-z]+))/test/apikeys$" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			recipes, err := Parse(codegoblins.KeyPages)
			if err != nil {
				t.Fatal(err)
			}
			test.change(&recipes.Providers[0])
			data, err := json.Marshal(recipes)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := Parse(data); err == nil || !strings.Contains(err.Error(), "key page recipes") {
				t.Fatalf("unsafe recipe = %v, want refusal", err)
			}
		})
	}
}
