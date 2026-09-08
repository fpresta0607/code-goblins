package harness

import (
	"strings"
	"testing"
)

func TestPowerShellTypedLineRequiresTypedLaunch(t *testing.T) {
	launch := Launch{Env: map[string]string{"GOTMPDIR": `C:\tmp\gotmp`}, Executable: "pi"}
	if _, err := launch.PowerShellTypedLine(); err == nil {
		t.Fatal("PowerShellTypedLine returned nil error without TypedLaunch")
	}
}

func TestPowerShellTypedLineRequiresExecutable(t *testing.T) {
	launch := Launch{Env: map[string]string{"GOTMPDIR": `C:\tmp\gotmp`}, TypedLaunch: true}
	if _, err := launch.PowerShellTypedLine(); err == nil {
		t.Fatal("PowerShellTypedLine returned nil error without an executable")
	}
}

func TestPowerShellTypedLineRendersQuotingSafeCommand(t *testing.T) {
	launch := Launch{
		TypedLaunch: true,
		Executable:  "pi",
		Args:        []string{"--tui-mode", "regular"},
		Env:         map[string]string{"GOTMPDIR": `C:\task tmp\O'Brien\gotmp`},
		Dir:         `C:\work\O'Brien\task`,
		PromptFile:  `C:\briefs\O'Brien\task.md`,
	}

	got, err := launch.PowerShellTypedLine()
	if err != nil {
		t.Fatalf("PowerShellTypedLine: %v", err)
	}
	want := `Set-Location -LiteralPath 'C:\work\O''Brien\task'; $env:GOTMPDIR = 'C:\task tmp\O''Brien\gotmp'; & 'pi' '--tui-mode' 'regular' 'Read the brief at C:\briefs\O''Brien\task.md and follow it exactly.'`
	if got != want {
		t.Errorf("PowerShellTypedLine() = %q\nwant %q", got, want)
	}
}

func TestBriefInstructionIsSingleLiteralArgument(t *testing.T) {
	if got, want := BriefInstruction(`C:\briefs\task.md`), `Read the brief at C:\briefs\task.md and follow it exactly.`; got != want {
		t.Errorf("BriefInstruction() = %q, want %q", got, want)
	}
}

// codex resume takes `[OPTIONS] [SESSION_ID] [PROMPT]`, so an instruction
// appended as the first positional is read as a session name and the resumed
// goblin starts with nothing to do. Verified against codex-cli 0.153.4 when
// the typed launch landed.
func TestTypedLineOmitsTheInstructionOnAResume(t *testing.T) {
	base := Launch{
		TypedLaunch: true,
		Executable:  "codex",
		Args:        []string{"resume", "--last", "--dangerously-bypass-approvals-and-sandbox"},
		Instruction: "Read the handoff at C:/tasks/t1/handoff.md and follow it exactly.",
		Dir:         `C:\work`,
		Env:         map[string]string{"GOTMPDIR": `C:\tmp\gotmp`},
	}

	fresh := base
	freshLine, err := fresh.PowerShellTypedLine()
	if err != nil {
		t.Fatalf("PowerShellTypedLine: %v", err)
	}
	if !strings.Contains(freshLine, base.Instruction) {
		t.Errorf("a launch that is not a resume must still carry its instruction positionally:\n%s", freshLine)
	}

	resumed := base
	resumed.Resumed = true
	resumedLine, err := resumed.PowerShellTypedLine()
	if err != nil {
		t.Fatalf("PowerShellTypedLine: %v", err)
	}
	if strings.Contains(resumedLine, base.Instruction) {
		t.Errorf("a resumed launch must not pass the instruction positionally; codex binds it to SESSION_ID:\n%s", resumedLine)
	}
	if !strings.HasSuffix(resumedLine, powerShellLiteral("--dangerously-bypass-approvals-and-sandbox")) {
		t.Errorf("resumed line should end at the last real argument:\n%s", resumedLine)
	}
	for _, arg := range base.Args {
		if !strings.Contains(resumedLine, powerShellLiteral(arg)) {
			t.Errorf("resumed line dropped %q:\n%s", arg, resumedLine)
		}
	}
}
