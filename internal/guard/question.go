package guard

import "strings"

// nativePromptTools are the harness tools that stop a session on a question
// only its own terminal shows. Claude Code's is AskUserQuestion.
var nativePromptTools = map[string]bool{
	"askuserquestion": true,
}

// ClassifyNativePrompt reports whether tool is a harness-native question
// prompt, named the way ClassifySubagent names tools: lowercased and stripped
// to [a-z0-9]. MCP tools are never one.
func ClassifyNativePrompt(tool string) bool {
	if strings.HasPrefix(tool, "mcp__") {
		return false
	}
	var b strings.Builder
	for _, r := range strings.ToLower(tool) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return nativePromptTools[b.String()]
}
