package auth

import (
	"strings"
	"testing"
)

// A stored value is checked for its shape before anything says it is
// stored: the Overlord's STRIPE_TEST_SECRET_KEY arrived as one character, a
// Ctrl+V with nothing to paste, and still showed stored.
func TestValueProblemRefusesWhatCannotBeTheCredentialItsNameNames(t *testing.T) {
	random := "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
	for _, test := range []struct {
		name, credential, value, says string
	}{
		{"the Ctrl+V a paste with no text types", "STRIPE_TEST_SECRET_KEY", "\x16", "control character"},
		{"an escape inside a value", "RESEND_API_KEY", "re_" + random + "\x1b[201~", "control character"},
		{"nothing but spaces", "STRIPE_TEST_SECRET_KEY", "   ", "is empty"},
		{"one character for a test secret key", "STRIPE_TEST_SECRET_KEY", "s", "does not start with sk_test_ or rk_test_"},
		{"a live key where a test key is named", "STRIPE_TEST_SECRET_KEY", "sk" + "_live_" + random, "not a Stripe test secret key"},
		{"a publishable key where a secret key is named", "STRIPE_SECRET_KEY", "pk" + "_test_" + random, "does not start with sk_ or rk_"},
		{"a secret key where a publishable key is named", "STRIPE_TEST_PUBLISHABLE_KEY", "sk" + "_test_" + random, "does not start with pk_test_"},
		{"a cut-off test secret key", "STRIPE_TEST_SECRET_KEY", "sk" + "_test_" + "4eC3", "too short"},
		{"a webhook secret of the wrong kind", "STRIPE_WEBHOOK_SECRET", "sk" + "_test_" + random, "does not start with whsec_"},
		{"a GitHub token of the wrong kind", "GITHUB_TOKEN", random, "not a GitHub token"},
		{"an OpenAI key under OpenRouter's name", "OPENROUTER_API_KEY", "sk" + "-proj-" + random, "does not start with sk-or-"},
		{"a short key of no known kind", "QDRANT_API_KEY", "abc123", "too short to be a whole key"},
		{"a short token of no known kind", "FLY_API_TOKEN", "abc123", "too short to be a whole token"},
		{"one character of anything", "UPSTASH_EMAIL", "a", "too short to be a whole credential"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			problem := ValueProblem(test.credential, test.value)

			// Assert
			if !strings.Contains(problem, test.says) || !strings.HasPrefix(problem, test.credential+" ") {
				t.Fatalf("ValueProblem(%s) = %q, want it to name the credential and say %q", test.credential, problem, test.says)
			}
			if strings.TrimSpace(test.value) != "" && len(test.value) > 1 && strings.Contains(problem, strings.TrimSpace(test.value)) {
				t.Fatal("the reason repeats the value")
			}
		})
	}
}

func TestValueProblemTakesWhatCanBeTheCredentialItsNameNames(t *testing.T) {
	random := "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
	for credential, value := range map[string]string{
		"STRIPE_TEST_SECRET_KEY":      "sk" + "_test_" + random,
		"STRIPE_TEST_RESTRICTED_KEY":  "rk" + "_test_" + random,
		"STRIPE_SECRET_KEY":           "rk" + "_live_" + random,
		"STRIPE_TEST_PUBLISHABLE_KEY": "pk" + "_test_" + random,
		"STRIPE_WEBHOOK_SECRET":       "whsec" + "_" + random,
		"GITHUB_TOKEN":                "github" + "_pat_" + random,
		"GH_TOKEN":                    "ghp" + "_" + random,
		"OPENROUTER_API_KEY":          "sk" + "-or-v1-" + random,
		"ANTHROPIC_API_KEY":           "sk" + "-ant-api03-" + random,
		"OPENAI_API_KEY":              "sk" + "-proj-" + random,
		"RESEND_API_KEY":              "re" + "_" + random,
		"QDRANT_API_KEY":              random,
		"DATABASE_URL":                "postgres://app@db.internal:5432/app",
		"UPSTASH_EMAIL":               "ops@example.com",
		"GITHUB_APP_ID":               "123456",
		"GOOGLE_PRIVATE_KEY":          "-----BEGIN PRIVATE KEY-----\n" + random + "\n-----END PRIVATE KEY-----",
	} {
		if problem := ValueProblem(credential, value); problem != "" {
			t.Errorf("ValueProblem(%s) = %q, want a plausible value taken", credential, problem)
		}
	}
}
