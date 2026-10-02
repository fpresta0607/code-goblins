package auth

import (
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
