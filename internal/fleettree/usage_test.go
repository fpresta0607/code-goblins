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
