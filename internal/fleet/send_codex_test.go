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
	beforeScreen   string
	keys           int
	submitWorks    bool
	occupiedBefore bool
	changeProcess  bool
}

func (f *codexFake) Run(ctx context.Context, r execx.Request) (execx.Result, error) {
	command := strings.Join(r.Args, " ")
	switch {
	case strings.HasPrefix(command, "pane process-info"):
		if f.changeProcess && f.base.promptCalls > 0 {
			return execx.Result{Stdout: []byte(`{"result":{"process_info":{"shell_pid":10,"foreground_process_group_id":21}}}`)}, nil
		}
		return execx.Result{Stdout: []byte(`{"result":{"process_info":{"shell_pid":10,"foreground_process_group_id":20}}}`)}, nil
	case strings.HasPrefix(command, "pane read"):
		if f.base.promptCalls == 0 {
			if f.beforeScreen != "" {
				return execx.Result{Stdout: []byte(f.beforeScreen)}, nil
			}
			if !f.occupiedBefore {
				return execx.Result{Stdout: []byte(styledEmptyComposer)}, nil
			}
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

func TestCodexIdentityChangeBeforeEnterRefusesSubmission(t *testing.T) {
	base := newAgentFake(agentFake{status: "working"})
	runner := &codexFake{base: base, screen: "\n› retain the old gate\n  tab to queue message", changeProcess: true}
	sender := newAgentSender(base)
	sender.Herdr.Commands = runner
	err := sender.Text(context.Background(), "task-7", "retain the old gate")
	var receipt *DeliveryError
	if !errors.As(err, &receipt) || !strings.Contains(receipt.Stage, "foreground harness not verified") || runner.keys != 0 {
		t.Fatalf("identity change receipt=%v keys=%d", err, runner.keys)
	}
}

const styledEmptyComposer = "\n\x1b[1m›\x1b[0m\x1b[38;5;45m⠁\x1b[0m\x1b[2mAsk Codex to do anything\x1b[0m\n  \x1b[38;5;81m⠙⠹\x1b[0m\n  gpt-5.6-sol high · C:\\fixture\\project · Synthetic task\n"

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

func TestCodexUnrecognizedComposerIsNotTypedOrSubmitted(t *testing.T) {
	for _, screen := range []string{
		"tool output without a composer",
		"\n› draft without the recognized queue hint\n",
		"\n› Ask Codex to do anything plus a draft\n",
		"\n› ⠋⠙ occupied Unicode draft\n  gpt-5.6-sol high · C:\\fixture\\project · Synthetic task\n",
		"\n› Ask Codex to do anything ⠋ draft\n  ⠙⠹\n  gpt-5.6-sol high · C:\\fixture\\project · Synthetic task\n",
	} {
		base := newAgentFake(agentFake{status: "working"})
		runner := &codexFake{base: base, screen: screen, occupiedBefore: true}
		sender := newAgentSender(base)
		sender.Herdr.Commands = runner
		err := sender.Text(context.Background(), "task-7", "later wake")
		if err == nil || !strings.Contains(err.Error(), "not sent") || base.promptCalls != 0 || runner.keys != 0 {
			t.Fatalf("unrecognized composer mutated: screen=%q err=%v prompts=%d keys=%d", screen, err, base.promptCalls, runner.keys)
		}
	}
}

func TestCodexRealShapedEmptyComposerSubmitsOnce(t *testing.T) {
	base := newAgentFake(agentFake{status: "working"})
	runner := &codexFake{
		base:         base,
		beforeScreen: styledEmptyComposer,
		screen:       "\n› retain the old gate\n  tab to queue message",
		submitWorks:  true,
	}
	sender := newAgentSender(base)
	sender.Herdr.Commands = runner
	err := sender.Text(context.Background(), "task-7", "retain the old gate")
	var receipt *DeliveryError
	if !errors.As(err, &receipt) || !strings.HasPrefix(receipt.Stage, "submitted") {
		t.Fatalf("real-shaped empty composer receipt=%v", err)
	}
	if base.promptCalls != 1 || runner.keys != 1 {
		t.Fatalf("real-shaped empty composer prompts=%d keys=%d", base.promptCalls, runner.keys)
	}
}

func TestCodexTypedQueuedAndUnknownSubmission(t *testing.T) {
	for _, tc := range []struct {
		name, screen, stage string
		works               bool
		keys                int
	}{
		{"typed then submitted", "\n› retain the old gate\n  tab to queue message", "submitted", true, 1},
		{"styled animation replaces typed whitespace", "\n› retain\x1b[38;5;45m⠁\x1b[0mthe old gate\n  tab to queue message", "submitted", true, 1},
		{"already queued", "\n• Messages to be submitted after next tool call\n  ↳ retain the old gate\n› Ask Codex to do anything\n", "submitted", false, 0},
		{"Enter unsupported", "\n› retain the old gate\n  tab to queue message", "typed", false, 1},
		{"concurrent composer suffix", "\n› retain the old gate plus another draft\n  tab to queue message", "", false, 0},
		{"non-exact queued suffix", "\n• Messages to be submitted after next tool call\n  ↳ retain the old gate plus another draft\n› Ask Codex to do anything\n", "", false, 0},
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

func TestCodexStyledComposerRejectsOccupiedAndUnknownBraille(t *testing.T) {
	for _, screen := range []string{
		"\n\x1b[1m›\x1b[0m\x1b[38;5;45m⠁\x1b[0m\x1b[2mAsk Codex to do anything\x1b[0m\x1b[38;5;45m⠙\x1b[0mdraft\n",
		"\n\x1b[1m›\x1b[0m⠁\x1b[2mAsk Codex to do anything\x1b[0m\n",
		"\n\x1b[1m›\x1b[0m\x1b[38;5;45m⠁\x1b[0m\x1b[2mAsk Codex to do anything\x1b[0m\x1b[999m⠙\x1b[0m\n",
	} {
		base := newAgentFake(agentFake{status: "working"})
		runner := &codexFake{base: base, beforeScreen: screen}
		sender := newAgentSender(base)
		sender.Herdr.Commands = runner
		err := sender.Text(context.Background(), "task-7", "later wake")
		if err == nil || !strings.Contains(err.Error(), "not sent") || base.promptCalls != 0 || runner.keys != 0 {
			t.Fatalf("unsafe styled composer mutated: screen=%q err=%v prompts=%d keys=%d", screen, err, base.promptCalls, runner.keys)
		}
	}
}
