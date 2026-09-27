package guard

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// sessionTools are tools a Claude Code session spends its time in, none of
// which the guard refuses.
var sessionTools = []string{
	"Bash", "Read", "Edit", "Write", "Grep", "Glob", "NotebookEdit", "WebFetch", "WebSearch",
	"Skill", "ToolSearch", "TodoWrite", "PowerShell", "LSP", "EnterPlanMode", "ExitPlanMode",
	"PushNotification",
}

// spelled writes word with sep between its letters, a spelling ClassifySubagent
// normalizes back to word.
func spelled(word, sep string) string {
	return strings.Join(strings.Split(word, ""), sep)
}

// A pre-tool hook costs each tool call it matches a process start, so the
// pretool-subagent matcher selects every tool the guard can refuse, however
// the name is spelled, and none of the tools a session spends its time in.
func TestHookMatcherSelectsEveryToolTheGuardCanRefuse(t *testing.T) {
	// Arrange: Claude Code tests a matcher that holds a regular-expression
	// character as an unanchored JavaScript regular expression, which reads
	// these character classes and alternatives exactly as Go's regexp does.
	pattern := HookMatcher()
	matcher := regexp.MustCompile(pattern)
	names := []string{
		"Agent", "AskUserQuestion", "CronCreate", "CronDelete", "CronList", "EnterWorktree",
		"ExitWorktree", "ListAgents", "Monitor", "RemoteTrigger", "ScheduleWakeup", "SendMessage",
		"Task", "TaskCreate", "TaskGet", "TaskList", "TaskOutput", "TaskStop", "TaskUpdate", "Workflow",
	}
	for _, word := range append(slices.Clone(delegationStems), "askuserquestion") {
		names = append(names, word, strings.ToUpper(word), "My"+strings.ToUpper(word[:1])+word[1:]+"Tool", spelled(word, "_"), spelled(word, "-"), spelled(word, " "))
	}

	// Act and Assert
	for _, name := range names {
		_, refused := ClassifySubagent(name)
		if (refused || ClassifyNativePrompt(name)) && !matcher.MatchString(name) {
			t.Errorf("the guard refuses %q but the matcher does not select it", name)
		}
	}
	for _, name := range sessionTools {
		if matcher.MatchString(name) {
			t.Errorf("the matcher selects %q, which the guard never refuses", name)
		}
	}
	if !strings.ContainsAny(pattern, "[]*") {
		t.Errorf("matcher %q would be read as a list of exact names, not a regular expression", pattern)
	}
}
