package auth

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"regexp"
	"slices"
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

// publishablePrefixes start a key a service issues for browsers: Stripe's
// and Supabase's publishable keys and Mapbox's public token.
var publishablePrefixes = []string{"pk_", "pk.", "sb_publishable_"}

// secretPrefixes start a key that is secret whatever variable holds it.
var secretPrefixes = []string{"sk_", "rk_", "sk-", "whsec_", "sb_secret_", "sbp_", "ghp_", "github_pat_", "glpat-", "-----BEGIN"}

// bundledPrefixes start the name of a variable a bundler ships to the
// browser by its own rule, whatever the rest of the name says.
var bundledPrefixes = []string{"VITE_", "REACT_APP_", "GATSBY_"}

// keyShaped matches values whose whole shape is a key: an AWS access key ID,
// and a UUID, which several services issue as their API key.
var keyShaped = []*regexp.Regexp{
	regexp.MustCompile(`^(AKIA|ASIA)[A-Z0-9]{16}$`),
	regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`),
}

// wordEdges is the punctuation prose puts around a word.
const wordEdges = "\"'`()[]{}<>,;:!?."

// SecretShape says why text looks like a credential value rather than a name
// or a word of prose, or returns "" when it does not. A request for a
// credential takes names only, so anything shaped like the credential itself
// is refused before it can reach the board's records. The reason never
// repeats the text.
func SecretShape(text string) string {
	token := strings.Trim(text, wordEdges)
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

// IsKeyValue says a word of prose is a credential value by its shape alone:
// it starts the way a known kind of key does, is long enough to be one and
// holds a digit, or it has an AWS access key ID's shape. Unlike SecretShape it
// passes the commit hashes, run ids and assignments that text about code
// carries, so it can screen a whole issue word by word.
func IsKeyValue(token string) bool {
	token = strings.Trim(token, wordEdges)
	return hasValuePrefix(token) && len(token) >= 20 && strings.ContainsAny(token, "0123456789") || keyShaped[0].MatchString(token)
}

// supabaseSecret matches a Supabase secret key by its start and a key's length
// after it. Unlike exposedKinds it needs no digit, since a key of letters alone
// is no less secret and that start says what it is.
var supabaseSecret = regexp.MustCompile(`sb_secret_[A-Za-z0-9_-]{20,}`)

// webToken matches the header and payload of a JSON Web Token anywhere in text.
// It stops before the signature, so a token run straight onto another is found.
var webToken = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.`)

// keyRun matches a stretch of text written in the characters of a key.
var keyRun = regexp.MustCompile(`[A-Za-z0-9_.-]+`)

// HoldsSecretKey says a value typed into a plain field holds a secret key
// anywhere in it, which no name and no word of his makes safe to send: a key
// ExposedKeys finds, a Supabase secret key, a JSON Web Token whose role is not
// anon, or a key of those kinds run straight onto a public key, with nothing
// between them to mark a word's end. A Google API key is left out, since a key
// for browsers looks the same, and a value that only starts the way a secret
// key does is no key of these kinds.
func HoldsSecretKey(value string) bool {
	return holdsSecretKind(value, exposedKinds) || supabaseSecret.MatchString(value) ||
		slices.ContainsFunc(webToken.FindAllString(value, -1), func(token string) bool { return TokenRole(token) != "anon" }) ||
		slices.ContainsFunc(keyRun.FindAllString(value, -1), func(run string) bool {
			return (StartsPublishable(run) || strings.HasPrefix(run, "eyJ")) && holdsSecretKind(run, runOnKinds)
		})
}

// bareKey matches a value that is one key and nothing else.
var bareKey = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// PlainPublicKey says a value typed into a plain field is a key made for
// browsers, so it is no secret: the field is named as public and the whole
// value says so too, by a publishable key's start or an anon token's role.
// Neither alone is enough: a public name holds whatever is pasted into it, and
// a service's personal token can start the way a publishable key does. A value
// that holds a secret key's start anywhere in it, as two keys pasted together
// do, is not one key.
func PlainPublicKey(name, value string) bool {
	holdsSecret := slices.ContainsFunc(secretPrefixes, func(prefix string) bool { return strings.Contains(value, prefix) })
	role := TokenRole(value)
	saysPublic := role == "anon" && strings.HasPrefix(value, "eyJ") || role == "" && StartsPublishable(value)
	return PublishableName(name) && bareKey.MatchString(value) && !holdsSecret && saysPublic
}

// StartsSecret says a value starts the way a secret key does, which makes it
// one whatever variable holds it.
func StartsSecret(value string) bool {
	return startsWithAny(value, secretPrefixes)
}

// StartsPublishable says a value starts the way a key made for browsers does.
func StartsPublishable(value string) bool {
	return startsWithAny(value, publishablePrefixes)
}

// PublishableName says a variable is named as a key handed to browsers:
// public or publishable by a word of its name, an anon key, or under a prefix
// a bundler ships to the browser.
func PublishableName(name string) bool {
	upper := strings.ToUpper(name)
	return startsWithAny(upper, bundledPrefixes) || strings.Contains(upper, "PUBLISHABLE") || strings.Contains(upper, "PUBLIC") || slices.Contains(strings.Split(upper, "_"), "ANON")
}

// TokenRole returns the role a JSON Web Token's payload names, as a Supabase
// key's does, or "" for a value that is no such token. Nothing else of the
// value is read.
func TokenRole(value string) string {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Role string `json:"role"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Role
}

// plainIdentifier matches the identifiers a service shows openly: at most 20
// letters and digits.
var plainIdentifier = regexp.MustCompile(`^[A-Za-z0-9]{1,20}$`)

// PlainIdentifier says a value typed into a plain field is a public
// identifier, such as an OAuth client ID, and no key: the field is named as an
// ID, and the value is at most 20 letters and digits that neither start the
// way a known kind of key does nor have the whole shape of one. A longer value
// in an ID field, or one with a key's prefix, still reads as a key.
func PlainIdentifier(name, value string) bool {
	name = strings.ToUpper(name)
	isIDField := name == "ID" || strings.HasSuffix(name, "_ID")
	return isIDField && plainIdentifier.MatchString(value) && !hasValuePrefix(value) && !keyShaped[0].MatchString(value) && !keyShaped[1].MatchString(value)
}

func hasValuePrefix(token string) bool {
	return startsWithAny(token, valuePrefixes)
}

func startsWithAny(text string, prefixes []string) bool {
	return slices.ContainsFunc(prefixes, func(prefix string) bool { return strings.HasPrefix(text, prefix) })
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
