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

// Sentences a CEO writes as a request's reason, and the pages a request links
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
		"a Google key run into it":           {"NEXT_PUBLIC_GOOGLE_KEY", "pk" + "_test_" + random + exposed("AI"+"za", 35), false},
		"a Google key run into an anon key":  {"NEXT_PUBLIC_GOOGLE_KEY", token(`{"role":"anon"}`) + exposed("AI"+"za", 35), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := PlainPublicKey(c.field, c.value); got != c.want {
				t.Errorf("PlainPublicKey(%q, a value of %d characters) = %v, want %v", c.field, len(c.value), got, c.want)
			}
		})
	}
}

// A secret key is one whatever is around it, while a public key, a word and a
// value that only starts like a key are not. The whole value is read, so a
// secret counts wherever in it the key sits.
func TestSecretKeyKnowsSecretsFromPublicKeysAndWords(t *testing.T) {
	random := "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
	googleKey := exposed("AI"+"za", 35)
	for name, c := range map[string]struct {
		value string
		want  bool
	}{
		"a Supabase secret key":              {"sb" + "_secret_" + random, true},
		"a secret key of letters alone":      {"sb" + "_secret_" + "ZkQxLmVbNcRtWyHsJdFgA", true},
		"a Stripe secret key":                {"sk" + "_live_" + random, true},
		"a quoted secret key":                {`"` + "sk" + "_live_" + random + `",`, true},
		"a service role token":               {token(`{"role":"service_role"}`), true},
		"a token with no role":               {token(`{"iss":"made-up"}`), true},
		"a service role token in a sentence": {"the key is " + token(`{"role":"service_role"}`), true},
		"a Slack token":                      {"xox" + "b-1234-" + random, true},
		"an AWS access key ID":               {"AKIA" + "Q7LM2XV9RT4KP8ZW", true},
		"a Mapbox secret token":              {"sk" + ".eyJ1IjoibWFkZS11cCJ9." + random, true},
		"a line of an env file":              {"SUPABASE_SECRET_KEY=" + "sb" + "_secret_" + random, true},
		"a quoted line of an env file":       {`STRIPE_KEY="` + "sk" + "_live_" + random + `"`, true},
		"a secret after a public key":        {"pk" + "_test_" + random + " sk" + "_live_" + random, true},
		"a comma between keys":               {"pk" + "_test_" + random + ",sk" + "_live_" + random, true},
		"a JSON value":                       {`{"key":"` + "sk" + "_live_" + random + `"}`, true},
		"a name and a colon":                 {"KEY:" + "sk" + "_live_" + random, true},
		"a token before an address":          {"ghp" + "_" + random + "@github.com", true},
		"a private key's header":             {"-----BEGIN" + " PRIVATE KEY-----", true},
		"an RSA private key's header":        {"-----BEGIN" + " RSA PRIVATE KEY-----" + random, true},
		"a PGP private key's header":         {"-----BEGIN" + " PGP PRIVATE KEY BLOCK-----" + random, true},
		"a Google key beside a secret":       {googleKey + " sk" + "_live_" + random, true},
		"a secret run onto a Google key":     {googleKey + "sk" + "_live_" + random, true},
		"a public key's env file line":       {"NEXT_PUBLIC_KEY=" + "pk" + "_test_" + random, false},
		"an anon token's env file line":      {"SUPABASE_ANON_KEY=" + token(`{"role":"anon"}`), false},
		"an address that starts a key":       {"sk" + "-support.team1@example.com", false},
		"a Supabase publishable key":         {"sb" + "_publishable_" + random, false},
		"a Stripe publishable key":           {"pk" + "_live_" + random, false},
		"a quoted publishable key":           {`"` + "pk" + "_live_" + random + `"`, false},
		"a Mapbox public token":              {"pk" + ".eyJ1IjoibWFkZS11cCJ9." + random, false},
		"an anon token":                      {token(`{"role":"anon"}`), false},
		"a short part number":                {"sk" + "-1234", false},
		"a word":                             {"xoxo", false},
		"random text of no known kind":       {random, false},
		"a domain that starts like a token":  {"sk.example.com", false},
		"a name that starts like a key":      {"sk" + "_test_handler_for_the_checkout_session_page", false},
		"the word that starts a PEM block":   {"-----BEGIN", false},
		"a public key's PEM block":           {"-----BEGIN" + " PUBLIC KEY-----" + random + "-----END PUBLIC KEY-----", false},
		"a certificate's PEM block":          {"-----BEGIN" + " CERTIFICATE-----" + random + "-----END CERTIFICATE-----", false},
		"a PGP public key's block":           {"-----BEGIN" + " PGP PUBLIC KEY BLOCK-----" + random, false},
		"a Google key":                       {googleKey, false},
		"a Google key on a public key":       {"pk" + "_test_" + random + googleKey, false},
		"a secret's start inside task-":      {"task-" + random + random, false},
		"a secret's start inside work_":      {"work_test_" + random, false},
		"a secret's start with no key after": {"pk" + "_test_" + random + "sk" + "_x", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := HoldsSecretKey(c.value); got != c.want {
				t.Errorf("HoldsSecretKey(a value of %d characters) = %v, want %v", len(c.value), got, c.want)
			}
		})
	}
}

// A key run straight onto another has no word boundary before it, which is what
// happens when he pastes over a field without clearing it. Every kind of secret
// is found there, on every kind of key that can come first.
func TestSecretKeyRunStraightOntoAnotherKeyIsFound(t *testing.T) {
	random := "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
	leadingKeys := map[string]string{
		"a Stripe key":            "pk" + "_test_" + random,
		"a Supabase key":          "sb" + "_publishable_" + random,
		"a Mapbox token":          "pk" + ".eyJ1IjoibWFkZS11cCJ9." + random,
		"an anon token":           token(`{"role":"anon"}`),
		"a Google key":            exposed("AI"+"za", 35),
		"a key of no listed kind": "shp" + "at_" + random,
	}
	secrets := map[string]string{
		"a Stripe secret key":       "sk" + "_live_" + random,
		"a Stripe restricted key":   "rk" + "_test_" + random,
		"a webhook signing secret":  exposed("whsec"+"_", 32),
		"a GitHub token":            exposed("ghp"+"_", 36),
		"an Anthropic key":          exposed("sk"+"-ant-api03-", 90),
		"an OpenAI key":             exposed("sk"+"-proj-", 48),
		"a Resend key":              exposed("re"+"_", 32),
		"a Slack token":             exposed("xox"+"b-", 40),
		"an AWS access key ID":      "AKIA" + "Q7LM2XV9RT4KP8ZW",
		"a Supabase secret key":     exposed("sb"+"_secret_", 32),
		"a Supabase key of letters": "sb" + "_secret_" + "ZkQxLmVbNcRtWyHsJdFgA",
		"a GitLab token":            exposed("gl"+"pat-", 20),
		"an npm token":              exposed("npm"+"_", 36),
		"a Hugging Face token":      exposed("hf"+"_", 34),
		"a Mapbox secret token":     "sk" + ".eyJ1IjoibWFkZS11cCJ9." + random,
		"a service role token":      token(`{"role":"service_role"}`),
		"a private key's header":    "-----BEGIN" + " PRIVATE KEY-----",
		"a token with no role":      token(`{"iss":"made-up"}`),
	}
	for leadingName, leadingKey := range leadingKeys {
		for secretName, secret := range secrets {
			t.Run(secretName+" on "+leadingName, func(t *testing.T) {
				for _, value := range []string{leadingKey + secret, "MADE_UP_KEY=" + leadingKey + secret, `"` + leadingKey + secret + `"`} {
					if !HoldsSecretKey(value) {
						t.Errorf("HoldsSecretKey(a value of %d characters) = false, want the secret found", len(value))
					}
				}
			})
		}
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
