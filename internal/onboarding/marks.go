package onboarding

import "strings"

// DrawsUnicode reports whether the console announces that it draws symbols
// beyond its code page: Windows Terminal, VS Code's terminal, or a TERM that
// names a modern emulator. getenv reads the environment. The agents decide
// the same way, which is why Claude Code marks a dialog's focus with a plain
// > in a console that announces none of these.
func DrawsUnicode(getenv func(string) string) bool {
	return getenv("WT_SESSION") != "" || getenv("TERM_PROGRAM") == "vscode" || strings.HasPrefix(getenv("TERM"), "xterm")
}

// Marks are the symbols the quick start draws: the tick of a finished step,
// and each agent's own mark on its tab.
type Marks struct {
	Tick   string
	Agents map[string]string
}

// MarksFor returns the quick start's symbols for a console that draws
// Unicode, or else the ones every Windows console font has.
func MarksFor(unicode bool) Marks {
	if unicode {
		return Marks{Tick: "✓", Agents: map[string]string{"claude": "✻", "codex": ">_", "pi": "π"}}
	}
	return Marks{Tick: "√", Agents: map[string]string{"claude": "*", "codex": ">_", "pi": "π"}}
}
