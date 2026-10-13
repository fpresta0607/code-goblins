package auth

import (
	"strings"
	"testing"
)

func TestKnownKeyKindsRefuseMaskedAndWhitespaceValuesWithoutRepeatingThem(t *testing.T) {
	for _, kind := range []struct{ name, prefix string }{
		{"STRIPE_TEST_SECRET_KEY", "sk_test_"},
		{"STRIPE_TEST_PUBLISHABLE_KEY", "pk_test_"},
		{"STRIPE_WEBHOOK_SECRET", "whsec_"},
		{"GITHUB_TOKEN", "github_pat_"},
		{"OPENROUTER_API_KEY", "sk-or-"},
		{"ANTHROPIC_API_KEY", "sk-ant-"},
		{"OPENAI_API_KEY", "sk-"},
		{"RESEND_API_KEY", "re_"},
	} {
		for _, mask := range []struct{ name, value string }{
			{"dots", "..."}, {"ellipsis", "…"}, {"bullets", "•••"}, {"asterisks", "***"},
			{"space", " "}, {"newline", "\n"}, {"tab", "\t"}, {"nonbreaking space", "\u00a0"},
		} {
			t.Run(kind.name+"/"+mask.name, func(t *testing.T) {
				value := kind.prefix + strings.Repeat("x", 24) + mask.value + "1234"

				problem := ValueProblem(kind.name, value)

				if problem == "" {
					t.Fatal("a masked or whitespace-containing key was accepted")
				}
				if strings.Contains(problem, value) {
					t.Fatal("the refusal repeated the key")
				}
			})
		}
	}
}

func TestUnclassifiedCredentialsKeepTheirOwnPunctuation(t *testing.T) {
	for name, value := range map[string]string{
		"DATABASE_URL": "postgres://app@db.internal:5432/app",
		"APPLICATION_SECRET": "a.password.with.punctuation",
		"GOOGLE_PRIVATE_KEY": "-----BEGIN PRIVATE KEY-----\nfixture\n-----END PRIVATE KEY-----",
	} {
		if problem := ValueProblem(name, value); problem != "" {
			t.Errorf("%s refused its punctuation: %s", name, problem)
		}
	}
}
