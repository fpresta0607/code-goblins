package routing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectClassifiesTheProviderFailuresGoblinsActuallyHit(t *testing.T) {
	cases := []struct {
		name string
		tail string
		want Fault
	}{
		{"kimi quota 403", "API error: 403 quota exceeded for this organization", RateLimit},
		{"anthropic 429", "Error: 429 Too Many Requests - rate limit reached", RateLimit},
		{"usage limit prose", "You have hit your usage limit. Try again later.", RateLimit},
		{"rejected key", "401 Unauthorized: invalid api key", Auth},
		{"anthropic auth shape", `{"type":"authentication_error"}`, Auth},
		{"provider outage", "Error: 503 Service Unavailable", Provider},
		{"overloaded", `{"type":"overloaded_error","message":"Overloaded"}`, Provider},
	}
	for _, test := range cases {
		fault, evidence, found := Detect(test.tail)
		if !found || fault != test.want {
			t.Errorf("%s: Detect = (%q, %v), want %q", test.name, fault, found, test.want)
		}
		if evidence == "" {
			t.Errorf("%s: no evidence line, so a report cannot quote what the provider said", test.name)
		}
	}
}

func TestDetectReadsAQuota403AsARateLimitNotAnAuthFailure(t *testing.T) {
	// The Overlord's real case: kimi refused with a 403 that was about quota.
	// Routing it to "auth" would send the fleet hunting for a bad credential.
	fault, _, found := Detect("kimi: request failed with 403: quota exceeded")
	if !found || fault != RateLimit {
		t.Fatalf("Detect = (%q, %v), want %q", fault, found, RateLimit)
	}
}

func TestDetectIgnoresOrdinaryWork(t *testing.T) {
	tails := []string{
		"",
		"Running tests... ok\nAll 42 tests passed",
		"warning: unused variable at line 429 of parser.go",
		"HTTP 401 handler registered",
		"added 503 lines to the report",
	}
	for _, tail := range tails {
		if fault, _, found := Detect(tail); found {
			t.Errorf("Detect(%q) = %q, want no fault in ordinary output", tail, fault)
		}
	}
}

func TestDetectQuotesTheLineTheProviderPrinted(t *testing.T) {
	tail := "building the parser\nError: 429 rate limit reached for model kimi-k2\nretrying in 60s"
	_, evidence, found := Detect(tail)
	if !found {
		t.Fatal("Detect found nothing")
	}
	if evidence != "Error: 429 rate limit reached for model kimi-k2" {
		t.Errorf("evidence = %q, want the offending line alone", evidence)
	}
}

func TestLoadTreatsAMissingPolicyAsNoStandingRules(t *testing.T) {
	policy, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(policy.Rules) != 0 {
		t.Errorf("rules = %v, want none", policy.Rules)
	}
	if _, matched := policy.Match("kimi", RateLimit); matched {
		t.Error("an empty policy matched a rule")
	}
}

func TestLoadRefusesARuleThatDoesNotSayWhereToGo(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"rules":[{"fault":"rate-limit"}]}`)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "does not say what to switch to") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestMatchPrefersAHarnessRuleOverACatchAllWhicheverComesFirst(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"rules":[
		{"fault":"rate-limit","switch":{"harness":"codex"}},
		{"harness":"kimi","fault":"rate-limit","switch":{"harness":"claude","model":"opus","effort":"xhigh"},"auto":true}
	]}`)
	policy, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	rule, matched := policy.Match("kimi", RateLimit)
	if !matched || rule.Switch.Harness != "claude" || !rule.Auto {
		t.Fatalf("rule = %+v, want the kimi-specific automatic rule", rule)
	}
	// Anything else still gets the catch-all.
	other, matched := policy.Match("codex", RateLimit)
	if !matched || other.Switch.Harness != "codex" || other.Auto {
		t.Errorf("rule = %+v, want the catch-all", other)
	}
	if _, matched := policy.Match("kimi", Auth); matched {
		t.Error("a rate-limit rule answered an auth fault")
	}
}

func TestCommandRendersTheSwitchTheRuleCallsFor(t *testing.T) {
	rule := Rule{Switch: Switch{Harness: "claude", Model: "opus", Effort: "xhigh"}}
	if got, want := rule.Command("qm-v4"), "cfo switch qm-v4 --harness claude --model opus --effort xhigh"; got != want {
		t.Errorf("Command = %q, want %q", got, want)
	}
	bare := Rule{Switch: Switch{Harness: "codex"}}
	if got, want := bare.Command("g1"), "cfo switch g1 --harness codex"; got != want {
		t.Errorf("Command = %q, want %q", got, want)
	}
	forced := Rule{Switch: Switch{Harness: "claude", Model: "opus", Effort: "xhigh"}, ForceDirty: true}
	if got, want := forced.Command("qm-v4"), "cfo switch qm-v4 --harness claude --model opus --effort xhigh --force-dirty"; got != want {
		t.Errorf("Command = %q, want %q", got, want)
	}
}

func write(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDetectDoesNotReadACFOsteerAsARateLimit(t *testing.T) {
	// The real false positive: the CFO's own steer echoed into the composer
	// contained the phrase "rate limit" in prose, with no error framing.
	steer := "this is them, not you, and not a model rate limit, so no harness switch"
	if fault, _, found := Detect(steer); found {
		t.Errorf("Detect(%q) = %q, want no fault in conversational CFO text", steer, fault)
	}
}

func TestDetectDoesNotReadAGitHostMentionAsAnOutage(t *testing.T) {
	steers := []string{
		"this is not a github rate limit, so no harness switch",
		"the goblin exceeded the github storage budget and is now cleaning up",
	}
	for _, steer := range steers {
		if fault, _, found := Detect(steer); found {
			t.Errorf("Detect(%q) = %q, want no fault in conversational text", steer, fault)
		}
	}
}

func TestDetectClassifiesAGitHubOutageAsThirdParty(t *testing.T) {
	tails := []string{
		"gh: 429 API rate limit exceeded for user 123456",
		"fatal: unable to access 'https://github.com/x/y.git': The requested URL returned error: 503",
		"You have exceeded a secondary rate limit on api.github.com",
	}
	for _, tail := range tails {
		fault, _, found := Detect(tail)
		if !found || fault != ThirdParty {
			t.Errorf("Detect(%q) = (%q, %v), want ThirdParty for a GitHub outage", tail, fault, found)
		}
	}
}

func TestDetectDoesNotReadAGhSuffixWordAsAnOutage(t *testing.T) {
	tails := []string{
		"high: 429 rows",
		"weigh: 503 bytes",
		"sigh: 502 gates",
	}
	for _, tail := range tails {
		if fault, _, found := Detect(tail); found {
			t.Errorf("Detect(%q) = %q, want no fault for a word ending in gh:", tail, fault)
		}
	}
}

func TestThirdPartyFaultNeverMatchesAPolicyRule(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"rules":[{"harness":"claude","fault":"third-party","switch":{"harness":"codex"}}]}`)
	policy, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rule, matched := policy.Match("claude", ThirdParty); matched {
		t.Errorf("a third-party outage matched a switch rule: %+v", rule)
	}
	// A genuine model-provider outage must still match its own rule.
	providerRule := Rule{Harness: "claude", Fault: Provider, Switch: Switch{Harness: "codex"}}
	policy = Policy{Rules: []Rule{providerRule}}
	if rule, matched := policy.Match("claude", Provider); !matched || rule.Switch.Harness != "codex" {
		t.Errorf("a model-provider outage did not match its rule: rule=%+v matched=%v", rule, matched)
	}
}
