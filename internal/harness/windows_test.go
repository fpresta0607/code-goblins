package harness

import "testing"

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
