package routing

import (
	"strings"
	"testing"
)

func TestDetectIgnoresNativeRanSearchCommands(t *testing.T) {
	// The Ran search and token were witnessed; paths and wrap widths are fixtures.
	for _, test := range []struct {
		name, tail string
	}{
		{"witnessed search shape", "• Ran rg -n \"invalid_api_key\" internal/routing\n  └ no matches"},
		{"bare Ran header", "Ran rg -n \"invalid_api_key\" internal/routing\n  └ no matches"},
		{"wrapped arguments before output", "• Ran rg -n\n    \"invalid_api_key|authentication_error\"\n    internal/routing\n  └ no matches"},
		{"CRLF wrapped arguments", "• Ran rg -n\r\n    \"invalid_api_key\"\r\n    internal/routing\r\n  └ no matches"},
		{"provider search", "• Ran rg -n \"503 service unavailable\" internal/routing\n  └ no matches"},
		{"quota search", "• Ran rg -n \"rate_limit_error\" internal/routing\n  └ no matches"},
		{"third party search", "• Ran rg -n \"gh: 429 API rate limit exceeded for user\" internal/routing\n  └ no matches"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fault, detail, found := Detect(test.tail + "\n\n• Working (8s • esc to interrupt)")
			if found {
				t.Fatalf("Detect = (%q, %q), want no worker fault from a search command", fault, detail)
			}
		})
	}
}

func TestDetectIgnoresSavedExplanatorySearchSentence(t *testing.T) {
	// Exact assistant text at 2026-10-04T14:20:35.981Z; not a recovered screen wrap.
	tail := "The saved rollout confirms that `invalid_api_key` was in an `rg -n` tool command."
	fault, detail, found := Detect(tail)
	if found {
		t.Fatalf("Detect = (%q, %q), want no worker fault from the saved explanatory sentence", fault, detail)
	}
}

func TestDetectConstructedOperatorContinuationKeepsFaultContext(t *testing.T) {
	// The historical 4972 screen is unavailable; this indented wrap is constructed.
	tail := "↳ CFO: Inspect the quoted search command.\n  invalid_api_key was a literal search token.\n• Working (8s • esc to interrupt)"
	fault, detail, found := Detect(tail)
	if found {
		t.Fatalf("Detect = (%q, %q), want no worker fault from the constructed steer", fault, detail)
	}
	refusal := "401 Unauthorized: invalid api key"
	fault, detail, found = Detect(tail + "\n" + refusal)
	if !found || fault != Auth || detail != refusal {
		t.Fatalf("Detect = (%q, %q, %v), want genuine Auth after the operator boundary", fault, detail, found)
	}
}

func TestDetectKeepsFaultsAfterNativeCommandBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, refusal string
		want          Fault
	}{
		{"auth", "401 Unauthorized: invalid api key", Auth},
		{"provider", "Error: 503 Service Unavailable", Provider},
		{"third party", "gh: 429 API rate limit exceeded for user", ThirdParty},
		{"quota", "API Error: 429 rate_limit_error: rate limit reached", RateLimit},
		{"native capacity", "■ Selected model is at capacity. Please try a different model.", Provider},
		{"remote compact capacity", "■ Error running remote compact task: Selected model is at capacity. Please try a different model.", Provider},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, boundary := range []struct {
				name, prefix string
			}{
				{"output then blank", "• Ran rg -n \"invalid_api_key\" internal/routing\n  └ no matches\n\n"},
				{"wrapped arguments then turn", "• Ran rg -n\n    \"invalid_api_key\"\n    internal/routing\n  └ no matches\n• Working (8s • esc to interrupt)\n"},
				{"constructed operator then turn", "↳ CFO: Inspect the search evidence.\n  invalid_api_key was a literal search token.\n• Working (8s • esc to interrupt)\n"},
			} {
				t.Run(boundary.name, func(t *testing.T) {
					fault, detail, found := Detect(boundary.prefix + test.refusal)
					if !found || fault != test.want || detail != test.refusal {
						t.Fatalf("Detect = (%q, %q, %v), want (%q, %q, true)", fault, detail, found, test.want, test.refusal)
					}
				})
			}
		})
	}
	refusal := "Error: 503 Service Unavailable"
	tail := "The search token was `invalid_api_key`; " + refusal
	fault, detail, found := Detect(tail)
	if !found || fault != Provider || detail != tail {
		t.Fatalf("Detect = (%q, %q, %v), want the real provider failure alongside inline quoted code", fault, detail, found)
	}
	toolFailure := "• Ran gh pr view\n  └ gh: 429 API rate limit exceeded for user"
	fault, detail, found = Detect(toolFailure)
	if !found || fault != ThirdParty || detail != "└ gh: 429 API rate limit exceeded for user" {
		t.Fatalf("Detect = (%q, %q, %v), want genuine third-party tool failure", fault, detail, found)
	}
}

func TestDetectRecognizesRemoteCompactCapacityRefusal(t *testing.T) {
	// Complete native line witnessed by the CFO for wake 4989, not a quota banner.
	refusal := "■ Error running remote compact task: Selected model is at capacity. Please try a different model."
	for _, test := range []struct {
		name, tail string
	}{
		{"native refusal", refusal},
		{"leading whitespace", "  " + refusal},
		{"CRLF", refusal + "\r\n› Ask Codex to do anything"},
		{"after closed quote", "````text\n```nested\n" + refusal + "\n````\n" + refusal},
		{"after command output", "• Ran Get-Content review.log\n  └ " + refusal + "\n\n" + refusal},
	} {
		t.Run(test.name, func(t *testing.T) {
			fault, detail, found := Detect(test.tail)
			if !found || fault != Provider || detail != refusal {
				t.Fatalf("Detect = (%q, %q, %v), want the exact Provider refusal", fault, detail, found)
			}
		})
	}
	for _, test := range []struct {
		name, tail string
	}{
		{"fenced", "```text\n" + refusal + "\n```"},
		{"tool output", "• Ran Get-Content review.log\n  └ " + refusal + "\n\n• Working (8s • esc to interrupt)"},
		{"diff", "    38 +" + refusal},
		{"source", "review.log:38: " + refusal},
		{"operator", "↳ CFO: Inspect this earlier refusal.\n  " + refusal},
		{"ordinary discussion", "Earlier output said " + refusal + "; the worker continued."},
	} {
		t.Run(test.name, func(t *testing.T) {
			fault, detail, found := Detect(test.tail)
			if found {
				t.Fatalf("Detect = (%q, %q), want no worker fault from a quoted remote-compaction refusal", fault, detail)
			}
		})
	}
	fault, detail, found := Detect("An unfinished fence:\n```text\n" + refusal)
	if !found || fault != Provider || strings.TrimSpace(detail) != refusal {
		t.Fatalf("Detect = (%q, %q, %v), want existing unfinished-fence pass-through", fault, detail, found)
	}
}
