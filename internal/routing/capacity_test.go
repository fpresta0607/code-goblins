package routing

import (
	"strings"
	"testing"
)

func TestDetectRecognizesTheNativeCodexCapacityRefusal(t *testing.T) {
	banner := "■ Selected model is at capacity. Please try a different model."
	quotes := []string{
		"",
		"    64 +" + banner,
		"-" + banner,
		"review.log:38: " + banner,
		"```text\n" + banner + "\n```",
		"~~~text\n" + banner + "\n~~~",
		"````text\n```nested\n" + banner + "\n````",
		"~~~text\n~~~nested\n" + banner + "\n~~~",
		"• Ran Get-Content review.log\n  └ earlier output\n    " + banner,
		"• Ran Get-Content review.log\n  └ " + banner,
		"> " + banner,
		"CFO: earlier capacity refusal\n  " + banner,
		"Overlord: " + banner,
		"The review log contains: " + banner,
		"Selected model is at capacity. Please try a different model.",
		banner + " was a historical failure.",
		"■ Selected model is at capacity.",
	}
	for _, quote := range quotes {
		t.Run(quote, func(t *testing.T) {
			if fault, evidence, found := Detect(quote); found {
				t.Fatalf("quoted/discussed capacity = (%q, %q), want no fault", fault, evidence)
			}
			for _, refusal := range []string{banner, "  " + banner + "\r"} {
				fault, evidence, found := Detect(quote + "\n\n" + refusal)
				if !found || fault != Provider || evidence != banner {
					t.Errorf("genuine refusal after quote = (%q, %q, %v), want Provider and original banner", fault, evidence, found)
				}
			}
		})
	}
	for _, prefix := range []string{
		"• Ran Get-Content review.log\n  └ earlier output\n    quoted continuation\n",
		"CFO: continue on the same model\n",
		"```an unfinished fence\n",
	} {
		fault, evidence, found := Detect(prefix + banner)
		if !found || fault != Provider || !strings.HasPrefix(evidence, "■ Selected model") {
			t.Errorf("native refusal after prior text = (%q, %q, %v), want Provider", fault, evidence, found)
		}
	}
}

func TestDetectKeepsMatchingFenceRefusalsQuoted(t *testing.T) {
	refusals := []struct {
		name, line string
		fault      Fault
	}{
		{"capacity", "■ Selected model is at capacity. Please try a different model.", Provider},
		{"auth", "401 Unauthorized: invalid api key", Auth},
		{"provider", "Error: 503 Service Unavailable", Provider},
		{"third party", "gh: 429 API rate limit exceeded for user", ThirdParty},
		{"quota", "API Error: 429 rate_limit_error: rate limit reached", RateLimit},
	}
	for _, fence := range []struct {
		name, opening, inner, closing string
	}{
		{"backtick tagged inner", "```text", "```nested", "```"},
		{"backtick shorter inner", "````text", "```", "````"},
		{"backtick longer close", "```text", "~~~", "````"},
		{"backtick mixed inner", "````text", "~~~~", "````"},
		{"tilde tagged inner", "~~~text", "~~~nested", "~~~"},
		{"tilde shorter inner", "~~~~text", "~~~", "~~~~"},
		{"tilde longer close", "~~~text", "```", "~~~~"},
		{"tilde mixed inner", "~~~~text", "````", "~~~~"},
	} {
		t.Run(fence.name, func(t *testing.T) {
			for _, quoted := range refusals {
				t.Run(quoted.name, func(t *testing.T) {
					quote := "• Earlier log\n" + fence.opening + "\n" + fence.inner + "\n" + quoted.line + "\n" + fence.closing
					if fault, evidence, found := Detect(quote); found {
						t.Fatalf("closed quoted refusal = (%q, %q), want no fault", fault, evidence)
					}
					for _, actual := range refusals {
						fault, evidence, found := Detect(quote + "\n\n  " + actual.line + "\r\n")
						if !found || fault != actual.fault || evidence != actual.line {
							t.Errorf("actual %s after quoted %s = (%q, %q, %v), want (%q, %q, true)", actual.name, quoted.name, fault, evidence, found, actual.fault, actual.line)
						}
					}
				})
			}
		})
	}
}

func TestDetectPreservesUnfinishedFenceRefusals(t *testing.T) {
	for _, opening := range []string{"```text", "````text", "~~~text", "~~~~text"} {
		for _, refusal := range []struct {
			line  string
			fault Fault
		}{
			{"■ Selected model is at capacity. Please try a different model.", Provider},
			{"401 Unauthorized: invalid api key", Auth},
			{"Error: 503 Service Unavailable", Provider},
			{"gh: 429 API rate limit exceeded for user", ThirdParty},
			{"API Error: 429 rate_limit_error: rate limit reached", RateLimit},
		} {
			fault, evidence, found := Detect(opening + "\n" + refusal.line)
			if !found || fault != refusal.fault || evidence != refusal.line {
				t.Errorf("refusal after unfinished %q = (%q, %q, %v), want (%q, %q, true)", opening, fault, evidence, found, refusal.fault, refusal.line)
			}
		}
	}
}
