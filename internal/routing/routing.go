// Package routing reads the fleet's standing policy for what to do when a
// goblin's harness starts erroring: which provider failures are recognized,
// and which of them the Supreme Overlord has already decided the answer to.
package routing

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the policy file under the CFO home's data directory.
const FileName = "routing.json"

// Fault names a kind of provider failure seen in a goblin's pane.
type Fault string

const (
	// RateLimit is the provider refusing on quota: the standing case the
	// Overlord has a rule for.
	RateLimit Fault = "rate-limit"
	// Auth is a rejected or expired credential.
	Auth Fault = "auth"
	// Provider is the service failing on its own side.
	Provider Fault = "provider"
)

// faultPatterns are matched against a pane's tail, lowercased. Order matters:
// a quota message that also carries a 403 is a rate limit, not an auth
// failure, so the rate-limit patterns are tried first.
//
// Every pattern is a phrase a provider actually emits, never a bare status
// code: "429" alone matches "line 429 of parser.go" and would report a
// healthy goblin as rate-limited.
//
// ponytail: substring matching over a pane tail, which cannot tell a
// provider's error from a project that happens to discuss one - a repo with
// RATE_LIMIT_PER_WINDOW in its output can trip the rate-limit rule. The
// consequence is bounded (a wake carrying a recommendation), so the ceiling
// is accepted; if false wakes become real, the upgrade is to match only
// within the harness's own error framing rather than the whole tail.
var faultPatterns = []struct {
	fault    Fault
	patterns []string
}{
	{RateLimit, []string{
		"rate limit", "rate_limit", "ratelimit_error", "too many requests",
		"quota exceeded", "insufficient quota", "over quota", "out of quota",
		"usage limit",
	}},
	{Auth, []string{
		"401 unauthorized", "invalid api key", "invalid_api_key",
		"authentication_error", "authentication failed", "api key not valid",
		"invalid x-api-key",
	}},
	{Provider, []string{
		"internal server error", "502 bad gateway", "503 service unavailable",
		"service unavailable", "overloaded_error", "upstream connect error",
	}},
}

// Detect reports the provider fault a pane's tail shows, if any. It reads the
// tail the watcher already captured rather than probing anything.
func Detect(paneTail string) (Fault, string, bool) {
	lowered := strings.ToLower(paneTail)
	for _, group := range faultPatterns {
		for _, pattern := range group.patterns {
			if index := strings.Index(lowered, pattern); index >= 0 {
				return group.fault, evidence(paneTail, index), true
			}
		}
	}
	return "", "", false
}

// evidence returns the line the match landed on, so a report can quote what
// the provider actually said instead of only naming the pattern.
func evidence(tail string, index int) string {
	start := strings.LastIndexByte(tail[:index], '\n') + 1
	end := strings.IndexByte(tail[index:], '\n')
	if end < 0 {
		end = len(tail)
	} else {
		end += index
	}
	line := strings.TrimSpace(tail[start:end])
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}

// Switch is the harness, model, and effort a rule routes to.
type Switch struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// Rule is one standing decision: when this harness hits this fault, go here.
type Rule struct {
	// Harness the rule applies to. Empty matches any harness.
	Harness string `json:"harness,omitempty"`
	// Fault the rule answers.
	Fault Fault `json:"fault"`
	// Switch is where the task goes.
	Switch Switch `json:"switch"`
	// Auto lets the watcher act without asking. Without it the rule is a
	// recommendation the CFO is woken with, which is the safer default for a
	// policy that restarts someone's harness.
	Auto bool `json:"auto,omitempty"`
	// ForceDirty renders --force-dirty in the rule's switch command, so a
	// standing answer still works on a goblin that hit its fault mid-work
	// with uncommitted changes.
	ForceDirty bool `json:"force_dirty,omitempty"`
	// Note explains the rule to whoever reads the file next.
	Note string `json:"note,omitempty"`
}

// Policy is the whole standing routing table.
type Policy struct {
	Rules []Rule `json:"rules"`
	// Path is where it was loaded from; not serialized.
	Path string `json:"-"`
}

// Load reads the policy from a CFO home's data directory. A missing file is
// not an error: a fleet with no standing rules simply wakes the CFO for every
// fault, which is the behaviour without this package at all.
func Load(dataDir string) (Policy, error) {
	path := filepath.Join(dataDir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Policy{Path: path}, nil
	}
	if err != nil {
		return Policy{}, err
	}
	var policy Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		return Policy{}, fmt.Errorf("routing: %s: %w", path, err)
	}
	policy.Path = path
	for index, rule := range policy.Rules {
		if rule.Fault == "" {
			return Policy{}, fmt.Errorf("routing: %s: rule %d has no fault", path, index)
		}
		if rule.Switch.Harness == "" {
			return Policy{}, fmt.Errorf("routing: %s: rule %d for %s does not say what to switch to", path, index, rule.Fault)
		}
	}
	return policy, nil
}

// Match returns the rule that answers this harness hitting this fault. The
// first matching rule wins, and a harness-specific rule is preferred over a
// catch-all regardless of file order.
func (p Policy) Match(harness string, fault Fault) (Rule, bool) {
	var fallback Rule
	found := false
	for _, rule := range p.Rules {
		if rule.Fault != fault {
			continue
		}
		if strings.EqualFold(rule.Harness, harness) {
			return rule, true
		}
		if rule.Harness == "" && !found {
			fallback, found = rule, true
		}
	}
	return fallback, found
}

// Command renders the cfo switch command a rule calls for, which is what the
// CFO is shown when the rule is a recommendation rather than an automatic.
func (r Rule) Command(id string) string {
	command := "cfo switch " + id + " --harness " + r.Switch.Harness
	if r.Switch.Model != "" {
		command += " --model " + r.Switch.Model
	}
	if r.Switch.Effort != "" {
		command += " --effort " + r.Switch.Effort
	}
	if r.ForceDirty {
		command += " --force-dirty"
	}
	return command
}
