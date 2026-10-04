package routing

import (
	"strings"
	"testing"
)

func TestDetectIgnoresQuotedReviewerFaults(t *testing.T) {
	weekly := "You've hit your weekly limit · resets Oct 7, 4am (America/Chicago)"
	cases := []struct {
		name string
		tail string
	}{
		{"wake4683 review diff", "• Edited handoff.md (+1 -0)\n    64 +The review log states: " + weekly + "\n• Working (8s • esc to interrupt)"},
		{"numbered historical refusal", "    38 +Error: 429 rate limit reached for model codex"},
		{"unnumbered added log", "+" + weekly},
		{"removed historical refusal", "-Error: 429 rate limit reached"},
		{"source search result", `internal/routing/detect_quoted_test.go:38: refusal := "API Error: 429 rate_limit_error"`},
		{"read review log", "• Ran Get-Content review.log\n  └ " + weekly + "\n    Error: 503 Service Unavailable\n\n• Working (8s • esc to interrupt)"},
		{"tool test output", "• Ran go test ./internal/routing\n  └ --- FAIL: TestQuotedFailure\n    fixture: API Error: 429 rate_limit_error\n    fixture: 401 Unauthorized: invalid api key\n\n• Working (8s • esc to interrupt)"},
		{"own historical failure in code fence", "Earlier output:\n```text\nError: 429 rate limit reached\n```\nContinuing the repair."},
		{"CRLF review diff", "    64 +The review log states: " + weekly + "\r\n• Working (8s • esc to interrupt)"},
		{"quoted auth diff", "    38 +401 Unauthorized: invalid api key"},
		{"quoted provider source result", "review.log:38: Error: 503 Service Unavailable"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fault, evidence, found := Detect(test.tail)
			if found {
				t.Fatalf("Detect = (%q, %q), want no worker fault from quoted evidence", fault, evidence)
			}
		})
	}
}

func TestDetectKeepsGenuineFaultsAfterQuotedEvidence(t *testing.T) {
	cases := []struct {
		name string
		line string
		want Fault
	}{
		{"claude weekly", "You've hit your weekly limit · resets Oct 7, 4am (America/Chicago)", RateLimit},
		{"claude resumed tool result", "  ⎿ You've hit your session limit · resets 3:30pm (America/Chicago)", RateLimit},
		{"codex", "You've hit your usage limit. Upgrade to Plus or try again at Oct 7, 2026 4:00 AM.", RateLimit},
		{"pi provider response", "API Error: 429 rate_limit_error: rate limit reached", RateLimit},
		{"kimi", "kimi: request failed with 403: quota exceeded", RateLimit},
		{"auth", "401 Unauthorized: invalid api key", Auth},
		{"provider", "Error: 503 Service Unavailable", Provider},
		{"third party", "gh: 429 API rate limit exceeded for user", ThirdParty},
	}
	quotes := []string{
		"    64 +The review log states: You've hit your weekly limit · resets Oct 7, 4am (America/Chicago)\n",
		"• Ran Get-Content review.log\n  └ Error: 503 Service Unavailable\n\n",
		"```text\n401 Unauthorized: invalid api key\n```\n",
		"• Ran Get-Content review.log\n  └ Error: 503 Service Unavailable\n    previous output\n",
		"An incomplete quoted fence:\n```text\n",
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for _, quote := range quotes {
				fault, evidence, found := Detect(quote + test.line)
				if !found || fault != test.want || evidence != strings.TrimSpace(test.line) {
					t.Errorf("Detect after %q = (%q, %q, %v), want (%q, %q, true)", quote, fault, evidence, found, test.want, strings.TrimSpace(test.line))
				}
				fault, evidence, found = Detect(quote + "  " + test.line)
				if !found || fault != test.want || evidence != strings.TrimSpace(test.line) {
					t.Errorf("Detect indented refusal after %q = (%q, %q, %v), want (%q, %q, true)", quote, fault, evidence, found, test.want, strings.TrimSpace(test.line))
				}
			}
		})
	}
}

func TestDetectKeepsThirdPartyToolFailures(t *testing.T) {
	tail := "• Ran gh pr view\n  └ gh: 429 API rate limit exceeded for user\n"
	fault, evidence, found := Detect(tail)
	if !found || fault != ThirdParty || evidence != "└ gh: 429 API rate limit exceeded for user" {
		t.Fatalf("Detect = (%q, %q, %v), want the actual git-platform tool failure", fault, evidence, found)
	}
}
