package auth

import (
	"math"
	"regexp"
	"strings"
)

// valuePrefixes are how the keys goblins are asked for begin: Stripe, GitHub,
// Slack, JSON Web Tokens, OpenAI and Anthropic, Google, GitLab, npm, Hugging
// Face, Resend, Supabase, DigitalOcean, Shopify, Linear, SendGrid, and PEM.
var valuePrefixes = []string{
	"sk_", "rk_", "pk_", "whsec_",
	"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_",
	"xox", "xapp-",
	"eyJ",
	"sk-",
	"AIza", "ya29.", "GOCSPX-",
	"glpat-", "npm_", "hf_", "re_", "sbp_", "sb_secret_", "sb_publishable_",
	"dop_v1_", "shpat_", "lin_api_", "SG.",
	"-----BEGIN",
}

// keyShaped matches values whose whole shape is a key: an AWS access key ID,
// and a UUID, which several services issue as their API key.
var keyShaped = []*regexp.Regexp{
	regexp.MustCompile(`^(AKIA|ASIA)[A-Z0-9]{16}$`),
	regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`),
}

// SecretShape says why text looks like a credential value rather than a name
// or a word of prose, or returns "" when it does not. A request for a
// credential takes names only, so anything shaped like the credential itself
// is refused before it can reach the board's records. The reason never
// repeats the text.
func SecretShape(text string) string {
	token := strings.Trim(text, "\"'`()[]{}<>,;:!?.")
	switch {
	case strings.Contains(token, "="):
		return "it assigns a value with an equals sign"
	case hasValuePrefix(token):
		return "it starts the way a known kind of key does"
	case keyShaped[0].MatchString(token) || keyShaped[1].MatchString(token):
		return "it has the shape of a key"
	case hasRandomRun(token):
		return "it holds long random text"
	}
	return ""
}

func hasValuePrefix(token string) bool {
	for _, prefix := range valuePrefixes {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}

// hasRandomRun finds a run of base64 characters long and varied enough to be
// generated rather than written: 16 or more mixing letters and digits, or 24
// or more whose case changes as often as random text does. Words, snake_case
// names and CamelCase identifiers split or stay below both bars.
func hasRandomRun(token string) bool {
	runs := strings.FieldsFunc(token, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '/')
	})
	for _, run := range runs {
		var letters, digits, upper, lower, changes int
		for i, r := range run {
			switch {
			case r >= '0' && r <= '9':
				digits++
			case r >= 'A' && r <= 'Z':
				letters++
				upper++
			case r >= 'a' && r <= 'z':
				letters++
				lower++
			}
			if i > 0 && isUpper(rune(run[i-1])) != isUpper(r) && isLetter(rune(run[i-1])) && isLetter(r) {
				changes++
			}
		}
		if len(run) >= 16 && letters > 0 && digits > 0 && entropy(run) >= 3.0 {
			return true
		}
		if len(run) >= 24 && upper > 0 && lower > 0 && changes*4 >= len(run) && entropy(run) >= 3.5 {
			return true
		}
	}
	return false
}

func isUpper(r rune) bool  { return r >= 'A' && r <= 'Z' }
func isLetter(r rune) bool { return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' }

// entropy is the Shannon entropy of text in bits per character.
func entropy(text string) float64 {
	counts := map[rune]int{}
	for _, r := range text {
		counts[r]++
	}
	bits := 0.0
	for _, count := range counts {
		p := float64(count) / float64(len(text))
		bits -= p * math.Log2(p)
	}
	return bits
}
