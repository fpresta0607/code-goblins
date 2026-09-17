package routing

import (
	"strings"
	"testing"
)

// steerPane is the pane of peak-compute-to-supabase as Herdr captured it on
// 2026-09-17, rendered by Claude Code: an earlier line naming a pull request
// URL, then the CFO's steer about a GitHub Actions failure with the pane's
// prompt marker in front and two-space continuation lines where the pane
// wrapped it. The watcher woke the CFO with harness_error rate-limit and a
// `cfo switch` recommendation for a goblin that was working perfectly.
func steerPane(prefix string) string {
	return "  https://github.com/fpresta0607/peakCraftsman/pull/23 - docs only, MERGEABLE / CLEAN, all checks green.\n" +
		"\n" +
		"✻ Cogitated for 5m 35s · done 7:06 AM\n" +
		"\n" +
		"❯ " + prefix + "CI on main failed (run 35221209724, job sql) and the Overlord wants it closed out. It is not our code: supabase/setup-cli@v1 with\n" +
		"  version 'latest' resolves through the GitHub API and got 'Failed to resolve latest Supabase CLI release: rate limit exceeded', then the\n" +
		"  always() cleanup died 127 because the CLI was never installed. A rerun is in flight. Harden it in a small PR: pin setup-cli to an exact CLI\n" +
		"  version rather than 'latest' so no API resolution happens on every run, with a comment saying why and how to bump it; make the cleanup step\n" +
		"  tolerant of a CLI that was never installed so a setup failure reports its real cause instead of a misleading 127; and if setup-cli supports\n" +
		"  it, add a short retry. Confirm the rerun's outcome and say whether main is green, then notify done with the PR and the run id. Nothing else\n" +
		"  in the workflow changes.\n" +
		"\n" +
		"  Ran 2 shell commands\n" +
		"\n" +
		"● Key finding: setup-cli installs from npm, not GitHub releases - so a fixed version skips API resolution entirely. It also says it can detect\n" +
		"  from the lockfile. Let me check what we have.\n"
}

func TestDetectIgnoresTheCFOsOwnSteerAboutAGitHubRateLimit(t *testing.T) {
	if fault, evidence, found := Detect(steerPane(SteerPrefix)); found {
		t.Fatalf("Detect = %q (%q), want no fault: the CFO's own steer is not evidence about the harness", fault, evidence)
	}
}

func TestDetectReadsAnUnprefixedGitHubActionsRateLimitAsThirdParty(t *testing.T) {
	// Even without the prefix, a GitHub API or Actions rate limit is the
	// platform's problem, never a harness-switch case. The first "github" in
	// this pane is a pull request URL on a healthy line; the detector must
	// keep looking rather than fall through to the provider rule.
	fault, evidence, found := Detect(steerPane(""))
	if !found || fault != ThirdParty {
		t.Fatalf("Detect = (%q, %v), want %q", fault, found, ThirdParty)
	}
	if !strings.Contains(evidence, "rate limit exceeded") {
		t.Errorf("evidence = %q, want the line that reported the GitHub rate limit", evidence)
	}
}

func TestDetectReadsAnActionsRunnerRateLimitAsThirdPartyWithoutTheWordGitHub(t *testing.T) {
	tail := "  the Actions run failed: rate limit exceeded while resolving the CLI release\n"
	if fault, _, found := Detect(tail); !found || fault != ThirdParty {
		t.Errorf("Detect = (%q, %v), want %q", fault, found, ThirdParty)
	}
}

func TestDetectIgnoresAnOverlordLineTypedIntoThePane(t *testing.T) {
	tail := "❯ " + OverlordPrefix + "ignore the 429 rate limit exceeded noise from the mock server, it is\n  ours and expected\n"
	if fault, _, found := Detect(tail); found {
		t.Errorf("Detect = %q, want no fault from a line the Overlord typed", fault)
	}
}

func TestDetectStillReadsTheProviderAfterASteer(t *testing.T) {
	tail := steerPane(SteerPrefix) + "\nError: 429 rate limit reached for model opus\n"
	fault, evidence, found := Detect(tail)
	if !found || fault != RateLimit {
		t.Fatalf("Detect = (%q, %v), want the provider's own refusal still detected", fault, found)
	}
	if evidence != "Error: 429 rate limit reached for model opus" {
		t.Errorf("evidence = %q, want the provider line quoted from the original tail", evidence)
	}
}

func TestDetectRedactsOnlyTheSteerAndItsWrappedContinuation(t *testing.T) {
	tail := "❯ " + SteerPrefix + "not a model rate limit, keep going\n  (wrapped) rate limit exceeded is GitHub's\nError: 503 Service Unavailable\n"
	fault, evidence, found := Detect(tail)
	if !found || fault != Provider || evidence != "Error: 503 Service Unavailable" {
		t.Errorf("Detect = (%q, %q, %v), want the unindented provider line after the steer", fault, evidence, found)
	}
}

func TestDetectReadsAHarnessRefusalAttachedDirectlyUnderASteer(t *testing.T) {
	refusal := `  ⎿  API Error: 429 {"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your rate limit"}}`
	tail := "❯ " + SteerPrefix + "keep going\n" + refusal + "\n"
	fault, evidence, found := Detect(tail)
	if !found || fault != RateLimit {
		t.Fatalf("Detect = (%q, %v), want the harness's own refusal under the steer detected", fault, found)
	}
	if evidence != strings.TrimSpace(refusal) {
		t.Errorf("evidence = %q, want the refusal line %q", evidence, strings.TrimSpace(refusal))
	}
}

func TestDetectDoesNotReadAStatusCodeInsideAGitHubRunID(t *testing.T) {
	// Every github line is examined now, and run 17742950312 contains both
	// 429 and 503; the harness's own refusal below it must still be the fault.
	tail := "● Opened https://github.com/org/repo/pull/23\n" +
		"● CI: https://github.com/org/repo/actions/runs/17742950312\n" +
		"Error: 429 rate limit reached\n"
	fault, evidence, found := Detect(tail)
	if !found || fault != RateLimit || evidence != "Error: 429 rate limit reached" {
		t.Errorf("Detect = (%q, %q, %v), want the harness rate limit", fault, evidence, found)
	}
	if fault, _, found := Detect("gh: 429 API rate limit exceeded for user\n"); !found || fault != ThirdParty {
		t.Errorf("Detect = (%q, %v), want a genuine gh 429 still third-party", fault, found)
	}
}

func TestDetectDoesNotReadAPullRequestOrIssueNumberAsAnOutage(t *testing.T) {
	pushed := "To github.com:org/repo.git\n● Opened https://github.com/org/repo/pull/502\n"
	for _, tail := range []string{pushed, "● Opened https://github.com/org/repo/issues/429\n"} {
		if fault, evidence, found := Detect(tail); found {
			t.Errorf("Detect(%q) = (%q, %q), want no fault for a pull request or issue number", tail, fault, evidence)
		}
	}
	refusal := `API Error: 429 {"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your rate limit"}}`
	fault, evidence, found := Detect(pushed + refusal + "\n")
	if !found || fault != RateLimit || evidence != refusal {
		t.Errorf("Detect = (%q, %q, %v), want the harness's rate limit after the pull request line", fault, evidence, found)
	}
}

func TestDetectReadsAGitHubHTTPStatusAsThirdPartyNotProvider(t *testing.T) {
	// gh prints a GitHub 5xx with no error word; the provider patterns "502
	// bad gateway" and "service unavailable" would otherwise claim it and
	// recommend a harness switch.
	for _, tail := range []string{
		"HTTP 502: 502 Bad Gateway (https://api.github.com/graphql)",
		"HTTP 503: Service Unavailable (https://api.github.com/repos)",
		"HTTP 502 (https://api.github.com/graphql)",
		"api.github.com answered 503 Service Unavailable",
	} {
		if fault, _, found := Detect(tail); !found || fault != ThirdParty {
			t.Errorf("Detect(%q) = (%q, %v), want %q", tail, fault, found, ThirdParty)
		}
	}
	if fault, evidence, found := Detect("● Opened https://github.com/org/repo/pull/502: ready for review\n"); found {
		t.Errorf("Detect = (%q, %q), want no fault for a pull request number before a colon", fault, evidence)
	}
}

func TestDetectExclusionUsesTheSameConstantTheSenderStamps(t *testing.T) {
	// fleet.Sender stamps every steer with SteerPrefix; the exclusion keys on
	// the same constant, so this test and the sender's test move together
	// when the prefix changes. A literal here would drift.
	if fault, _, found := Detect(SteerPrefix + "Error: 429 Too Many Requests - rate limit reached"); found {
		t.Errorf("Detect = %q, want a prefixed line excluded", fault)
	}
	if fault, _, found := Detect("Error: 429 Too Many Requests - rate limit reached"); !found || fault != RateLimit {
		t.Errorf("Detect = (%q, %v), want the same line without the prefix detected", fault, found)
	}
}
