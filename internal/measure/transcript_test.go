package measure

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	windowFrom = time.Date(2026, 11, 13, 6, 0, 0, 0, time.UTC)
	windowTo   = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
)

// at is a moment minutes into the window.
func at(minutes int) time.Time { return windowFrom.Add(time.Duration(minutes) * time.Minute) }

// writeLines writes each line whole, ended as its harness ends it.
func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func claudeAnswer(id, model string, minutes int, input, cacheCreation, cacheRead, output int) string {
	return `{"type":"assistant","timestamp":"` + at(minutes).Format(time.RFC3339Nano) + `","message":{"id":"` + id + `","model":"` + model + `","usage":{"input_tokens":` + strconv.Itoa(input) + `,"cache_creation_input_tokens":` + strconv.Itoa(cacheCreation) + `,"cache_read_input_tokens":` + strconv.Itoa(cacheRead) + `,"output_tokens":` + strconv.Itoa(output) + `}}}`
}

// A Claude Code transcript repeats an answer's usage on each of its blocks,
// so the answer counts once, with its cache reads and writes in its input;
// a notice the harness wrote itself, an answer outside the window and a last
// line still being written count for nothing, and each model is its own.
func TestClaudeSpendCountsEachAnswerOnceForItsModelInTheWindow(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeLines(t, path,
		claudeAnswer("before", "claude-opus-5-5", -5, 999, 999, 999, 999),
		`{"type":"user","timestamp":"`+at(1).Format(time.RFC3339Nano)+`","message":{"role":"user","content":"go"}}`,
		claudeAnswer("a", "claude-opus-5-5", 2, 10, 20, 300, 40),
		claudeAnswer("a", "claude-opus-5-5", 2, 10, 20, 300, 40),
		claudeAnswer("limit", syntheticModel, 3, 0, 0, 0, 0),
		claudeAnswer("b", "claude-sonnet-5-5", 4, 1, 2, 3, 4),
		claudeAnswer("c", "claude-opus-5-5", 30, 5, 0, 100, 6),
		claudeAnswer("after", "claude-opus-5-5", 200, 999, 999, 999, 999),
	)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"type":"assistant","timestamp":"` + at(40).Format(time.RFC3339Nano) + `","message":{"id":"partial","model":"claude-opus-5-5","usage":{"input_tokens":7`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	// Act
	spends, err := ClaudeSpend(path, windowFrom, windowTo)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []Spend{
		{Model: "claude-opus-5-5", Requests: 2, Usage: Usage{InputTokens: 435, OutputTokens: 46, CacheReadTokens: 400}, First: at(2), Last: at(30)},
		{Model: "claude-sonnet-5-5", Requests: 1, Usage: Usage{InputTokens: 6, OutputTokens: 4, CacheReadTokens: 3}, First: at(4), Last: at(4)},
	}
	if !reflect.DeepEqual(spends, want) {
		t.Errorf("spends =\n%+v\nwant\n%+v", spends, want)
	}
}

func codexTurn(minutes int, model, effort string) string {
	return `{"timestamp":"` + at(minutes).Format(time.RFC3339Nano) + `","type":"turn_context","payload":{"model":"` + model + `","effort":"` + effort + `"}}`
}

func codexCount(minutes int, total string, last string) string {
	info := `{`
	if total != "" {
		info += `"total_token_usage":` + total
	}
	if last != "" {
		if total != "" {
			info += `,`
		}
		info += `"last_token_usage":` + last
	}
	return `{"timestamp":"` + at(minutes).Format(time.RFC3339Nano) + `","type":"event_msg","payload":{"type":"token_count","info":` + info + `}}}`
}

// A Codex rollout's token counts carry the thread's running total, so each
// request spent what the total moved since the count before it, counted
// before the window too, on the model and effort of its turn; a count that
// repeats the total spent nothing, and one with no total adds its own.
func TestCodexSpendTakesEachRequestFromTheRunningTotal(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeLines(t, path,
		`{"timestamp":"`+at(-10).Format(time.RFC3339Nano)+`","type":"session_meta","payload":{"id":"lead"}}`,
		codexTurn(-10, "gpt-6-astra", "high"),
		codexCount(-9, `{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":100}`, ""),
		codexCount(5, `{"input_tokens":1500,"cached_input_tokens":1100,"output_tokens":150}`, ""),
		codexCount(6, `{"input_tokens":1500,"cached_input_tokens":1100,"output_tokens":150}`, ""),
		codexTurn(10, "gpt-6.1-sol", "medium"),
		codexCount(12, `{"input_tokens":1700,"cached_input_tokens":1200,"output_tokens":170}`, ""),
		codexCount(13, "", `{"input_tokens":30,"cached_input_tokens":10,"output_tokens":3}`),
		codexCount(500, `{"input_tokens":9000,"cached_input_tokens":9000,"output_tokens":900}`, ""),
	)

	// Act
	spends, err := CodexSpend(path, windowFrom, windowTo)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []Spend{
		{Model: "gpt-6-astra", Effort: "high", Requests: 1, Usage: Usage{InputTokens: 500, OutputTokens: 50, CacheReadTokens: 300}, First: at(5), Last: at(5)},
		{Model: "gpt-6.1-sol", Effort: "medium", Requests: 2, Usage: Usage{InputTokens: 230, OutputTokens: 23, CacheReadTokens: 110}, First: at(12), Last: at(13)},
	}
	if !reflect.DeepEqual(spends, want) {
		t.Errorf("spends =\n%+v\nwant\n%+v", spends, want)
	}
}

// A pi answer's input leaves out its cache reads and writes, so they are
// added to it, on the thinking level the session last set.
func TestPiSpendAddsTheCacheToTheInputOnTheThinkingLevel(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "session.jsonl")
	answer := func(minutes int, model string) string {
		return `{"type":"message","timestamp":"` + at(minutes).Format(time.RFC3339Nano) + `","message":{"role":"assistant","model":"` + model + `","usage":{"input":100,"output":20,"cacheRead":30,"cacheWrite":5}}}`
	}
	writeLines(t, path,
		`{"type":"thinking_level_change","timestamp":"`+at(1).Format(time.RFC3339Nano)+`","thinkingLevel":"high"}`,
		answer(2, "deepseek/deepseek-v4"),
		`{"type":"message","timestamp":"`+at(3).Format(time.RFC3339Nano)+`","message":{"role":"user","content":"more"}}`,
		answer(4, "deepseek/deepseek-v4"),
		answer(-4, "deepseek/deepseek-v4"),
	)

	// Act
	spends, err := PiSpend(path, windowFrom, windowTo)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []Spend{{Model: "deepseek/deepseek-v4", Effort: "high", Requests: 2, Usage: Usage{InputTokens: 270, OutputTokens: 40, CacheReadTokens: 60}, First: at(2), Last: at(4)}}
	if !reflect.DeepEqual(spends, want) {
		t.Errorf("spends =\n%+v\nwant\n%+v", spends, want)
	}
}

// Each sub-agent of a Claude Code conversation is read from its own
// transcript beside its parent's, with the type its record names; one
// whose type is not recorded has none, and one that spent nothing in the
// window is none of the run's.
func TestClaudeWorkersReadsEachSubagentWithItsType(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	lead := filepath.Join(dir, "session.jsonl")
	writeLines(t, lead, claudeAnswer("lead", "claude-opus-5-5", 1, 1, 0, 0, 1))
	subagents := filepath.Join(dir, "session", "subagents")
	writeLines(t, filepath.Join(subagents, "agent-a.jsonl"), claudeAnswer("a", "claude-sonnet-5-5", 5, 10, 0, 0, 2))
	writeLines(t, filepath.Join(subagents, "agent-a.meta.json"), `{"agentType":"ceo-implement","toolUseId":"toolu_a"}`)
	writeLines(t, filepath.Join(subagents, "agent-b.jsonl"), claudeAnswer("b", "claude-haiku-5-5", 6, 4, 0, 0, 1))
	writeLines(t, filepath.Join(subagents, "agent-old.jsonl"), claudeAnswer("old", "claude-sonnet-5-5", -60, 9, 0, 0, 9))
	writeLines(t, filepath.Join(subagents, "agent-old.meta.json"), `{"agentType":"Explore"}`)

	// Act
	workers, err := ClaudeWorkers(lead, windowFrom, windowTo)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []Worker{
		{Agent: "ceo-implement", Spends: []Spend{{Model: "claude-sonnet-5-5", Requests: 1, Usage: Usage{InputTokens: 10, OutputTokens: 2}, First: at(5), Last: at(5)}}},
		{Agent: "", Spends: []Spend{{Model: "claude-haiku-5-5", Requests: 1, Usage: Usage{InputTokens: 4, OutputTokens: 1}, First: at(6), Last: at(6)}}},
	}
	if !reflect.DeepEqual(workers, want) {
		t.Errorf("workers =\n%+v\nwant\n%+v", workers, want)
	}
}

// A Codex child is a rollout that opens on the lead's thread as the one that
// spawned it, filed by the day it started, in the role it was spawned in; a
// rollout of another thread is no child of the lead's.
func TestCodexWorkersReadsTheChildrenOfTheLeadsThread(t *testing.T) {
	// Arrange
	home := t.TempDir()
	day := filepath.Join(home, codexSessions, "2026", "11", "13")
	lead := filepath.Join(day, "rollout-lead.jsonl")
	writeLines(t, lead, `{"type":"session_meta","payload":{"id":"lead-thread","source":"cli"}}`)
	writeLines(t, filepath.Join(day, "rollout-child.jsonl"),
		`{"type":"session_meta","payload":{"id":"child","source":{"subagent":{"thread_spawn":{"parent_thread_id":"lead-thread","agent_role":"worker"}}}}}`,
		codexTurn(20, "gpt-6.1-luna", "medium"),
		codexCount(21, `{"input_tokens":40,"cached_input_tokens":0,"output_tokens":4}`, ""),
	)
	writeLines(t, filepath.Join(day, "rollout-stranger.jsonl"),
		`{"type":"session_meta","payload":{"id":"stranger","parent_thread_id":"another-thread"}}`,
		codexTurn(20, "gpt-6.1-luna", "medium"),
		codexCount(21, `{"input_tokens":99,"cached_input_tokens":0,"output_tokens":9}`, ""),
	)

	// Act
	workers, err := CodexWorkers(home, lead, windowFrom, windowTo)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []Worker{{Agent: "worker", Spends: []Spend{{Model: "gpt-6.1-luna", Effort: "medium", Requests: 1, Usage: Usage{InputTokens: 40, OutputTokens: 4}, First: at(21), Last: at(21)}}}}
	if !reflect.DeepEqual(workers, want) {
		t.Errorf("workers =\n%+v\nwant\n%+v", workers, want)
	}
}
