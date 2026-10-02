package harness

import "testing"

func TestBriefInstructionIsSingleLiteralArgument(t *testing.T) {
	if got, want := BriefInstruction(`C:\briefs\task.md`), `Read the brief at C:\briefs\task.md and follow it exactly.`; got != want {
		t.Errorf("BriefInstruction() = %q, want %q", got, want)
	}
}
