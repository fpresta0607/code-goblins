package auth

import (
	"slices"
	"strings"
	"unicode"
)

// keyKind is a kind of key a credential's name says it is: every word in
// words is among the name's underscore-separated words, at least one of any
// is when any is set, its values start with one of prefixes, and the
// shortest one has shortest characters.
type keyKind struct {
	words    []string
	any      []string
	kind     string
	prefixes []string
	shortest int
}

// keyKinds are the kinds whose start is certain, the most specific first.
var keyKinds = []keyKind{
	{words: []string{"STRIPE", "TEST", "PUBLISHABLE"}, kind: "Stripe test publishable key", prefixes: []string{"pk_test_"}, shortest: 20},
	{words: []string{"STRIPE", "LIVE", "PUBLISHABLE"}, kind: "Stripe live publishable key", prefixes: []string{"pk_live_"}, shortest: 20},
	{words: []string{"STRIPE", "PUBLISHABLE"}, kind: "Stripe publishable key", prefixes: []string{"pk_"}, shortest: 20},
	{words: []string{"STRIPE", "WEBHOOK"}, kind: "Stripe webhook signing secret", prefixes: []string{"whsec_"}, shortest: 20},
	{words: []string{"STRIPE", "TEST"}, any: []string{"SECRET", "RESTRICTED"}, kind: "Stripe test secret key", prefixes: []string{"sk_test_", "rk_test_"}, shortest: 20},
	{words: []string{"STRIPE", "LIVE"}, any: []string{"SECRET", "RESTRICTED"}, kind: "Stripe live secret key", prefixes: []string{"sk_live_", "rk_live_"}, shortest: 20},
	{words: []string{"STRIPE"}, any: []string{"SECRET", "RESTRICTED"}, kind: "Stripe secret key", prefixes: []string{"sk_", "rk_"}, shortest: 20},
	{any: []string{"GITHUB", "GH"}, kind: "GitHub token", prefixes: []string{"ghp_", "github_pat_", "gho_", "ghu_", "ghs_"}, shortest: 20},
	{words: []string{"OPENROUTER", "KEY"}, kind: "OpenRouter key", prefixes: []string{"sk-or-"}, shortest: 20},
	{words: []string{"ANTHROPIC", "KEY"}, kind: "Anthropic key", prefixes: []string{"sk-ant-"}, shortest: 20},
	{words: []string{"OPENAI", "KEY"}, kind: "OpenAI key", prefixes: []string{"sk-"}, shortest: 20},
	{words: []string{"RESEND", "KEY"}, kind: "Resend key", prefixes: []string{"re_"}, shortest: 20},
}

// The fewest characters a value of a name ending in KEY, SECRET or TOKEN
// has, and any other credential value.
const (
	shortestKey   = 8
	shortestValue = 4
)

func (k keyKind) names(words []string) bool {
	if k.any != nil && !slices.ContainsFunc(k.any, func(word string) bool { return slices.Contains(words, word) }) {
		return false
	}
	for _, word := range k.words {
		if !slices.Contains(words, word) {
			return false
		}
	}
	// A GitHub kind is a token: GITHUB_TOKEN, GH_TOKEN or GITHUB_PAT.
	return k.kind != "GitHub token" || slices.Contains(words, "TOKEN") || slices.Contains(words, "PAT")
}

// ValueProblem says why value cannot be the credential name names, or
// returns "" when it can: it is empty, it holds a control character other
// than a tab or a line break, it is shorter than any credential of its kind,
// or it does not start the way the kind of key its name names starts. The
// reason names the name and never repeats any of the value.
func ValueProblem(name, value string) string {
	if strings.TrimSpace(value) == "" {
		return name + " is empty"
	}
	if strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' }) {
		return name + " holds a control character, as a Ctrl+V with nothing to paste leaves"
	}
	words := strings.Split(strings.ToUpper(name), "_")
	for _, kind := range keyKinds {
		if !kind.names(words) {
			continue
		}
		if !slices.ContainsFunc(kind.prefixes, func(prefix string) bool { return strings.HasPrefix(value, prefix) }) {
			return name + " does not start with " + strings.Join(kind.prefixes, " or ") + ", so it is not a " + kind.kind
		}
		if len(value) < kind.shortest {
			return name + " is too short to be a whole " + kind.kind
		}
		return ""
	}
	switch last := words[len(words)-1]; {
	case (last == "KEY" || last == "SECRET" || last == "TOKEN") && len(value) < shortestKey:
		return name + " is too short to be a whole " + strings.ToLower(last)
	case len(value) < shortestValue:
		return name + " is too short to be a whole credential"
	}
	return ""
}
