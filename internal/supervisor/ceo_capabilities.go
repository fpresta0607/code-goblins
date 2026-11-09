package supervisor

// CEOCapability is what a CEO running one harness gets from the fleet, and
// only what has been proved. It is the one table the quick start, the board's
// first-run page, the doctor and the docs read, so no surface says more than
// another.
type CEOCapability struct {
	// Agent is the harness by its id, and Name by the name every surface
	// shows it by.
	Agent string
	Name  string
	// Recommended marks the one harness the fleet recommends for the CEO.
	Recommended bool
	// Wake is how a goblin's report reaches the CEO.
	Wake CEOWake
	// Registers is how the CEO becomes the home's registered CEO.
	Registers string
	// Resumes is whether a closed CEO comes back on its conversation.
	Resumes bool
	// Note is the few words the choice of agent shows for the harness.
	Note string
	// Lacks is what a CEO in this harness goes without, each in plain words,
	// and empty for the harness that goes without nothing.
	Lacks []string
}

// CEOCapabilities is the table, in the order every surface shows the
// harnesses: Claude Code first, the recommended one. A Codex or pi CEO is
// woken by a line typed into its native terminal and registers with its first
// prompt, since the fleet's hooks for those harnesses only report their
// prompts: nothing gives them the session-start digest and nothing guards
// their turns. Both were proved with real harnesses in scratch homes on
// 2026-10-02, and a closed Codex CEO coming back on its conversation with
// them (TestACEOStartedAsGoblinsStartsItRegistersIsWokenAndComesBack). A
// closed pi CEO comes back on its session with pi --session, as a pi goblin
// does since pi 0.85.1, once pi has written that session's file.
func CEOCapabilities() []CEOCapability {
	typed := []string{
		"it is woken only in a native terminal, where the wake line is typed while it sits idle at an empty prompt",
		"no session-start digest: it runs ceo session-start to validate current memory and retrieves relevant cold authority with ceo memory retrieve",
		"no turn-end guard and no pre-tool guards",
	}
	return []CEOCapability{
		{Agent: "claude", Name: "Claude Code", Recommended: true, Wake: CEOWakeFor("claude"), Registers: "its SessionStart hook registers it", Resumes: true, Note: "the best experience"},
		{Agent: "codex", Name: "Codex", Wake: CEOWakeFor("codex"), Registers: "its first prompt runs ceo register", Resumes: true, Note: "woken by a typed line; no digest or guards", Lacks: typed},
		{Agent: "pi", Name: "pi", Wake: CEOWakeFor("pi"), Registers: "its first prompt runs ceo register", Resumes: true, Note: "woken by a typed line; no digest or guards", Lacks: typed},
	}
}

// CEOCapabilityFor is the table's row for agent, and false for a harness no
// CEO runs in.
func CEOCapabilityFor(agent string) (CEOCapability, bool) {
	for _, capability := range CEOCapabilities() {
		if capability.Agent == agent {
			return capability, true
		}
	}
	return CEOCapability{}, false
}
