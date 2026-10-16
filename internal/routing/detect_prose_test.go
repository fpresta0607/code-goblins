package routing

import "testing"

// A goblin's own words about a limit are not its harness hitting one: on
// 2026-10-15 wake 1006530 woke the CFO with a rate limit and a switch for a
// working goblin that wrote "a 429 that the classifier mistakes for rate
// limiting" (#971).
func TestDetectIgnoresAGoblinsOwnWordsAboutALimit(t *testing.T) {
	cases := []struct {
		name string
		tail string
	}{
		{"wake 1006530's line", "a 429 that the classifier mistakes for rate limiting and pauses on. I'll dig into that pause logic next."},
		{"the same under claude's reply mark", "⏺ Found it: a 429 that the classifier mistakes for rate limiting and pauses on.\n  I'll dig into that pause logic next.\n\n✽ Shimmying… (12s · ↓ 1.2k tokens)"},
		{"a claude reply quoting a refusal", "⏺ The deploy check failed earlier with\n  Error: 429 Too Many Requests - rate limit reached, so the retry\n  waits now.\n\n  The next paragraph says the API error: 429 was the provider's."},
		{"a codex reply quoting a refusal", "• The API returned Error: 429 Too Many Requests - rate limit reached\n  before, which the backoff now handles.\n\n• Working (8s • esc to interrupt)"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Act
			fault, evidence, found := Detect(test.tail)

			// Assert
			if found {
				t.Errorf("Detect = (%q, %q), want no fault in the goblin's own words", fault, evidence)
			}
		})
	}
}

func TestDetectStillReadsTheHarnessRefusalBesideAGoblinsWords(t *testing.T) {
	cases := []struct {
		name string
		tail string
		want Fault
	}{
		{"claude's refusal under its reply", "⏺ I'll check the rate limiting next.\n  ⎿  API Error: 429 {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\"}}", RateLimit},
		{"claude's session banner after its reply", "⏺ Looking at the 429 handling now.\n\nYou've hit your session limit · resets 3:30pm (America/Chicago)", RateLimit},
		{"a harness notice on the reply mark", "⏺ API Error: 429 {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\"}}", RateLimit},
		{"codex's usage refusal after its reply", "• Checking the rate limit code.\n\n■ You've hit your usage limit. Upgrade to Plus or try again at 3:04 PM.", RateLimit},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Act
			fault, _, found := Detect(test.tail)

			// Assert
			if !found || fault != test.want {
				t.Errorf("Detect = (%q, %v), want %q", fault, found, test.want)
			}
		})
	}
}
