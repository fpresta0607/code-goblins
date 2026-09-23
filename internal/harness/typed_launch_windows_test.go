package harness

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Invoked only in the child native process reached through PowerShell 5.1.
func TestWindowsArgvFixture(t *testing.T) {
	path := os.Getenv("CFO_ARGV_FIXTURE_OUTPUT")
	if path == "" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			data, err := json.Marshal(os.Args[i+1:])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("missing fixture argument delimiter")
}

func TestPowerShell51CodexLaunchPreservesMaxWithoutQuotedPrompt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "O'Brien task")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "argv.json")
	t.Setenv("CFO_ARGV_FIXTURE_OUTPUT", output)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "codex.ps1")
	if err := os.WriteFile(shim, []byte("& "+powerShellLiteral(exe)+" '-test.run=^TestWindowsArgvFixture$' '--' @args\nexit $LASTEXITCODE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	want := []string{"--model", "gpt-6-astra", "-c", "model_reasoning_effort=max"}
	prompt := "Read the brief. Report --blocked \"question options: a | b\". O'Brien $(literal) `literal`."
	launch := Launch{TypedLaunch: true, Executable: shim, Args: want, Instruction: prompt, Dir: dir, Env: map[string]string{"GOTMPDIR": dir}}
	line, err := launch.PowerShellTypedLine()
	if err != nil {
		t.Fatal(err)
	}
	run := func(script string) []string {
		t.Helper()
		path := filepath.Join(dir, "launch.ps1")
		if err := os.WriteFile(path, []byte(script+"\nexit $LASTEXITCODE\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path).CombinedOutput(); err != nil {
			t.Fatalf("native launch: %v %s", err, out)
		}
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		var args []string
		if err := json.Unmarshal(data, &args); err != nil {
			t.Fatal(err)
		}
		return args
	}
	// Reproduce the former path, proving why PowerShell quoting alone is not a fix.
	broken := run(line + " " + powerShellLiteral(prompt))
	if reflect.DeepEqual(broken, append(append([]string{}, want...), prompt)) {
		t.Fatal("fixture did not reproduce PowerShell 5.1 native quoting loss")
	}
	if got := run(line); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
	if strings.Contains(line, prompt) {
		t.Fatal("prompt still entered native argv")
	}
}
