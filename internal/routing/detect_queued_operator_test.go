package routing

import (
	"strings"
	"testing"
)

func TestDetectQueuedNativeOperatorPromptKeepsFaultContext(t *testing.T) {
	queued := "\u21b3 " + SteerPrefix + "Inspect the quoted 401 Unauthorized: invalid api key failure.\n" +
		"  Error: 503 Service Unavailable was earlier tool output.\n" +
		"  gh: 429 API rate limit exceeded for user was quoted too.\n"
	for _, test := range []struct {
		name, tail string
	}{
		{"wake4746 auth on queued line", "\u21b3 " + SteerPrefix + "Inspect the quoted 401 Unauthorized: invalid api key failure."},
		{"wrapped auth", "\u21b3 " + SteerPrefix + "Inspect the prior report:\n  401 Unauthorized: invalid api key"},
		{"wrapped provider", "\u21b3 " + SteerPrefix + "Inspect the prior report:\n  Error: 503 Service Unavailable"},
		{"wrapped third party", "\u21b3 " + SteerPrefix + "Inspect the prior report:\n  gh: 429 API rate limit exceeded for user"},
		{"queued mixed report", queued},
		{"indented queued report", "  " + queued},
		{"CRLF queued report", strings.ReplaceAll(queued, "\n", "\r\n")},
		{"queued Overlord report", strings.ReplaceAll(queued, SteerPrefix, OverlordPrefix)},
		{"ordinary operator report", strings.TrimPrefix(queued, "\u21b3 ")},
		{"quoted source", "fixture.go:12: 401 Unauthorized: invalid api key"},
		{"quoted fence", "```text\n401 Unauthorized: invalid api key\n```"},
		{"quoted tool output", "\u2022 Ran go test\n  \u2514 401 Unauthorized: invalid api key\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if fault, evidence, found := Detect(test.tail); found {
				t.Errorf("Detect = (%q, %q), want no fault from an operator or quoted report", fault, evidence)
			}
		})
	}
	for _, test := range []struct {
		name, line string
		fault      Fault
	}{
		{"auth", "401 Unauthorized: invalid api key", Auth},
		{"provider", "Error: 503 Service Unavailable", Provider},
		{"third party", "gh: 429 API rate limit exceeded for user", ThirdParty},
		{"quota", "API Error: 429 rate_limit_error: rate limit reached", RateLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, tail := range []string{
				test.line,
				test.line + "\n" + queued,
				queued + "\n" + test.line,
				queued + "  \u23bf " + test.line,
				"\u21b3 " + test.line,
			} {
				fault, evidence, found := Detect(tail)
				if !found || fault != test.fault || !strings.HasSuffix(evidence, test.line) {
					t.Errorf("Detect(%q) = (%q, %q, %t), want the actual %s refusal", tail, fault, evidence, found, test.fault)
				}
			}
		})
	}
}
