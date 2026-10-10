package projectcheck

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

// The reasons a value of an env file is production's, worst first.
const (
	liveKey         = "a live key"
	namesProduction = "names production"
	remoteHost      = "a remote host"
	carriesLogin    = "an address that carries a login"
	realCredential  = "a credential"
	namedCredential = "a credential by its name"
)

// The reasons a value the rules do not count is named all the same, so a
// reader sees what a count left out.
const (
	placeholderValue = "a placeholder under a credential's name"
	trackedPlain     = "plain text under a credential's name, in a file git tracks"
	plainKey         = "plain text under the name of a key or a token, which a service issues and no person types"
	randomValue      = "long random text under a name the rules take for no credential"
	otherHost        = "an address on another host whose name says no data store"
)

// passedOverReasons are those reasons in the order a line names them.
var passedOverReasons = []string{placeholderValue, trackedPlain, plainKey, randomValue, otherHost}

// typedWords are the words in a variable's name for a credential a person
// types, which needs no shape to be one: a password, a passphrase or a
// shared secret. A key and a token are issued by a service, so plain text
// under one of those names is a setting.
var typedWords = map[string]bool{"PASSWORD": true, "PASS": true, "PASSWD": true, "PWD": true, "PASSPHRASE": true, "PASSCODE": true, "SECRET": true, "CODE": true, "CODES": true}

// isTyped reports whether a variable named like a credential is named for
// one a person types.
func isTyped(upper string) bool {
	for _, word := range strings.Split(upper, "_") {
		if typedWords[word] {
			return true
		}
	}
	return false
}

// environmentSwitch reports whether a variable says which environment a
// program runs as: its name ends in the word ENV, ENVIRONMENT, MODE or
// STAGE, as NODE_ENV, PLAID_ENV and STRIPE_MODE do.
func environmentSwitch(upper string) bool {
	parts := strings.Split(upper, "_")
	switch parts[len(parts)-1] {
	case "ENV", "ENVIRONMENT", "MODE", "STAGE":
		return true
	}
	return false
}

// serviceWords are the words in a variable's name that make its URL a data
// store, a queue or a place reports go, where a test that connects writes.
var serviceWords = map[string]bool{"DATABASE": true, "DB": true, "POSTGRES": true, "PG": true, "MYSQL": true, "MONGO": true, "REDIS": true, "VALKEY": true, "QDRANT": true, "ELASTIC": true, "SUPABASE": true, "SENTRY": true, "DSN": true, "BROKER": true, "AMQP": true, "RABBIT": true, "KAFKA": true, "WEBHOOK": true, "S3": true, "STORAGE": true, "UPSTASH": true, "CELERY": true}

// judgement is what the rules make of one variable of an env file. It holds
// reasons and never any part of the value.
type judgement struct {
	// production says why the value is production's, empty when it is not.
	production string
	// passedOver says why a value that is not counted is named all the same,
	// empty for a value that needs no second look.
	passedOver string
	// isPublicStore marks the address of a store on another host that is
	// filed under a public name: no credential alone, and the place a key of
	// the same service opens.
	isPublicStore bool
}

// judge says why a variable of an env file is production's: a live secret
// key, an environment switch that says production, a store or sink on a
// host that is not this machine, an address that carries a login, or a
// credential that is neither empty, a test key, a publishable key nor a
// placeholder. For a value it does not count it says why a reader may want
// to look, where there is a reason. Tracked says the file is one git
// tracks: plain text under a credential's name is a credential in a file
// only this machine holds, and a default git already publishes in one it
// tracks.
func judge(name, value string, tracked bool) judgement {
	upper := strings.ToUpper(name)
	lower := strings.ToLower(value)
	switch {
	case value == "":
		return judgement{}
	case strings.HasPrefix(value, "sk_live_"), strings.HasPrefix(value, "rk_live_"):
		return judgement{production: liveKey}
	case strings.HasPrefix(value, "sk_test_"), strings.HasPrefix(value, "rk_test_"):
		return judgement{}
	case environmentSwitch(upper):
		if lower == "production" || lower == "prod" || lower == "live" {
			return judgement{production: namesProduction}
		}
		return judgement{}
	case strings.Contains(value, "://"):
		switch public, store, login := publishableName(name), namesStore(upper), carriesALogin(value); {
		case localHost(value):
			return judgement{}
		case public && login:
			return judgement{production: carriesLogin}
		case public:
			return judgement{isPublicStore: store}
		case store:
			return judgement{production: remoteHost}
		case login:
			return judgement{production: carriesLogin}
		}
		return judgement{passedOver: otherHost}
	case publishable(name, value):
		return judgement{}
	}
	named, shaped := credentialName(name), auth.SecretShape(strings.ReplaceAll(value, "=", "")) != ""
	switch {
	case startsSecret(value), named && shaped:
		return judgement{production: realCredential}
	case named && isPlaceholder(value):
		return judgement{passedOver: placeholderValue}
	case named && tracked:
		return judgement{passedOver: trackedPlain}
	case named && isTyped(upper):
		return judgement{production: namedCredential}
	case named:
		return judgement{passedOver: plainKey}
	case shaped:
		return judgement{passedOver: randomValue}
	}
	return judgement{}
}

// namesStore reports whether a word of a variable's name makes its address
// a data store, a queue or a place reports go.
func namesStore(upper string) bool {
	return len(storeWords(upper)) > 0
}

// storeWords returns the words of a variable's name that name a service.
func storeWords(upper string) []string {
	var words []string
	for _, word := range strings.Split(upper, "_") {
		if serviceWords[word] {
			words = append(words, word)
		}
	}
	return words
}

// loginQueries are the query parameters an address carries a credential in.
var loginQueries = map[string]bool{"token": true, "access_token": true, "key": true, "apikey": true, "api_key": true, "secret": true, "password": true, "sig": true, "signature": true}

// carriesALogin reports whether an address carries a login: a password in
// front of its host, or a credential in its query.
func carriesALogin(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if _, has := parsed.User.Password(); has {
		return true
	}
	for name, values := range parsed.Query() {
		if loginQueries[strings.ToLower(name)] && slices.ContainsFunc(values, func(held string) bool { return held != "" }) {
			return true
		}
	}
	return false
}

// startsSecret reports whether a value starts the way a secret key does,
// which makes it one whatever variable holds it.
func startsSecret(value string) bool {
	for _, prefix := range secretPrefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// placeholderWords mark a value written to be replaced, and
// placeholderValues are the whole values that stand for none: a word a
// template or a local default ships with.
var (
	placeholderWords  = []string{"your", "example", "changeme", "change-me", "change_me", "placeholder", "replace", "todo", "dummy", "xxx", "<", "..."}
	placeholderValues = map[string]bool{
		"test": true, "testing": true, "secret": true, "password": true, "pass": true, "none": true, "null": true, "nil": true,
		"false": true, "true": true, "tbd": true, "n/a": true, "dev": true, "development": true, "local": true, "localhost": true,
		"demo": true, "fake": true, "mock": true, "admin": true, "postgres": true, "root": true, "guest": true, "user": true,
	}
)

// isPlaceholder reports whether a value reads as one written to be
// replaced or as a local default, which is no credential of anything.
func isPlaceholder(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if placeholderValues[lower] {
		return true
	}
	for _, word := range placeholderWords {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// publishablePrefixes start a key a service issues for browsers: Stripe's
// and Supabase's publishable keys and Mapbox's public token.
var publishablePrefixes = []string{"pk_", "pk.", "sb_publishable_"}

// secretPrefixes start a key that is secret whatever variable holds it.
var secretPrefixes = []string{"sk_", "rk_", "sk-", "whsec_", "sb_secret_", "sbp_", "ghp_", "github_pat_", "glpat-", "-----BEGIN"}

// bundledPrefixes start the name of a variable a bundler ships to the
// browser by its own rule, whatever the rest of the name says.
var bundledPrefixes = []string{"VITE_", "REACT_APP_", "GATSBY_"}

// publishable reports whether a variable holds a key every browser is handed
// by design, which is no credential. The value is asked first, since it can
// say what a name cannot: a key that starts the way a secret one does, or
// whose own payload names a role other than anon, is a secret filed under a
// publishable name. Where the value says nothing, the name decides.
func publishable(name, value string) bool {
	switch role := tokenRole(value); {
	case role == "anon":
		return true
	case role != "":
		return false
	}
	for _, prefix := range secretPrefixes {
		if strings.HasPrefix(value, prefix) {
			return false
		}
	}
	for _, prefix := range publishablePrefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return publishableName(name)
}

// publishableName reports whether a variable is named as a key handed to
// browsers: public or publishable by a word of its name, an anon key, or
// under a prefix a bundler ships to the browser.
func publishableName(name string) bool {
	upper := strings.ToUpper(name)
	for _, prefix := range bundledPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return strings.Contains(upper, "PUBLISHABLE") || strings.Contains(upper, "PUBLIC") || slices.Contains(strings.Split(upper, "_"), "ANON")
}

// tokenRole returns the role a JSON Web Token's payload names, as a Supabase
// key's does, or "" for a value that is no such token. Nothing else of the
// value is read.
func tokenRole(value string) string {
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

// localHost reports whether a URL names this machine or a service beside
// it: localhost, a loopback address, a name with no dot in it such as a
// compose service, or a name under a suffix kept for local use.
func localHost(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case host == "", host == "localhost", host == "127.0.0.1", host == "::1", host == "0.0.0.0", host == "host.docker.internal":
		return true
	case !strings.Contains(host, "."):
		return true
	}
	for _, suffix := range []string{".local", ".localhost", ".test"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}
