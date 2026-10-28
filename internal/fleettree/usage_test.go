package fleettree

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// piModelStore writes pi's own model catalog naming one model's context
// window, under home.
func piModelStore(t *testing.T, home, model string, window int64) {
	t.Helper()
	dir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"acme":{"models":[{"id":"` + model + `","contextWindow":` + strconv.FormatInt(window, 10) + `}]}}`
	if err := os.WriteFile(filepath.Join(dir, "models-store.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAPiSessionRecordsWhatItSpent(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := piSession(t, home,
		piHeader(),
		piMessageEntry("u1", "", "user", object{"content": "work"}),
		piMessageEntry("a1", "u1", "assistant", object{
			"provider": "acme", "model": "thinker-1", "stopReason": "stop",
			"usage":   object{"input": 1_400_000, "output": 289_000, "cacheRead": 208_000_000, "totalTokens": 593_000, "cost": object{"total": 3.209}},
			"content": []object{{"type": "text", "text": "done"}},
		}),
	)
	piModelStore(t, home, "thinker-1", 1_000_000)
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{InputTokens: 1_400_000, OutputTokens: 289_000, CacheReadTokens: 208_000_000, CostUSD: 3.209, ContextTokens: 593_000, ContextWindow: 1_000_000, Model: "acme/thinker-1"}
	if page.Usage == nil || *page.Usage != want {
		t.Fatalf("usage = %+v, want %+v", page.Usage, want)
	}
	if held := reader.Usage(path); held == nil || *held != want {
		t.Fatalf("a stream's usage = %+v, want %+v", held, want)
	}
}

func TestAPiSessionUsesItsProvidersFullModelIDForContext(t *testing.T) {
	for _, test := range []struct {
		name, provider, model string
		window                int64
	}{
		{"namespaced model", "openrouter", "deepseek/deepseek-v4.1-flash", 1_000_000},
		{"same id in another provider", "acme", "shared", 200_000},
		{"missing provider", "missing", "shared", 0},
		{"model only in another provider", "acme", "other-only", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			path := piSession(t, home, piHeader(),
				piMessageEntry("u1", "", "user", object{"content": "work"}),
				piMessageEntry("a1", "u1", "assistant", object{"provider": test.provider, "model": test.model,
					"usage": object{"input": 200, "output": 50, "totalTokens": 250}, "content": []object{{"type": "text", "text": "done"}}}),
			)
			catalog := `{"openrouter":{"models":[{"id":"deepseek/deepseek-v4.1-flash","contextWindow":1000000}]},"acme":{"models":[{"id":"shared","contextWindow":200000}]},"other":{"models":[{"id":"shared","contextWindow":100000},{"id":"other-only","contextWindow":300000}]}}`
			if err := os.WriteFile(filepath.Join(home, ".pi", "agent", "models-store.json"), []byte(catalog), 0o600); err != nil {
				t.Fatal(err)
			}
			reader := Reader{Home: home}

			// Act
			page, _, err := reader.MessagePage(path, -1)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if page.Usage == nil || page.Usage.ContextWindow != test.window || page.Usage.Model != test.provider+"/"+test.model {
				t.Fatalf("usage = %+v, want context window %d for %s/%s", page.Usage, test.window, test.provider, test.model)
			}
		})
	}
}

func TestAClaudeConversationCountsItsRequestsOnce(t *testing.T) {
	// Arrange
	home := t.TempDir()
	first := answer("a1", "reply-1", textBlock("one"))
	first["message"].(object)["usage"] = object{"input_tokens": 10, "output_tokens": 5, "cache_creation_input_tokens": 2, "cache_read_input_tokens": 3}
	// Claude Code repeats an answer's usage on each block's entry.
	second := answer("a2", "reply-1", textBlock("two"))
	second["message"].(object)["usage"] = first["message"].(object)["usage"]
	path := conversation(t, home, prompted("u1", "work"), first, second)
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{InputTokens: 12, OutputTokens: 5, CacheReadTokens: 3, ContextTokens: 20, Model: "claude-opus-5-5"}
	if page.Usage == nil || *page.Usage != want {
		t.Fatalf("usage = %+v, want %+v", page.Usage, want)
	}
}

func TestASessionThatRecordsNoUsageHasNone(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home, prompted("u1", "work"))
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if page.Usage != nil {
		t.Fatalf("usage = %+v, want none", page.Usage)
	}
}

// A session's effort is what its harness says it last set: Claude Code's
// /effort, from its own result, Codex's turn context and pi's thinking level
// change, so Modern Chat shows a change made in either view (the Overlord,
// 2026-10-27: "they should control the same interfaces. Same session").
func TestASessionReadsTheEffortItsHarnessLastSet(t *testing.T) {
	command := func(uuid, args string) object {
		return prompted(uuid, "<command-name>/effort</command-name>\n            <command-message>effort</command-message>\n            <command-args>"+args+"</command-args>")
	}
	used := answer("a1", "reply-1", textBlock("done"))
	used["message"].(object)["usage"] = object{"input_tokens": 10, "output_tokens": 5}
	for _, test := range []struct {
		name string
		path func(t *testing.T, home string) string
		want string
	}{
		{"Claude Code's /effort with a level", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("u1", "work"), used, command("c1", "max"), prompted("o1", "<local-command-stdout>Set effort level to max</local-command-stdout>"))
		}, "max"},
		{"Claude Code's /effort picker", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("u1", "work"), used, command("c1", ""), prompted("o1", "<local-command-stdout>Set effort level to xhigh (this session only)</local-command-stdout>"))
		}, "xhigh"},
		{"Claude Code's cancelled /effort", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("u1", "work"), used, command("c1", ""), prompted("o1", "<local-command-stdout>Cancelled</local-command-stdout>"))
		}, ""},
		{"Claude Code's refused /effort, which lists every level", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("u1", "work"), used, command("c1", "hgih"), prompted("o1", "<local-command-stdout>Invalid argument: hgih. Valid options are: low, medium, high, xhigh, max, auto</local-command-stdout>"))
		}, ""},
		{"Claude Code's /effort auto", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("u1", "work"), used, command("c1", "auto"), prompted("o1", "<local-command-stdout>Effort level set to auto</local-command-stdout>"))
		}, "auto"},
		{"Claude Code's /effort with its description", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("u1", "work"), used, command("c1", "high"), prompted("o1", "<local-command-stdout>Set effort level to high: Deeper reasoning for harder problems</local-command-stdout>"))
		}, "high"},
		{"Codex's turn context", func(t *testing.T, home string) string {
			turn := codexTurnContext("gpt-6-astra")
			turn["payload"].(object)["effort"] = "xhigh"
			return codexRollout(t, home, "effort", codexMeta("effort", workDir), turn,
				codexLine("token_usage_record", object{"usage": object{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}, "thread_token_usage": object{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}))
		}, "xhigh"},
		{"pi's thinking level change", func(t *testing.T, home string) string {
			return piSession(t, home, piHeader(),
				piEntry("model_change", "m1", "", object{"provider": "openrouter", "modelId": "z-ai/glm-5.3-flash"}),
				piEntry("thinking_level_change", "e1", "m1", object{"thinkingLevel": "high"}),
				piMessageEntry("u1", "e1", "user", object{"content": "work"}))
		}, "high"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			path := test.path(t, home)
			reader := Reader{Home: home}

			// Act
			page, _, err := reader.MessagePage(path, -1)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if page.Usage != nil {
				got = page.Usage.Effort
			}
			if got != test.want {
				t.Fatalf("effort = %q, want %q (usage %+v)", got, test.want, page.Usage)
			}
		})
	}
}

// A pi session's model is the one its last model change set, before any
// reply of the new model's has said so.
func TestAPiSessionReadsTheModelItLastChangedTo(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := piSession(t, home, piHeader(),
		piMessageEntry("u1", "", "user", object{"content": "work"}),
		piMessageEntry("a1", "u1", "assistant", object{"provider": "acme", "model": "thinker-1", "usage": object{"input": 200, "output": 50, "totalTokens": 250}, "content": []object{{"type": "text", "text": "done"}}}),
		piEntry("model_change", "m1", "a1", object{"provider": "openrouter", "modelId": "deepseek/deepseek-v4.1-flash"}))
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if page.Usage == nil || page.Usage.Model != "openrouter/deepseek/deepseek-v4.1-flash" {
		t.Fatalf("usage = %+v, want the model it changed to", page.Usage)
	}
}

// A usage limit's notice is an entry Claude Code writes itself, under the
// model "<synthetic>" with no usage: the session keeps the model it runs and
// how full its context is (the Overlord, 2026-10-28: the box showed
// "<synthetic>").
func TestAClaudeLimitNoticeKeepsTheModelAndTheContext(t *testing.T) {
	// Arrange
	home := t.TempDir()
	worked := answer("a1", "reply-1", textBlock("done"))
	worked["message"].(object)["usage"] = object{"input_tokens": 10, "output_tokens": 5, "cache_read_input_tokens": 3}
	limit := answer("a2", "reply-2", textBlock("You've hit your 5-hour limit · resets 10pm (America/Chicago)"))
	limit["message"].(object)["model"] = "<synthetic>"
	limit["message"].(object)["usage"] = object{"input_tokens": 0, "output_tokens": 0}
	path := conversation(t, home, prompted("u1", "work"), worked, prompted("u2", "more"), limit)
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{InputTokens: 10, OutputTokens: 5, CacheReadTokens: 3, ContextTokens: 18, Model: "claude-opus-5-5"}
	if page.Usage == nil || *page.Usage != want {
		t.Fatalf("usage = %+v, want %+v", page.Usage, want)
	}
}
