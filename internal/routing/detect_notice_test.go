package routing

import (
	"strings"
	"testing"
)

// Claude Code opens its replies, its tool calls and its own notices with "⏺"
// on macOS and "●" everywhere else. A Windows goblin's Monitor notice
// printing a test's local 503 woke the CFO as a model-provider outage
// (#1027).
func TestDetectIgnoresOutputClaudeCodeDrawsOnItsMark(t *testing.T) {
	const witnessed = "Monitor event: keys_windows_test.go:288: http://127.0.0.1:64300/json/version answered 503 Service Unavailable"
	cases := []struct {
		name string
		body string
	}{
		{"a monitor event printing a 503", witnessed},
		{"a monitor event printing a 429", "Monitor event: keys_windows_test.go:288: http://127.0.0.1:64300/json/version answered 429 Too Many Requests"},
		{"a monitor event printing a provider's refusal", "Monitor event: fixture: API Error: 429 {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\"}}"},
		{"a monitor event wrapped onto its next row", "Monitor event: keys_windows_test.go:288: http://127.0.0.1:64300/json/version answered\n  503 Service Unavailable"},
		{"a tool call's output", "Bash(go test ./internal/browser)\n  ⎿  keys_windows_test.go:288: answered 503 Service Unavailable"},
		{"the agent's reply quoting a refusal", "The deploy check failed earlier with\n  Error: 429 Too Many Requests - rate limit reached, so the retry waits now."},
	}
	for _, mark := range []string{"⏺ ", "● "} {
		for _, test := range cases {
			t.Run(mark+test.name, func(t *testing.T) {
				// Arrange
				tail := mark + test.body + "\n\n✻ Cogitating… (esc to interrupt)"

				// Act
				fault, evidence, found := Detect(tail)

				// Assert
				if found {
					t.Errorf("Detect = (%q, %q), want no fault in output the harness draws on its mark", fault, evidence)
				}
			})
		}
	}
}

func TestDetectStillReadsTheHarnessRefusalOnItsMark(t *testing.T) {
	const notice = "Monitor event: keys_windows_test.go:288: http://127.0.0.1:64300/json/version answered 503 Service Unavailable\n\n"
	cases := []struct {
		name string
		tail string
		want Fault
	}{
		{"an api error on the mark", "API Error: 429 {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\"}}", RateLimit},
		{"an api error after a monitor event", notice + "{mark}API Error: 529 {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}", Provider},
		{"an api error under a reply", "I'll check the rate limiting next.\n  ⎿  API Error: 429 {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\"}}", RateLimit},
		{"a session limit after a monitor event", notice + "You've hit your session limit · resets 3:30pm (America/Chicago)", RateLimit},
	}
	for _, mark := range []string{"⏺ ", "● "} {
		for _, test := range cases {
			t.Run(mark+test.name, func(t *testing.T) {
				// Arrange
				tail := mark + strings.ReplaceAll(test.tail, "{mark}", mark)

				// Act
				fault, _, found := Detect(tail)

				// Assert
				if !found || fault != test.want {
					t.Errorf("Detect = (%q, %v), want %q", fault, found, test.want)
				}
			})
		}
	}
}
