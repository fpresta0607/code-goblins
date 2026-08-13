package guard

import "testing"

func TestClassifySubagent(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		wantDeny bool
		wantStem string
	}{
		{name: "Agent denies on stem agent", tool: "Agent", wantDeny: true, wantStem: "agent"},
		{name: "SendMessage denies on sendmessage", tool: "SendMessage", wantDeny: true, wantStem: "sendmessage"},
		{name: "TaskCreate allows (plan-only)", tool: "TaskCreate", wantDeny: false},
		{name: "TaskUpdate allows (plan-only)", tool: "TaskUpdate", wantDeny: false},
		{name: "TaskOutput allows (observe-only)", tool: "TaskOutput", wantDeny: false},
		{name: "TaskStop allows", tool: "TaskStop", wantDeny: false},
		{name: "CronList allows (observe-only beats cron stem)", tool: "CronList", wantDeny: false},
		{name: "mcp__herdr__spawn_task allows (MCP)", tool: "mcp__herdr__spawn_task", wantDeny: false},
		{name: "Bash allows", tool: "Bash", wantDeny: false},
		{name: "Read allows", tool: "Read", wantDeny: false},
		{name: "CronCreate denies on cron", tool: "CronCreate", wantDeny: true, wantStem: "cron"},
		{name: "EnterWorktree denies on worktree", tool: "EnterWorktree", wantDeny: true, wantStem: "worktree"},
		{name: "Workflow denies on workflow", tool: "Workflow", wantDeny: true, wantStem: "workflow"},
		{name: "case-insensitivity: AGENT denies", tool: "AGENT", wantDeny: true, wantStem: "agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stem, deny := ClassifySubagent(tt.tool)
			if deny != tt.wantDeny {
				t.Errorf("ClassifySubagent(%q) deny = %v, want %v (stem=%q)", tt.tool, deny, tt.wantDeny, stem)
			}
			if tt.wantDeny && stem != tt.wantStem {
				t.Errorf("ClassifySubagent(%q) stem = %q, want %q", tt.tool, stem, tt.wantStem)
			}
		})
	}
}
