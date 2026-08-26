package harness

import (
	"strings"
	"testing"
)

func TestRenderEnvScriptQuotesEveryValueAndSortsTheNames(t *testing.T) {
	script, err := RenderEnvScript(map[string]string{
		"DATABASE_URL":      "postgres://user:p'ass@host/db",
		"STRIPE_SECRET_KEY": "sk_live_value",
	})
	if err != nil {
		t.Fatalf("RenderEnvScript: %v", err)
	}
	// A single quote inside a value must be doubled, or the assignment ends
	// early and the rest of the secret becomes PowerShell code.
	want := "$env:DATABASE_URL = 'postgres://user:p''ass@host/db'\n$env:STRIPE_SECRET_KEY = 'sk_live_value'\n"
	if !strings.HasSuffix(script, want) {
		t.Errorf("script =\n%s\nwant it to end with\n%s", script, want)
	}
}

func TestRenderEnvScriptRefusesAnUnexportableName(t *testing.T) {
	if _, err := RenderEnvScript(map[string]string{"BAD-NAME": "x"}); err == nil {
		t.Fatal("RenderEnvScript = nil, want a refusal for a name the shell cannot export")
	}
}

func TestPowerShellPrefixSourcesTheSecretsFileBeforeTheHarnessEnvironment(t *testing.T) {
	launch := Launch{
		Env:         map[string]string{"GOTMPDIR": `C:\tmp\gotmp`},
		SecretsFile: `C:\state\tasktmp\task-1\auth.ps1`,
		Dir:         `C:\worktree`,
	}
	prefix, err := launch.PowerShellPrefix()
	if err != nil {
		t.Fatalf("PowerShellPrefix: %v", err)
	}
	want := `Set-Location -LiteralPath 'C:\worktree'; . 'C:\state\tasktmp\task-1\auth.ps1'; $env:GOTMPDIR = 'C:\tmp\gotmp'`
	if prefix != want {
		t.Fatalf("prefix = %q, want %q", prefix, want)
	}
	// Ordering is the contract: the harness environment is applied last so a
	// project credential can never redirect GOTMPDIR.
	if strings.Index(prefix, "auth.ps1") > strings.Index(prefix, "GOTMPDIR") {
		t.Error("the secrets file is sourced after the harness environment")
	}
}

func TestPowerShellPrefixRefusesARelativeSecretsFile(t *testing.T) {
	launch := Launch{Env: map[string]string{"GOTMPDIR": `C:\tmp`}, SecretsFile: `auth.ps1`}
	if _, err := launch.PowerShellPrefix(); err == nil {
		t.Fatal("PowerShellPrefix = nil, want a refusal for a relative secrets path")
	}
}

func TestPowerShellPrefixIsUnchangedWithoutASecretsFile(t *testing.T) {
	launch := Launch{Env: map[string]string{"GOTMPDIR": `C:\tmp\gotmp`}, Dir: `C:\worktree`}
	prefix, err := launch.PowerShellPrefix()
	if err != nil {
		t.Fatalf("PowerShellPrefix: %v", err)
	}
	if strings.Contains(prefix, ". '") {
		t.Errorf("prefix = %q, want no dot-source when nothing was injected", prefix)
	}
}

// The pane script strips every harness billing key before the harness
// starts, whether or not the project has credentials to inject, so a key
// inherited from the user environment or a dot-sourced project file can
// never make the harness bill it instead of the subscription.
func TestRenderEnvScriptStripsHarnessBillingKeysEvenWithNothingToInject(t *testing.T) {
	script, err := RenderEnvScript(map[string]string{})
	if err != nil {
		t.Fatalf("RenderEnvScript: %v", err)
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY"} {
		want := "Remove-Item -Path Env:" + key + " -ErrorAction SilentlyContinue\n"
		if !strings.Contains(script, want) {
			t.Errorf("script does not strip %s:\n%s", key, script)
		}
	}
}

func TestRenderEnvScriptStripsBeforeItAssigns(t *testing.T) {
	script, err := RenderEnvScript(map[string]string{"DATABASE_URL": "postgres://x"})
	if err != nil {
		t.Fatalf("RenderEnvScript: %v", err)
	}
	strip := strings.Index(script, "Remove-Item -Path Env:ANTHROPIC_API_KEY")
	assign := strings.Index(script, "$env:DATABASE_URL = ")
	if strip < 0 || assign < 0 || strip > assign {
		t.Errorf("strip must precede assignment; strip=%d assign=%d\n%s", strip, assign, script)
	}
}
