package supervisor

// CFOCapability is what a CFO running one harness gets from the fleet, and
// only what has been proved. It is the one table the quick start, the board's
// first-run page, the doctor and the docs read, so no surface says more than
// another.
type CFOCapability struct {
	// Agent is the harness by its id, and Name by the name every surface
	// shows it by.
	Agent string
	Name  string
	// Recommended marks the one harness the fleet recommends for the CFO.
	Recommended bool
	// Wake is how a goblin's report reaches the CFO.
	Wake CFOWake
	// Registers is how the CFO becomes the home's registered CFO.
	Registers string
	// Resumes is whether a closed CFO comes back on its conversation.
	Resumes bool
	// Note is the few words the choice of agent shows for the harness.
	Note string
	// Lacks is what a CFO in this harness goes without, each in plain words,
	// and empty for the harness that goes without nothing.
	Lacks []string
}

// CFOCapabilities is the table, in the order every surface shows the
// harnesses: Claude Code first, the recommended one. A Codex or pi CFO is
// woken by a line typed into its native terminal and registers with its first
// prompt, since the fleet's hooks for those harnesses only report their
// prompts: nothing gives them the session-start digest and nothing guards
// their turns. Both were proved with real harnesses in scratch homes on
// 2026-10-02, and a closed Codex CFO coming back on its conversation with
// them (TestACFOStartedAsGoblinsStartsItRegistersIsWokenAndComesBack).
func CFOCapabilities() []CFOCapability {
	typed := []string{
		"it is woken only in a native terminal, where the wake line is typed while it sits idle at an empty prompt",
		"no session-start hook: it runs cfo session-start and reads data/overlord.md and the memory index itself",
		"no turn-end guard and no pre-tool guards",
	}
	return []CFOCapability{
		{Agent: "claude", Name: "Claude Code", Recommended: true, Wake: CFOWakeFor("claude"), Registers: "its SessionStart hook registers it", Resumes: true, Note: "the best experience"},
		{Agent: "codex", Name: "Codex", Wake: CFOWakeFor("codex"), Registers: "its first prompt runs cfo register", Resumes: true, Note: "woken by a typed line; no digest or guards", Lacks: typed},
		{Agent: "pi", Name: "pi", Wake: CFOWakeFor("pi"), Registers: "its first prompt runs cfo register", Note: "woken by a typed line; no digest, guards or resume", Lacks: append(append([]string{}, typed...), "a closed pi CFO starts a new conversation: it is not resumed")},
	}
}

// CFOCapabilityFor is the table's row for agent, and false for a harness no
// CFO runs in.
func CFOCapabilityFor(agent string) (CFOCapability, bool) {
	for _, capability := range CFOCapabilities() {
		if capability.Agent == agent {
			return capability, true
		}
	}
	return CFOCapability{}, false
}
