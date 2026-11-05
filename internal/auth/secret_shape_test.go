package auth

import (
	"encoding/base64"
	"strings"
	"testing"
)

// The names every project manifest on the fleet declared on 2026-09-30, plus
// a few conventional ones: a credential request must take each as a name.
func TestSecretShapeTakesEveryDeclaredCredentialName(t *testing.T) {
	for _, name := range []string{
		"AUTH_BASE_URL", "AUTH_USERNAME", "CONTEXT7_API_KEY", "DATABASE_URL", "FLY_API_TOKEN", "FLY_BROKER_API_TOKEN",
		"FLY_PROD_API_TOKEN", "GITHUB_TOKEN", "IONOS_HOST", "IONOS_ROOT_PASSWORD", "IONOS_SSH_KEY_PATH", "IONOS_SSH_USER",
		"NEXT_PUBLIC_SUPABASE_ANON_KEY", "NEXT_PUBLIC_SUPABASE_PUBLISHABLE_KEY", "NEXT_PUBLIC_SUPABASE_URL", "OPENAI_API_KEY",
		"OPENROUTER_API_KEY", "PLAID_CLIENT_ID", "PLAID_SECRET", "QDRANT_API_KEY", "QDRANT_URL", "RAILWAY_TOKEN", "REDIS_URL",
		"RESEND_API_KEY", "SENTRY_DSN", "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET", "SUPABASE_ACCESS_TOKEN",
		"SUPABASE_PUBLISHABLE_KEY", "SUPABASE_SERVICE_KEY", "SUPABASE_SERVICE_ROLE_KEY", "SUPABASE_URL", "TEST_DATABASE_URL",
		"UPSTASH_API_KEY", "UPSTASH_EMAIL", "VALKEY_BROKER_PASSWORD", "VERCEL_TOKEN",
		"AWS_SECRET_ACCESS_KEY", "AWS_ACCESS_KEY_ID", "GOOGLE_APPLICATION_CREDENTIALS", "S3_BUCKET_2026", "OAUTH2_CLIENT_SECRET",
		"AZURE_STORAGE_CONNECTION_STRING_V2", "ASIA_PACIFIC_ENDPOINT", "BASE64_ENCODED_SERVICE_ACCOUNT_JSON",
	} {
		if reason := SecretShape(name); reason != "" {
			t.Errorf("SecretShape(%q) = %q, want a name", name, reason)
		}
	}
}

// Sentences a CFO writes as a request's reason, and the pages a request links
// to, are not values either.
func TestSecretShapeTakesPlainReasonsAndLinkSegments(t *testing.T) {
	for _, text := range []string{
		"Charge", "test", "cards", "in", "the", "checkout", "tests.", "(webhooks)", "StripeWebhookHandler", "x86_64",
		"dashboard.stripe.com", "apikeys", "settings", "personal_access_tokens", "9f86d081", "2026-10-01",
	} {
		if reason := SecretShape(text); reason != "" {
			t.Errorf("SecretShape(%q) = %q, want plain text", text, reason)
		}
	}
}

// Every sample is assembled at run time, so no file in the repository holds
// anything a secret scanner would read as a live credential.
func TestSecretShapeRefusesValues(t *testing.T) {
	random := "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
	for index, value := range []string{
		"sk" + "_live_" + random,
		"sk" + "_test_" + random,
		"rk" + "_live_" + random,
		"pk" + "_test_" + random,
		"whsec" + "_" + random,
		"ghp" + "_" + random,
		"github" + "_pat_" + random,
		"xox" + "b-1234-" + random,
		"AKIA" + "Q7LM2XV9RT4KP8ZW",
		"eyJ" + "hbGciOiJIUzI1NiJ9." + random,
		"sk" + "-proj-" + random,
		"sk" + "-ant-" + random,
		"AIza" + random,
		"glpat" + "-" + random,
		"npm" + "_" + random,
		"hf" + "_" + random,
		"re" + "_" + random,
		"sbp" + "_" + random,
		"sb" + "_secret_" + random,
		"sb" + "_publishable_" + random,
		"-----BEGIN" + " PRIVATE KEY-----",
		"STRIPE_SECRET_KEY=" + "anything",
		"NAME=value",
		random,
		"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b",
		"123e4567-e89b-12d3-a456-426614174000",
		"QwErTyUiOpAsDfGhJkLzXcVbNmQwErTyUi",
		"wJalrXUtnFEMI/K7MDENG/bPxRfiCY" + "EXAMPLEKEY",
	} {
		if reason := SecretShape(value); reason == "" {
			t.Errorf("SecretShape took sample %d (%d characters) as plain text", index, len(value))
		} else if strings.Contains(reason, value) {
			t.Errorf("SecretShape repeats sample %d in its reason", index)
		}
	}
}

// token is a JSON Web Token whose payload is claims, assembled at run time.
func token(claims string) string {
	return "eyJ" + "hbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".q7Lm2Xv9Rt4Kp8Zw3Nb6"
}

// A key is public only when its field's name and its own text both say so,
// and the whole value is that one key.
func TestPlainPublicKeyNeedsThePublicNameAndThePublicValue(t *testing.T) {
	random := "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
	for name, c := range map[string]struct {
		field, value string
		want         bool
	}{
		"a Supabase key under NEXT_PUBLIC":   {"NEXT_PUBLIC_SUPABASE_KEY", "sb" + "_publishable_" + random, true},
		"a Stripe key named publishable":     {"STRIPE_PUBLISHABLE_KEY", "pk" + "_live_" + random, true},
		"a lower case name":                  {"stripe_publishable_key", "pk" + "_test_" + random, true},
		"a bundled name":                     {"REACT_APP_STRIPE_KEY", "pk" + "_test_" + random, true},
		"a Mapbox token":                     {"NEXT_PUBLIC_MAPBOX_TOKEN", "pk" + ".eyJ1IjoibWFkZS11cCJ9." + random, true},
		"an anon token":                      {"SUPABASE_ANON_KEY", token(`{"role":"anon"}`), true},
		"a field not named public":           {"MADE_UP_ACCOUNT_REF", "sb" + "_publishable_" + random, false},
		"a name that only holds the letters": {"CANON_KEY", "pk" + "_test_" + random, false},
		"random text":                        {"NEXT_PUBLIC_MADE_UP_KEY", random, false},
		"an empty value":                     {"NEXT_PUBLIC_MADE_UP_KEY", "", false},
		"a service role token":               {"SUPABASE_ANON_KEY", token(`{"role":"service_role"}`), false},
		"a token with no role":               {"SUPABASE_ANON_KEY", token(`{"iss":"made-up"}`), false},
		"an anon role after another key":     {"SUPABASE_ANON_KEY", "xox" + "b-1234." + base64.RawURLEncoding.EncodeToString([]byte(`{"role":"anon"}`)) + ".x", false},
		"a secret key":                       {"NEXT_PUBLIC_MADE_UP_KEY", "sk" + "_live_" + random, false},
		"two words":                          {"NEXT_PUBLIC_MADE_UP_KEY", "pk" + "_test_" + random + " " + random, false},
		"an assignment":                      {"NEXT_PUBLIC_MADE_UP_KEY", "pk" + "_test_" + random + "=" + random, false},
		"a secret run into it":               {"NEXT_PUBLIC_MADE_UP_KEY", "sb" + "_publishable_" + random + "sb" + "_secret_" + random, false},
		"a Stripe secret run into it":        {"NEXT_PUBLIC_MADE_UP_KEY", "pk" + "_test_" + random + "sk" + "_live_" + random, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := PlainPublicKey(c.field, c.value); got != c.want {
				t.Errorf("PlainPublicKey(%q, a value of %d characters) = %v, want %v", c.field, len(c.value), got, c.want)
			}
		})
	}
}

// A secret key is one whatever is around it, while a public key, a word and a
// short value that only starts like a key are not.
func TestSecretKeyKnowsSecretsFromPublicKeysAndWords(t *testing.T) {
	random := "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
	for name, c := range map[string]struct {
		word string
		want bool
	}{
		"a Supabase secret key":         {"sb" + "_secret_" + random, true},
		"a secret key of letters alone": {"sb" + "_secret_" + "ZkQxLmVbNcRtWyHsJdFgA", true},
		"a Stripe secret key":           {"sk" + "_live_" + random, true},
		"a quoted secret key":           {`"` + "sk" + "_live_" + random + `",`, true},
		"a service role token":          {token(`{"role":"service_role"}`), true},
		"a token with no role":          {token(`{"iss":"made-up"}`), true},
		"a Slack token":                 {"xox" + "b-1234-" + random, true},
		"an AWS access key ID":          {"AKIA" + "Q7LM2XV9RT4KP8ZW", true},
		"a line of an env file":         {"SUPABASE_SECRET_KEY=" + "sb" + "_secret_" + random, true},
		"a quoted line of an env file":  {`STRIPE_KEY="` + "sk" + "_live_" + random + `"`, true},
		"the start of a private key":    {"-----BEGIN", true},
		"a public key's env file line":  {"NEXT_PUBLIC_KEY=" + "pk" + "_test_" + random, false},
		"an address that starts a key":  {"sk" + "-support.team1@example.com", false},
		"a Supabase publishable key":    {"sb" + "_publishable_" + random, false},
		"a Stripe publishable key":      {"pk" + "_live_" + random, false},
		"a quoted publishable key":      {`"` + "pk" + "_live_" + random + `"`, false},
		"an anon token":                 {token(`{"role":"anon"}`), false},
		"a short part number":           {"sk" + "-1234", false},
		"a word":                        {"xoxo", false},
		"random text of no known kind":  {random, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := SecretKey(c.word); got != c.want {
				t.Errorf("SecretKey(a word of %d characters) = %v, want %v", len(c.word), got, c.want)
			}
		})
	}
}

// A field named as an ID takes the identifiers services show openly, and only
// those: a longer value, one with a key's prefix or whole shape, or a field
// not named as an ID reads as a key.
func TestPlainIdentifierTakesShortIdentifiersInIDFieldsOnly(t *testing.T) {
	for name, c := range map[string]struct {
		field, value string
		want         bool
	}{
		"a mixed case client ID":             {"MADE_UP_CLIENT_ID", "k7Qx9L" + "m2Vb4Rt8Nc", true},
		"a lower case field name":            {"made_up_client_id", "k7Qx9L" + "m2Vb4Rt8Nc", true},
		"a field named ID":                   {"ID", "k7Qx9L" + "m2Vb4Rt8Nc", true},
		"twenty letters and digits":          {"MADE_UP_CLIENT_ID", "A1b2C3d4E5" + "f6G7h8I9j0", true},
		"twenty one letters and digits":      {"MADE_UP_CLIENT_ID", "A1b2C3d4E5" + "f6G7h8I9j0K", false},
		"an empty value":                     {"MADE_UP_CLIENT_ID", "", false},
		"an AWS access key ID's shape":       {"MADE_UP_KEY_ID", "AKIA" + "ABCDEFGHIJKLMNOP", false},
		"a key's prefix":                     {"MADE_UP_CLIENT_ID", "eyJ" + "hbGciOiJ" + "IUzI1", false},
		"a hyphenated value":                 {"MADE_UP_CLIENT_ID", "k7Qx9-Lm2Vb4-Rt8N", false},
		"a field not named as an ID":         {"MADE_UP_TOKEN", "k7Qx9L" + "m2Vb4Rt8Nc", false},
		"a field whose name only ends in ID": {"MADE_UP_VALID", "k7Qx9L" + "m2Vb4Rt8Nc", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := PlainIdentifier(c.field, c.value); got != c.want {
				t.Errorf("PlainIdentifier(%q, %q) = %v, want %v", c.field, c.value, got, c.want)
			}
		})
	}
}
