package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type codexFake struct {
	base           *agentFake
	screen         string
	keys           int
	submitWorks    bool
	occupiedBefore bool
}

func (f *codexFake) Run(ctx context.Context, r execx.Request) (execx.Result, error) {
	command := strings.Join(r.Args, " ")
	switch {
	case strings.HasPrefix(command, "pane process-info"):
		return execx.Result{Stdout: []byte(`{"result":{"process_info":{"shell_pid":10,"foreground_process_group_id":20}}}`)}, nil
	case strings.HasPrefix(command, "pane read"):
		if f.base.promptCalls == 0 && !f.occupiedBefore {
			return execx.Result{Stdout: []byte("\n› Ask Codex to do anything\n")}, nil
		}
		return execx.Result{Stdout: []byte(f.screen)}, nil
	case strings.HasPrefix(command, "pane send-keys"):
		f.keys++
		if f.submitWorks {
			f.screen = "\n• Messages to be submitted after next tool call\n  ↳ retain the old gate\n› Ask Codex to do anything\n"
		}
		return execx.Result{Stdout: []byte(`{"result":{}}`)}, nil
	}
	result, err := f.base.Run(ctx, r)
	result.Stdout = []byte(strings.ReplaceAll(string(result.Stdout), `"claude"`, `"codex"`))
	return result, err
}

func TestCodexExistingComposerIsNotAppendedOrSubmitted(t *testing.T) {
	base := newAgentFake(agentFake{status: "working"})
	runner := &codexFake{base: base, screen: "\n› earlier pending answer\n tab to queue message", occupiedBefore: true}
	sender := newAgentSender(base)
	sender.Herdr.Commands = runner
	err := sender.Text(context.Background(), "task-7", "later wake")
	if err == nil || !strings.Contains(err.Error(), "not sent") || base.promptCalls != 0 || runner.keys != 0 {
		t.Fatalf("occupied composer mutated: %v prompts=%d keys=%d", err, base.promptCalls, runner.keys)
	}
}

func TestCodexTypedQueuedAndUnknownSubmission(t *testing.T) {
	for _, tc := range []struct {
		name, screen, stage string
		works               bool
		keys                int
	}{
		{"typed then submitted", "\n› retain the old gate\n  tab to queue message", "submitted", true, 1},
		{"already queued", "\n• Messages to be submitted after next tool call\n  ↳ retain the old gate\n› Ask Codex to do anything\n", "submitted", false, 0},
		{"Enter unsupported", "\n› retain the old gate\n  tab to queue message", "typed", false, 1},
		{"tool output not composer", "tool output: retain the old gate\n› Ask Codex to do anything\n", "", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := newAgentFake(agentFake{status: "working"})
			runner := &codexFake{base: base, screen: tc.screen, submitWorks: tc.works}
			sender := newAgentSender(base)
			sender.Herdr.Commands = runner
			err := sender.Text(context.Background(), "task-7", "retain the old gate")
			if err == nil {
				t.Fatal("busy counters falsely proved acceptance")
			}
			var receipt *DeliveryError
			if tc.stage != "" && (!errors.As(err, &receipt) || !strings.HasPrefix(receipt.Stage, tc.stage)) {
				t.Fatalf("receipt %v", err)
			}
			if base.promptCalls != 1 || runner.keys != tc.keys {
				t.Fatalf("prompt calls %d keys %d", base.promptCalls, runner.keys)
			}
		})
	}
}
