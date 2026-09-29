package spawn

import (
	"context"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestRecoveryResumesTheExactSessionAndKeepsTaskSettings(t *testing.T) {
	fixture := newSwitchFixture(t)
	fixture.runner.stopAfter = 0
	const session = "abc01234-5678-9012-abcd-012345678901"
	result, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, ResumeSession: session})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Resumed || len(fixture.runner.startArgs) < 2 || !slices.Equal(fixture.runner.startArgs[:2], []string{"--resume", session}) || slices.Contains(fixture.runner.startArgs, "--continue") {
		t.Fatalf("result=%+v args=%q", result, fixture.runner.startArgs)
	}
	meta, err := state.ReadTaskMeta(fixture.stateDir, fixture.meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Model != fixture.meta.Model || meta.Effort != fixture.meta.Effort || meta.Worktree != fixture.meta.Worktree || meta.ResumeSession != session {
		t.Fatalf("changed task settings: %+v", meta)
	}
}

func TestRecoveryRejectsAnInvalidSessionBeforeStoppingTheTask(t *testing.T) {
	fixture := newSwitchFixture(t)
	_, err := fixture.service.Switch(context.Background(), SwitchRequest{ID: fixture.meta.ID, ResumeSession: "--last"})
	if err == nil || fixture.runner.stopped || fixture.runner.restarted {
		t.Fatalf("err=%v stopped=%t restarted=%t", err, fixture.runner.stopped, fixture.runner.restarted)
	}
}
