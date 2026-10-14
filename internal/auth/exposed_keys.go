package auth

import (
	"regexp"
	"strings"
)

// exposedKind is a secret key whose whole shape is certain enough to find in
// text: a pattern whose first group, when it has one, is the key's
// generated part, which must mix letters and digits, so a name such as
// sk_test_handler_for_checkout is no key. A publishable key is meant to be
// seen, so it is not among them.
type exposedKind struct {
	kind    string
	pattern *regexp.Regexp
}

// exposedKinds are checked in this order, the most specific first, and a
// key one of them found is not counted again by a later, wider one.
var exposedKinds = []exposedKind{
	{"a Stripe secret key", regexp.MustCompile(`\b[sr]k_(?:test|live)_([A-Za-z0-9]{20,})`)},
	{"a Stripe webhook signing secret", regexp.MustCompile(`\bwhsec_([A-Za-z0-9+/]{20,})`)},
	{"a GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_([A-Za-z0-9]{30,})|github_pat_([A-Za-z0-9_]{40,}))`)},
	{"an Anthropic key", regexp.MustCompile(`\bsk-ant-([A-Za-z0-9_-]{30,})`)},
	{"an OpenRouter key", regexp.MustCompile(`\bsk-or-v1-([A-Za-z0-9]{30,})`)},
	{"an OpenAI key", regexp.MustCompile(`\bsk-(?:proj-|svcacct-|admin-)?([A-Za-z0-9_-]{30,})`)},
	{"a Resend key", regexp.MustCompile(`\bre_([A-Za-z0-9_]{20,})`)},
	{"a Slack token", regexp.MustCompile(`\bxox[abprs]-([A-Za-z0-9-]{20,})`)},
	{"an AWS access key ID", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{"a Google API key", regexp.MustCompile(`\bAIza([0-9A-Za-z_-]{35})`)},
	{"a Supabase key", regexp.MustCompile(`\b(?:sbp_([a-f0-9]{40})|sb_secret_([A-Za-z0-9_-]{20,}))`)},
	{"a GitLab token", regexp.MustCompile(`\bglpat-([A-Za-z0-9_-]{20,})`)},
	{"an npm token", regexp.MustCompile(`\bnpm_([A-Za-z0-9]{36})`)},
	{"a Hugging Face token", regexp.MustCompile(`\bhf_([A-Za-z0-9]{30,})`)},
	{"a private key", regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )*PRIVATE KEY-----`)},
}

// ExposedKeys names the kinds of secret key text holds, each once, in a
// fixed order, such as "a Stripe secret key". It never returns any of the
// text, so what it says can go where the text must not.
func ExposedKeys(text string) []string {
	var kinds []string
	found := map[[2]int]bool{}
	for _, kind := range exposedKinds {
		isFound := false
		for _, match := range kind.pattern.FindAllStringSubmatchIndex(text, -1) {
			at := [2]int{match[0], match[1]}
			if found[at] || !generated(text, match) {
				continue
			}
			found[at], isFound = true, true
		}
		if isFound {
			kinds = append(kinds, kind.kind)
		}
	}
	return kinds
}

// generated reports whether the generated part a match captured mixes
// letters and digits, as a key does and a name seldom does. A match that
// captured nothing, such as a private key's header, is a key by its shape.
func generated(text string, match []int) bool {
	for group := 2; group+1 < len(match); group += 2 {
		if match[group] < 0 {
			continue
		}
		part := text[match[group]:match[group+1]]
		return strings.ContainsAny(part, "0123456789") && strings.ContainsFunc(part, func(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' })
	}
	return true
}
