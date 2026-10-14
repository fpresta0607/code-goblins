package cirunner

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// recorder keeps every command HyperV runs and answers each with result.
type recorder struct {
	ran    []execx.Request
	result execx.Result
}

func (r *recorder) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.ran = append(r.ran, request)
	return r.result, nil
}

// script is the script a request ran.
func script(request execx.Request) string {
	return request.Args[len(request.Args)-1]
}

// eachHyperVCall makes every call HyperV has.
func eachHyperVCall(h HyperV) {
	ctx := context.Background()
	_ = h.TurnOff(ctx)
	_ = h.Restore(ctx)
	_ = h.AttachDVD(ctx, `C:\ProgramData\Code Goblins\ci-runner\work\job.iso`)
	_ = h.Start(ctx)
	_, _ = h.IsRunning(ctx)
}

// Every Hyper-V call runs Windows PowerShell by its full path, with no
// profile, no module path from PowerShell 7, and a script that stops at its
// first error and names the one VM.
func TestHyperVRunsFixedScriptsForItsOneVM(t *testing.T) {
	// Arrange
	t.Setenv("SystemRoot", `C:\Windows`)
	t.Setenv("PSModulePath", `C:\Program Files\PowerShell\7\Modules`)
	commands := &recorder{result: execx.Result{Stdout: []byte("Off")}}

	// Act
	eachHyperVCall(HyperV{Commands: commands})

	// Assert
	if len(commands.ran) != 5 {
		t.Fatalf("ran %d commands; want one for each of the 5 calls", len(commands.ran))
	}
	shell := filepath.Join(`C:\Windows`, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	for _, request := range commands.ran {
		text := script(request)
		if request.Name != shell || !slices.Equal(request.Args[:3], []string{"-NoProfile", "-NonInteractive", "-Command"}) {
			t.Errorf("ran %s %q; want %s -NoProfile -NonInteractive -Command", request.Name, request.Args, shell)
		}
		if !strings.HasPrefix(text, "$ErrorActionPreference = 'Stop'; ") || !strings.Contains(text, "'"+VMName+"'") {
			t.Errorf("ran %q; want a script that stops at its first error and names %s", text, VMName)
		}
		if slices.ContainsFunc(request.Env, func(entry string) bool { return strings.HasPrefix(strings.ToUpper(entry), "PSMODULEPATH=") }) {
			t.Errorf("ran %q with PowerShell 7's module path; want none", text)
		}
	}
}

// The DVD's path is passed in the environment, never into the script's text.
func TestAttachDVDPassesThePathOutsideTheScript(t *testing.T) {
	// Arrange
	commands := &recorder{}
	path := `C:\work\x'; Remove-VM -Force; '.iso`

	// Act
	err := HyperV{Commands: commands}.AttachDVD(context.Background(), path)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	request := commands.ran[0]
	if strings.Contains(script(request), path) || !slices.Contains(request.Env, dvdVariable+"="+path) {
		t.Errorf("script %q, environment %q; want the path only in %s", script(request), request.Env, dvdVariable)
	}
}

// The VM runs in every state but off.
func TestIsRunningIsFalseOnlyWhenTheVMIsOff(t *testing.T) {
	for state, want := range map[string]bool{"Off": false, "Off\r\n": false, "Running": true, "Saved": true, "Paused": true, "Starting": true} {
		t.Run(strings.TrimSpace(state), func(t *testing.T) {
			// Arrange
			commands := &recorder{result: execx.Result{Stdout: []byte(state)}}

			// Act
			isRunning, err := HyperV{Commands: commands}.IsRunning(context.Background())

			// Assert
			if err != nil || isRunning != want {
				t.Errorf("IsRunning with the VM %q = %v, %v; want %v", state, isRunning, err, want)
			}
		})
	}
}

// A script that fails is an error naming what it was for and what it wrote,
// never a VM taken for off.
func TestHyperVSaysAScriptThatFailed(t *testing.T) {
	// Arrange
	commands := &recorder{result: execx.Result{ExitCode: 1, Stderr: []byte("Get-VM : Hyper-V was unable to find a virtual machine with name \"cg-ci-precisiondocs\".\r\n")}}
	h := HyperV{Commands: commands}

	// Act
	isRunning, readErr := h.IsRunning(context.Background())
	turnOffErr := h.TurnOff(context.Background())

	// Assert
	if readErr == nil || isRunning || !strings.Contains(readErr.Error(), "read the VM's state: exit 1: Get-VM : Hyper-V was unable") {
		t.Errorf("IsRunning = %v, %v; want the failure named", isRunning, readErr)
	}
	if turnOffErr == nil || !strings.Contains(turnOffErr.Error(), "turn the VM off: exit 1") {
		t.Errorf("TurnOff = %v; want the failure named", turnOffErr)
	}
}
