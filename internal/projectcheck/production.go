package projectcheck

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// The reasons a value of an env file is production's, worst first.
const (
	liveKey         = "a live key"
	namesProduction = "names production"
	remoteHost      = "a remote host"
	realCredential  = "a credential"
)

// environmentSelectors are the variables that say which environment a
// program runs as.
var environmentSelectors = map[string]bool{"APP_ENV": true, "ENV": true, "ENVIRONMENT": true, "NODE_ENV": true, "RAILS_ENV": true, "FLASK_ENV": true, "DJANGO_ENV": true, "SENTRY_ENVIRONMENT": true}

// serviceWords are the words in a variable's name that make its URL a data
// store, a queue or a place reports go, where a test that connects writes.
var serviceWords = map[string]bool{"DATABASE": true, "DB": true, "POSTGRES": true, "PG": true, "MYSQL": true, "MONGO": true, "REDIS": true, "VALKEY": true, "QDRANT": true, "ELASTIC": true, "SUPABASE": true, "SENTRY": true, "DSN": true, "BROKER": true, "AMQP": true, "RABBIT": true, "KAFKA": true, "WEBHOOK": true, "S3": true, "STORAGE": true, "UPSTASH": true, "CELERY": true}

// productionValue says why a variable of an env file is production's, or
// returns "" when it is not: a live secret key, an environment selector that
// says production, a store or sink on a host that is not this machine, or a
// credential that is neither empty, a test key, a publishable key nor a
// placeholder. It never returns any part of the value.
func productionValue(name, value string) string {
	upper := strings.ToUpper(name)
	lower := strings.ToLower(value)
	switch {
	case value == "":
		return ""
	case strings.HasPrefix(value, "sk_live_"), strings.HasPrefix(value, "rk_live_"):
		return liveKey
	case strings.HasPrefix(value, "sk_test_"), strings.HasPrefix(value, "rk_test_"):
		return ""
	case publishable(name, value):
		return ""
	case environmentSelectors[upper]:
		if lower == "production" || lower == "prod" || lower == "live" {
			return namesProduction
		}
		return ""
	}
	if strings.Contains(value, "://") {
		for _, word := range strings.Split(upper, "_") {
			if serviceWords[word] && !localHost(value) {
				return remoteHost
			}
		}
	}
	if credentialName(name) && auth.SecretShape(strings.ReplaceAll(value, "=", "")) != "" && !strings.Contains(value, "://") {
		return realCredential
	}
	return ""
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

// testSetupPrefixes start the name of a file a test runner loads before the
// tests, where a project pins what its tests may see.
var testSetupPrefixes = []string{"jest.setup.", "vitest.setup.", "setupTests.", "vitest.config.", "jest.config.", "playwright.config.", "global-setup.", "globalSetup."}

// isTestSetup reports whether a tracked file is one a test runner loads
// before the tests.
func isTestSetup(name string) bool {
	base := path.Base(name)
	switch base {
	case "conftest.py", "pytest.ini", "tox.ini", ".env.test":
		return true
	}
	for _, prefix := range testSetupPrefixes {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}

// productionReach checks what a test run in a goblin's worktree starts with:
// the env files the worktree shares from the checkout and those the
// repository tracks. A production value in one is within a test's reach
// unless the test setup, or the gate's own test command, names the variable,
// which is how a project pins it. With no test command of the repository's
// own an agent chooses what the test step runs, and nothing stands between
// it and the file.
func (c *checker) productionReach(ctx context.Context, test string) {
	type envFile struct{ name, how string }
	var files []envFile
	if manifest, err := worktree.Resolve(c.DataDir, c.project); err == nil {
		how := "which every goblin's worktree shares by " + worktree.ManifestFileName
		for _, name := range manifest.Link {
			if !c.repo.tracked[name] {
				files = append(files, envFile{name, how})
			}
		}
	}
	for _, name := range sortedKeys(c.repo.tracked) {
		if isEnvFile(path.Base(name)) && !isExample(path.Base(name)) {
			files = append(files, envFile{name, "which the repository tracks"})
		}
	}

	// The files that pin: the test setup, and the scripts of the gate's own
	// test command.
	setup := map[string]string{}
	for _, name := range sortedKeys(c.repo.tracked) {
		if isTestSetup(name) {
			if data, ok := c.repo.read(ctx, name); ok {
				setup[name] = string(data)
			}
		}
	}
	for _, s := range segments(test) {
		for _, arg := range s.argv {
			if scriptExtensions[strings.ToLower(path.Ext(arg))] {
				if data, ok := c.repo.read(ctx, path.Join(s.dir, arg)); ok {
					setup[path.Join(s.dir, arg)] = string(data)
				}
			}
		}
	}
	pinnedBy := func(variable string) string {
		word := regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(variable) + `([^A-Za-z0-9_]|$)`)
		for _, name := range sortedKeys(setup) {
			if word.MatchString(setup[name]) {
				return name
			}
		}
		return ""
	}

	variables, production, pinned := 0, 0, 0
	pinners := map[string]bool{}
	var read, exposed []string
	worst := false
	for _, file := range files {
		var data []byte
		if c.repo.tracked[file.name] {
			data, _ = c.repo.read(ctx, file.name)
		} else {
			var err error
			if data, err = fsx.ReadFile(filepath.Join(c.Checkout, filepath.FromSlash(file.name))); err != nil {
				continue
			}
		}
		values, err := auth.ParseEnv(bytes.NewReader(data))
		if err != nil {
			continue
		}
		read = append(read, file.name+", "+file.how)
		variables += len(values)
		var open []string
		for _, variable := range sortedKeys(values) {
			reason := productionValue(variable, values[variable])
			if reason == "" {
				continue
			}
			production++
			if by := pinnedBy(variable); by != "" {
				pinned++
				pinners[by] = true
				continue
			}
			worst = worst || reason == liveKey || reason == namesProduction
			open = append(open, variable+" ("+reason+")")
		}
		if len(open) > 0 {
			exposed = append(exposed, file.name+", "+file.how+": "+strings.Join(open, ", "))
		}
	}

	step := fmt.Sprintf("The gate's test step is the repository's own command, %q", test)
	if test == "" {
		step = "The gate's test step is an agent's choice, since the gate names no test command"
	}
	pins := fmt.Sprintf("Named by the test setup and left out: %d", pinned)
	if len(pinners) > 0 {
		pins += " (" + strings.Join(sortedKeys(pinners), ", ") + ")"
	}
	if len(exposed) > 0 {
		severity := High
		if worst || test == "" {
			severity = Critical
		}
		c.add(AreaGate, "test-reaches-production", severity,
			"a test run in a goblin's worktree can read production: its env files hold "+count(production-pinned, "production value")+" the test setup does not name",
			strings.Join(exposed, ". ")+". "+pins+". A setup that clears variables by a rule and not by name is not seen here. "+step+". Read at "+c.repo.asRead(),
			"keep production out of what a worktree shares by naming a development env file, or none, as link in "+worktree.ManifestFileName+", or name each variable in the test setup")
	}
	evidence := "no env file is shared into a goblin's worktree or tracked"
	if len(read) > 0 {
		evidence = strings.Join(read, ". ")
	}
	c.add(AreaGate, "test-env-examined", OK,
		fmt.Sprintf("examined %s in %s a test run can read: %s, %d of them named by the test setup", count(variables, "variable"), count(len(read), "env file"), count(production, "production value"), pinned),
		evidence+". "+pins+". Test setup read at "+c.repo.asRead()+": "+orNone(sortedKeys(setup)), "")
}
