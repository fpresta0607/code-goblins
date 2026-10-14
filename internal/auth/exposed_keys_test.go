package auth

import (
	"slices"
	"strings"
	"testing"
)

// exposed builds a value of a key's shape at run time, so no source file
// holds one: prefix and a random-looking run of length characters.
func exposed(prefix string, length int) string {
	const alphabet = "aZ3kQ9xW1mP7rT5vB2nY8cL4hJ6dF0gS"
	var body strings.Builder
	for i := 0; i < length; i++ {
		body.WriteByte(alphabet[(i*7+3)%len(alphabet)])
	}
	return prefix + body.String()
}

func TestExposedKeysNamesEachSecretKindAndNeverItsValue(t *testing.T) {
	for _, test := range []struct {
		kind, text string
	}{
		{"a Stripe secret key", "STRIPE_TEST_SECRET_KEY=" + exposed("sk"+"_test_", 24)},
		{"a Stripe secret key", "restricted " + exposed("rk"+"_live_", 99)},
		{"a Stripe webhook signing secret", exposed("whsec"+"_", 32)},
		{"a GitHub token", `token: "` + exposed("ghp"+"_", 36) + `"`},
		{"a GitHub token", exposed("github"+"_pat_", 82)},
		{"an Anthropic key", exposed("sk"+"-ant-api03-", 90)},
		{"an OpenRouter key", exposed("sk"+"-or-v1-", 64)},
		{"an OpenAI key", exposed("sk"+"-proj-", 48)},
		{"a Resend key", exposed("re"+"_", 32)},
		{"a Slack token", exposed("xox"+"b-", 40)},
		{"an AWS access key ID", "AKIA" + "IOSFODNN7EXAMPLE"},
		{"a Google API key", exposed("AI"+"za", 35)},
		{"a Supabase key", exposed("sb"+"_secret_", 32)},
		{"a GitLab token", exposed("gl"+"pat-", 20)},
		{"an npm token", exposed("npm"+"_", 36)},
		{"a Hugging Face token", exposed("hf"+"_", 34)},
		{"a private key", "-----BEGIN " + "RSA PRIVATE KEY-----\nMIIE..."},
	} {
		t.Run(test.kind, func(t *testing.T) {
			// Act
			kinds := ExposedKeys(test.text)

			// Assert
			if !slices.Contains(kinds, test.kind) {
				t.Fatalf("kinds = %q, want %q", kinds, test.kind)
			}
			for _, kind := range kinds {
				if strings.Contains(test.text, kind) {
					t.Fatal("a kind repeated the text it was found in")
				}
			}
		})
	}
}

func TestExposedKeysPassesWhatIsNoSecretKey(t *testing.T) {
	for _, text := range []string{
		"commit 9f67ba752f1e637b1d58a0e13e1c07a692648c2c",
		"id 0b3c9a3e-6a52-4b43-a1d4-6d2f7c0a9e11",
		"publishable " + exposed("pk"+"_test_", 24),
		"masked sk" + "_test_••••••••4abc and sk" + "_live_...9xyz",
		"func sk_test_handler_for_the_checkout_session_page()",
		"re_render_the_whole_board_when_anything_changed",
		"--sk-ant flag and sk-learn and sk-proj-name-here",
		"aGVsbG8gd29ybGQsIHRoaXMgaXMgYSBiYXNlNjQgYmxvYg==",
		"STRIPE_TEST_SECRET_KEY is stored for PrecisionDocs-AI",
		"",
	} {
		if kinds := ExposedKeys(text); len(kinds) != 0 {
			t.Errorf("ExposedKeys(%q) = %q, want none", text, kinds)
		}
	}
}

func TestExposedKeysNamesEachKindOnceInAFixedOrder(t *testing.T) {
	text := exposed("ghp"+"_", 36) + " " + exposed("sk"+"_test_", 24) + " " + exposed("sk"+"_live_", 30)

	kinds := ExposedKeys(text)

	if !slices.Equal(kinds, []string{"a Stripe secret key", "a GitHub token"}) {
		t.Fatalf("kinds = %q, want each kind once in the table's order", kinds)
	}
}
