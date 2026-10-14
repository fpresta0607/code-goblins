//go:build windows

package cirunner

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// recorder keeps every command HyperV runs, and how long each had, and
// answers each with result.
type recorder struct {
	ran    []execx.Request
	limits []time.Duration
	result execx.Result
}

func (r *recorder) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	r.ran = append(r.ran, request)
	limit := time.Duration(-1)
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		limit = time.Until(deadline)
	}
	r.limits = append(r.limits, limit)
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

// Every Hyper-V call runs Windows PowerShell by its full path in Windows'
// own folder, with no profile, only Windows' own modules, and a script that
// stops at its first error and names the one VM.
func TestHyperVRunsFixedScriptsForItsOneVM(t *testing.T) {
	// Arrange
	t.Setenv("PSModulePath", `C:\Program Files\PowerShell\7\Modules`)
	commands := &recorder{result: execx.Result{Stdout: []byte("Off")}}
	windowsFolder, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	eachHyperVCall(HyperV{Commands: commands})

	// Assert
	if len(commands.ran) != 5 {
		t.Fatalf("ran %d commands; want one for each of the 5 calls", len(commands.ran))
	}
	shell := filepath.Join(windowsFolder, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	modules := "PSModulePath=" + filepath.Join(windowsFolder, "System32", "WindowsPowerShell", "v1.0", "Modules")
	for i, request := range commands.ran {
		if !request.KillTree || commands.limits[i] <= 0 || commands.limits[i] > hyperVLimit {
			t.Errorf("ran %q as a tree %v with %v to run; want it ended with all it started past %v", script(request), request.KillTree, commands.limits[i], hyperVLimit)
		}
		text := script(request)
		if request.Name != shell || !slices.Equal(request.Args[:3], []string{"-NoProfile", "-NonInteractive", "-Command"}) {
			t.Errorf("ran %s %q; want %s -NoProfile -NonInteractive -Command", request.Name, request.Args, shell)
		}
		if !strings.HasPrefix(text, "$ErrorActionPreference = 'Stop'; ") || !strings.Contains(text, "'"+VMName+"'") {
			t.Errorf("ran %q; want a script that stops at its first error and names %s", text, VMName)
		}
		paths := slices.DeleteFunc(slices.Clone(request.Env), func(entry string) bool { return !strings.HasPrefix(strings.ToUpper(entry), "PSMODULEPATH=") })
		if !slices.Equal(paths, []string{modules}) {
			t.Errorf("ran %q with module paths %q; want %q alone", text, paths, modules)
		}
	}
}

// An elevated command runs in its user's session, whose variables that user
// can set, so Windows PowerShell takes nothing from them that chooses what
// runs: not Windows' folder, the search path, the module path, nor a
// profiler.
func TestHyperVTakesNothingFromAUsersEnvironment(t *testing.T) {
	// Arrange
	for name, value := range map[string]string{
		"SystemRoot":           `C:\planted`,
		"windir":               `C:\planted`,
		"Path":                 `C:\planted`,
		"PATHEXT":              ".PS1",
		"ComSpec":              `C:\planted\cmd.exe`,
		"PSModulePath":         `C:\planted`,
		"COR_ENABLE_PROFILING": "1",
		"COR_PROFILER_PATH":    `C:\planted\profiler.dll`,
		"ProgramFiles":         `C:\planted`,
	} {
		t.Setenv(name, value)
	}
	commands := &recorder{result: execx.Result{Stdout: []byte("Off")}}
	windowsFolder, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	_ = HyperV{Commands: commands}.TurnOff(context.Background())

	// Assert
	if len(commands.ran) != 1 {
		t.Fatalf("ran %d commands; want one", len(commands.ran))
	}
	request := commands.ran[0]
	if !strings.HasPrefix(request.Name, windowsFolder+`\`) {
		t.Errorf("ran %s; want Windows PowerShell from Windows' own folder, %s", request.Name, windowsFolder)
	}
	for _, entry := range request.Env {
		if strings.Contains(entry, "planted") || strings.HasPrefix(strings.ToUpper(entry), "COR_") {
			t.Errorf("Windows PowerShell got %q from the user's environment", entry)
		}
	}
	if want := "SystemRoot=" + windowsFolder; !slices.Contains(request.Env, want) {
		t.Errorf("Windows PowerShell got %q; want %s", request.Env, want)
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
