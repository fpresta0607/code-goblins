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

func TestCodexParserAcceptsRawWindowsUTF8StyledFrame(t *testing.T) {
	// This is the shape emitted by Herdr through cmd.exe redirection: UTF-8
	// bytes, CRLF rows, RGB foreground/background SGR, and colored braille
	// animation immediately after the prompt. The BOM is accepted only as a
	// capture boundary marker and is never treated as composer content.
	screen := "\ufeff \r\n" +
		"\x1b[0m\x1b[1m\x1b[48;2;41;41;41m›\x1b[0m" +
		"\x1b[38;2;129;129;129m\x1b[48;2;41;41;41m\u2801\x1b[0m" +
		"\x1b[2m\x1b[48;2;41;41;41mAsk Codex to do anything\x1b[0m" +
		"\x1b[48;2;41;41;41m           \x1b[0m\r\n" +
		"\x1b[38;2;61;61;61m\x1b[48;2;41;41;41m\u2801\x1b[0m\r\n" +
		"  \x1b[38;2;246;226;183mgpt-6-astra high\x1b[0m\x1b[2m · \x1b[0m" +
		"\x1b[38;2;171;223;167mC:\\fixture\\project\x1b[0m\x1b[2m · \x1b[0mSynthetic task\r\n"
	if !codexComposerEmpty(screen) {
		t.Fatal("raw UTF-8 styled empty composer was not recognized")
	}
	if _, ok := parseANSICells(screen); !ok {
		t.Fatal("raw UTF-8 styled frame did not decode")
	}
}

func TestCodexParserAcceptsModelAgnosticLunaFooter(t *testing.T) {
	screen := "\r\n› \x1b[2mAsk Codex to do anything\x1b[0m\r\n" +
		"  Luna Reserve xhigh · C:\\fixture\\project · Synthetic task\r\n"
	if !codexComposerEmpty(screen) {
		t.Fatal("Luna Reserve footer should delimit an empty composer")
	}
	if !codexFooter("Luna Reserve xhigh · C:\\fixture\\project · Synthetic task") {
		t.Fatal("Luna Reserve footer was rejected")
	}
	if codexFooter("arbitrary text · not-a-workspace · suffix") {
		t.Fatal("arbitrary footer was accepted")
	}
}

func TestCodexParserAcceptsStyledAnimationInsideExactTypedMessage(t *testing.T) {
	screen := "\r\n\x1b[1m\x1b[48;2;41;41;41m›\x1b[0m" +
		"CFO-SMOKE-AAD\x1b[38;2;129;129;129m\x1b[48;2;41;41;41m\u2801\x1b[0m" +
		"\x1b[38;2;121;121;121m\x1b[48;2;41;41;41m\u2802\x1b[0m" +
		" please inspect\x1b[38;2;61;61;61m\x1b[48;2;41;41;41m\u2804\x1b[0m\r\n" +
		"  \x1b[2mtab to queue message\x1b[0m\r\n"
	if got := codexDelivery(screen, "CFO-SMOKE-AAD please inspect"); got != "typed" {
		t.Fatalf("styled typed frame result = %q, want typed", got)
	}
}

func TestCodexParserAcceptsTypedFrameWhenQueueHintIsOutOfViewport(t *testing.T) {
	// A short Herdr capture can end before the queue hint. The message is
	// still safe to identify because the composer is bounded by the Codex
	// footer and all other cells must be styled animation or whitespace.
	screen := "\r\n\x1b[1m\x1b[48;2;41;41;41m›\x1b[0m" +
		"\x1b[38;2;129;129;129m\x1b[48;2;41;41;41m\u2801\x1b[0m" +
		"CFO-SMOKE-AAD please inspect\r\n" +
		"  \x1b[38;2;61;61;61m\x1b[48;2;41;41;41m\u2804\u2802\x1b[0m\r\n" +
		"  \x1b[38;2;246;226;183mgpt-6-astra high\x1b[0m\x1b[2m · \x1b[0m" +
		"\x1b[38;2;171;223;167mC:\\fixture\\project\x1b[0m\x1b[2m · \x1b[0mSynthetic task\r\n"
	if got := codexDelivery(screen, "CFO-SMOKE-AAD please inspect"); got != "typed" {
		t.Fatalf("typed frame without visible queue hint result = %q, want typed", got)
	}
}

func TestCodexParserRejectsUnsafeBytesSequencesAndSuffixes(t *testing.T) {
	cases := []struct {
		name   string
		screen string
	}{
		{"invalid UTF8", "\n› Ask Codex to do anything\xff\n"},
		{"unknown CSI", "\n\x1b[?25l› Ask Codex to do anything\n"},
		{"occupied braille", "\n› \u2801 draft\n  tab to queue message\n"},
		{"arbitrary suffix", "\n› CFO-SMOKE-AAD plus another draft\n  tab to queue message\n"},
		{"oversized frame", strings.Repeat("x", maxANSIFrameBytes+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := codexDelivery(tc.screen, "CFO-SMOKE-AAD"); got != "unknown" {
				t.Fatalf("result = %q, want unknown", got)
			}
		})
	}
}
