package measure

import (
	"reflect"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/pipeline"
)

// spent is a conversation's spend on one model and effort, from a request
// first minutes into the window to one last minutes into it.
func spent(model, effort string, input, output int64, first, last int) Spend {
	return Spend{Model: model, Effort: effort, Usage: Usage{InputTokens: input, OutputTokens: output}, First: at(first), Last: at(last)}
}

// Each spend of a run counts for the role that spent it: the goblin's own
// conversation for the lead, its implement worker for implement, any other
// worker as a helper of its type, and each gate call by its purpose, with
// every Test step that passed or failed on the model of its last test agent.
func TestBuildCountsEachSpendForTheRoleThatSpentIt(t *testing.T) {
	cases := []struct {
		name  string
		input Input
		want  Record
	}{
		{
			name: "the conversation is the lead's, with the lane's effort on the lane's model alone",
			input: Input{
				Task: "task", Project: "project", Outcome: "done", PR: "https://example.test/pull/1", From: windowFrom, To: windowTo,
				Harness: "claude", LeadModel: "opus", LeadEffort: "xhigh",
				Lead:       []Spend{spent("claude-opus-5-5", "", 900, 90, 0, 30), spent("claude-sonnet-5-5", "", 10, 1, 40, 40)},
				Unmeasured: []string{"gate: unreadable"},
			},
			want: Record{
				Task: "task", Project: "project", Outcome: "done", PR: "https://example.test/pull/1", From: windowFrom, To: windowTo,
				Roles: []Role{
					{Role: RoleLead, Harness: "claude", Model: "claude-opus-5-5", Effort: "xhigh", Calls: 1, Usage: Usage{InputTokens: 900, OutputTokens: 90}, WallSeconds: 1800},
					{Role: RoleLead, Harness: "claude", Model: "claude-sonnet-5-5", Calls: 1, Usage: Usage{InputTokens: 10, OutputTokens: 1}},
				},
				Unmeasured: []string{"gate: unreadable"},
			},
		},
		{
			name: "a Claude Code ceo-implement worker implements and every other is a helper of its type",
			input: Input{
				Harness: "claude", ImplementModel: "sonnet", ImplementEffort: "high",
				Workers: []Worker{
					{Agent: claudeImplementAgent, Spends: []Spend{spent("claude-sonnet-5-5", "", 400, 40, 1, 11)}},
					{Agent: claudeImplementAgent, Spends: []Spend{spent("claude-sonnet-5-5", "", 200, 20, 20, 21)}},
					{Agent: "Explore", Spends: []Spend{spent("claude-haiku-5-5", "", 5, 1, 2, 2)}},
					{Spends: []Spend{spent("claude-sonnet-5-5", "", 7, 1, 3, 3)}},
				},
			},
			want: Record{Roles: []Role{
				{Role: RoleImplement, Harness: "claude", Model: "claude-sonnet-5-5", Effort: "high", Calls: 2, Usage: Usage{InputTokens: 600, OutputTokens: 60}, WallSeconds: 660},
				{Role: RoleHelper, Harness: "claude", Model: "claude-haiku-5-5", Agent: "Explore", Calls: 1, Usage: Usage{InputTokens: 5, OutputTokens: 1}},
				{Role: RoleHelper, Harness: "claude", Model: "claude-sonnet-5-5", Calls: 1, Usage: Usage{InputTokens: 7, OutputTokens: 1}},
			}},
		},
		{
			name: "a Codex child implements only on the implement lane's model and effort",
			input: Input{
				Harness: "codex", ImplementModel: "gpt-6.1-luna", ImplementEffort: "medium",
				Workers: []Worker{
					{Agent: "worker", Spends: []Spend{spent("gpt-6.1-luna", "medium", 500, 50, 0, 10), spent("gpt-6.1-luna", "high", 1, 1, 12, 12)}},
					{Agent: "worker", Spends: []Spend{spent("gpt-6.1-sol", "medium", 2, 1, 14, 14)}},
				},
			},
			want: Record{Roles: []Role{
				{Role: RoleImplement, Harness: "codex", Model: "gpt-6.1-luna", Effort: "medium", Calls: 1, Usage: Usage{InputTokens: 500, OutputTokens: 50}, WallSeconds: 600},
				{Role: RoleHelper, Harness: "codex", Model: "gpt-6.1-luna", Effort: "high", Agent: "worker", Calls: 1, Usage: Usage{InputTokens: 1, OutputTokens: 1}},
				{Role: RoleHelper, Harness: "codex", Model: "gpt-6.1-sol", Effort: "medium", Agent: "worker", Calls: 1, Usage: Usage{InputTokens: 2, OutputTokens: 1}},
			}},
		},
		{
			name: "a Codex child is a helper when the goblin implements its own work",
			input: Input{
				Harness: "codex",
				Workers: []Worker{{Agent: "worker", Spends: []Spend{spent("gpt-6.1-luna", "medium", 3, 1, 0, 0)}}},
			},
			want: Record{Roles: []Role{
				{Role: RoleHelper, Harness: "codex", Model: "gpt-6.1-luna", Effort: "medium", Agent: "worker", Calls: 1, Usage: Usage{InputTokens: 3, OutputTokens: 1}},
			}},
		},
		{
			name: "a gate call counts by its purpose and each Test step's outcome on its last test agent's model",
			input: Input{
				Harness: "claude",
				Gate: []pipeline.GateCalls{
					{Run: "first", Purpose: "review", Agent: "claude", Model: "claude-opus-5-5", Calls: 2, DurationMS: 90000, InputTokens: 1500, OutputTokens: 150, CacheReadTokens: 1200, Findings: 4},
					{Run: "first", Purpose: "review-coverage", Agent: "claude", Model: "claude-opus-5-5", Calls: 1, DurationMS: 10000, InputTokens: 100, OutputTokens: 10, Findings: 1},
					{Run: "first", Purpose: "review-fix", Agent: "claude", Model: "claude-opus-5-5", Calls: 1, DurationMS: 45000, Untallied: 1},
					{Run: "first", Purpose: "test", Agent: "kimi", Model: "kimi-k3", Calls: 1, DurationMS: 20000, InputTokens: 300, OutputTokens: 30},
					{Run: "second", Purpose: "test-fix", Agent: "claude", Model: "claude-opus-5-5", Calls: 1, DurationMS: 5000, InputTokens: 50, OutputTokens: 5},
					{Run: "second", Purpose: "test", Agent: "kimi", Model: "kimi-k3", Calls: 1, DurationMS: 4000, InputTokens: 40, OutputTokens: 4},
					{Run: "second", Purpose: "document", Agent: "claude", Model: "claude-opus-5-5", Calls: 1, DurationMS: 3000, InputTokens: 30, OutputTokens: 3},
				},
				Tests: []pipeline.GateTest{
					{Run: "first", Status: "failed", Agent: "kimi", Model: "kimi-k3"},
					{Run: "second", Status: "completed", Agent: "kimi", Model: "kimi-k3"},
					{Run: "running", Status: "running", Agent: "kimi", Model: "kimi-k3"},
					{Run: "untested"},
				},
			},
			want: Record{
				Runs: []string{"first", "second", "running", "untested"},
				Roles: []Role{
					{Role: RoleReview, Harness: "claude", Model: "claude-opus-5-5", Calls: 3, Usage: Usage{InputTokens: 1600, OutputTokens: 160, CacheReadTokens: 1200}, WallSeconds: 100, Findings: 5},
					{Role: RoleFix, Harness: "claude", Model: "claude-opus-5-5", Agent: "review-fix", Calls: 1, Untallied: 1, WallSeconds: 45},
					{Role: RoleVerify, Harness: "kimi", Model: "kimi-k3", Calls: 2, Usage: Usage{InputTokens: 340, OutputTokens: 34}, WallSeconds: 24, Passed: 1, Failed: 1},
					{Role: RoleFix, Harness: "claude", Model: "claude-opus-5-5", Agent: "test-fix", Calls: 1, Usage: Usage{InputTokens: 50, OutputTokens: 5}, WallSeconds: 5},
					{Role: RoleGate, Harness: "claude", Model: "claude-opus-5-5", Agent: "document", Calls: 1, Usage: Usage{InputTokens: 30, OutputTokens: 3}, WallSeconds: 3},
				},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Act
			record := Build(testCase.input)

			// Assert
			if !reflect.DeepEqual(record, testCase.want) {
				t.Errorf("record =\n%+v\nwant\n%+v", record, testCase.want)
			}
		})
	}
}
