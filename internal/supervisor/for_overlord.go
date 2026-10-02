package supervisor

// forOverlord reports whether a question is the Overlord's to answer. The
// Overlord, 2026-10-02: "The Command Center should only give me questions
// that the CFO has for the Overlord, for me." His are a question the CFO
// asks him and what the CFO passes up, both of which name no task. A
// goblin's question is the CFO's to answer and never his: it does not wait
// on him, alert him or open anything, and it is held for nobody while he is
// away. Every other item is his as it stands: a review item, a goblin's wait
// or page addressed to him, a command and a credential request.
func forOverlord(q Question) bool {
	return q.Task == ""
}
