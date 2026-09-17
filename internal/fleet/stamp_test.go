package fleet

import (
	"context"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/routing"
)

// A steer is stamped with the same constant the fault detector excludes, so
// the words the CFO writes about a provider never read as the harness's own
// fault. Both this test and the detector's reach the prefix through
// routing.SteerPrefix; changing the constant moves both, and a stamp that
// stopped using it would fail here.
func TestSenderTextStampsTheSteerPrefixTheDetectorExcludes(t *testing.T) {
	fake := newAgentFake(agentFake{})
	message := "CI is failing on a GitHub Actions rate limit exceeded, not on you; keep going"
	if err := newAgentSender(fake).Text(context.Background(), "task-7", message); err != nil {
		t.Fatalf("Text: %v", err)
	}
	var prompt string
	for _, args := range fake.requests {
		if len(args) >= 4 && args[0] == "agent" && args[1] == "prompt" {
			prompt = args[3]
		}
	}
	if prompt != routing.SteerPrefix+message {
		t.Fatalf("prompt = %q, want the message stamped with routing.SteerPrefix", prompt)
	}
	pane := "❯ " + prompt + "\n  wrapped by the pane\n"
	if fault, _, found := routing.Detect(pane); found {
		t.Errorf("Detect read the stamped steer as %q", fault)
	}
}

func TestStampIsIdempotentAndLeavesCommandsAlone(t *testing.T) {
	if got := Stamp(Stamp("keep going")); got != routing.SteerPrefix+"keep going" {
		t.Errorf("Stamp twice = %q, want one prefix", got)
	}
	for _, command := range []string{"/exit", "/compact", "$ git status"} {
		if got := Stamp(command); got != command {
			t.Errorf("Stamp(%q) = %q, want a harness command untouched", command, got)
		}
	}
}
