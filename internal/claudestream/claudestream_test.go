package claudestream

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestArgumentsStartOrResumeTheSessionOnTheStream(t *testing.T) {
	for _, testCase := range []struct {
		isResumed bool
		want      []string
	}{
		{false, []string{"--session-id", "s1"}},
		{true, []string{"--resume", "s1"}},
	} {
		// Act
		args := Arguments("s1", testCase.isResumed)

		// Assert
		if !slices.Equal(args[len(args)-2:], testCase.want) {
			t.Errorf("Arguments(resumed %v) ends %q, want %q", testCase.isResumed, args[len(args)-2:], testCase.want)
		}
		for _, flag := range []string{"-p", "--include-partial-messages", "--replay-user-messages", "--permission-prompt-tool"} {
			if !slices.Contains(args, flag) {
				t.Errorf("Arguments(resumed %v) = %q, want %s", testCase.isResumed, args, flag)
			}
		}
	}
}

func TestEnvironmentDropsTheAPIKeyInAnyCaseAndKeepsTheRest(t *testing.T) {
	// Arrange
	env := []string{"PATH=C:\\bin", "ANTHROPIC_API_KEY=sk-one", "anthropic_api_key=sk-two", "ANTHROPIC_API_KEY_HELPER=kept", "ANTHROPIC_MODEL=kept"}

	// Act
	kept := Environment(env)

	// Assert
	if want := []string{"PATH=C:\\bin", "ANTHROPIC_API_KEY_HELPER=kept", "ANTHROPIC_MODEL=kept"}; !slices.Equal(kept, want) {
		t.Errorf("Environment = %q, want %q", kept, want)
	}
}

func TestUserLineCarriesHisWordsExactly(t *testing.T) {
	// Arrange
	words := "line one\n\"quoted\" \\ <b>tag</b> é"

	// Act
	line := UserLine(words)

	// Assert
	var read struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &read); err != nil || read.Type != "user" || read.Message.Role != "user" || read.Message.Content != words {
		t.Errorf("UserLine = %s, %v", line, err)
	}
}

func TestControlLinesCarryTheirRequestID(t *testing.T) {
	for _, testCase := range []struct {
		name string
		line func() (json.RawMessage, error)
		want string
	}{
		{"a request", func() (json.RawMessage, error) {
			return RequestLine("q1", "set_model", map[string]string{"model": "claude-opus-5-5"}), nil
		}, `{"request":{"model":"claude-opus-5-5","subtype":"set_model"},"request_id":"q1","type":"control_request"}`},
		{"an allow", func() (json.RawMessage, error) { return AllowLine("r1", json.RawMessage(`{"command":"ls"}`)) },
			`{"response":{"request_id":"r1","response":{"behavior":"allow","updatedInput":{"command":"ls"}},"subtype":"success"},"type":"control_response"}`},
		{"a deny", func() (json.RawMessage, error) { return DenyLine("r1", "Not now."), nil },
			`{"response":{"request_id":"r1","response":{"behavior":"deny","message":"Not now."},"subtype":"success"},"type":"control_response"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Act
			line, err := testCase.line()

			// Assert
			if err != nil || string(line) != testCase.want {
				t.Errorf("line = %s, %v; want %s", line, err, testCase.want)
			}
		})
	}
}

func TestAllowLineRefusesInputThatIsNotJSON(t *testing.T) {
	// Act
	_, err := AllowLine("r1", json.RawMessage(`{"command":`))

	// Assert
	if err == nil {
		t.Error("AllowLine took input that is not JSON")
	}
}
