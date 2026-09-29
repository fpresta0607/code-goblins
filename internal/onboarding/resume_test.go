package onboarding

import (
	"slices"
	"testing"
)

func TestResumeNamesAnExactConversationForEveryAgent(t *testing.T) {
	const session = "abc01234-5678-9012-abcd-012345678901"
	for _, test := range []struct {
		name string
		want []string
	}{
		{"claude", []string{"--resume", session}},
		{"codex", []string{"resume", session}},
		{"pi", []string{"--session", session}},
	} {
		args, err := ResumeArgs(test.name, session)
		if err != nil || !slices.Equal(args, test.want) {
			t.Fatalf("%s: args=%q err=%v", test.name, args, err)
		}
	}
}

func TestResumeRefusesMissingAmbiguousOrUnsafeIdentity(t *testing.T) {
	for _, session := range []string{"", "latest", "abc", "../session", "--continue", "a\nother", "a&exit"} {
		for _, name := range []string{"claude", "codex", "pi"} {
			if _, err := ResumeArgs(name, session); err == nil {
				t.Errorf("accepted %s %q", name, session)
			}
		}
	}
	if _, err := ResumeArgs("unknown", "abc01234-5678-9012-abcd-012345678901"); err == nil {
		t.Fatal("accepted unknown agent")
	}
}
