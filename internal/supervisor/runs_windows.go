package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

const createNoWindow = 0x08000000

// The size a run's terminal starts at, until its card sizes it.
const (
	runCols = 120
	runRows = 30
)

// runnerLinger is how long, in seconds, a run's terminal waits once its
// command ended for the supervisor to keep what it shows and close it; a
// terminal no supervisor closes ends by itself after it.
const runnerLinger = 60

// OSRunLauncher starts a run item in a native terminal hosted the way a
// goblin's is, HostCommand being what runs a host (cfo.exe host), so the
// item's card draws it and no window opens. An admin item's host starts
// through Windows UAC: a hidden Windows PowerShell helper asks Windows to
// start it elevated and out of sight, so Windows itself asks the Overlord,
// and then waits for it, so the helper lives as long as the run. An Update
// item runs with no terminal at all.
type OSRunLauncher struct {
	HostCommand []string
}

func (o OSRunLauncher) Launch(_ context.Context, l RunLaunch) (RunStarted, error) {
	exists := func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}
	systemRoot := os.Getenv("SystemRoot")
	shell, err := runShellPath(l.Shell, exec.LookPath, exists, systemRoot)
	if err != nil {
		return RunStarted{}, err
	}
	runner, args := runnerScript(l, shell)
	if err := fsx.AtomicWriteFile(args[len(args)-1], runner); err != nil {
		return RunStarted{}, err
	}
	spec := host.Spec{ID: runTerminal, Args: append([]string{shell}, args...), Dir: l.Cwd, Cols: runCols, Rows: runRows}
	var started RunStarted
	switch {
	case l.Hidden:
		if started, err = runHidden(shell, args, l.Cwd); err != nil {
			return RunStarted{}, err
		}
	case len(o.HostCommand) == 0:
		return RunStarted{}, errors.New("the command that hosts a run's terminal is not set")
	case l.Admin:
		helper, err := runShellPath("powershell", exec.LookPath, exists, systemRoot)
		if err != nil {
			return RunStarted{}, err
		}
		elevate := filepath.Join(l.Dir, "elevate.ps1")
		hostArgs := append(append([]string(nil), o.HostCommand[1:]...), host.Arguments(l.Dir, spec)...)
		if err := fsx.AtomicWriteFile(elevate, elevateScript(o.HostCommand[0], hostArgs, filepath.Join(l.Dir, "declined.txt"))); err != nil {
			return RunStarted{}, err
		}
		cmd := execx.Command(helper, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", elevate)
		cmd.Dir = l.Cwd
		cmd.SysProcAttr.CreationFlags |= createNoWindow
		if err := cmd.Start(); err != nil {
			return RunStarted{}, err
		}
		go func() { _ = cmd.Wait() }()
		started.PID = cmd.Process.Pid
	default:
		terminal, err := host.Launch(l.Dir, o.HostCommand, os.Environ(), spec)
		if err != nil {
			return RunStarted{}, err
		}
		started.PID, started.Terminal = terminal.HostPID, true
	}
	// A process already gone reports no start time, so the item ends at once
	// unless its run wrote an exit code.
	if entries, err := proc.Ancestry(started.PID, 1); err == nil && len(entries) == 1 {
		started.Start = entries[0].Start
	}
	return started, nil
}

// runHidden starts a hidden item's runner with a console no one sees, which
// outlives the supervisor that started it.
func runHidden(shell string, args []string, cwd string) (RunStarted, error) {
	application, err := windows.UTF16PtrFromString(shell)
	if err != nil {
		return RunStarted{}, err
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{shell}, args...)))
	if err != nil {
		return RunStarted{}, err
	}
	dir, err := windows.UTF16PtrFromString(cwd)
	if err != nil {
		return RunStarted{}, err
	}
	startup := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	var info windows.ProcessInformation
	if err := windows.CreateProcess(application, commandLine, nil, nil, false, createNoWindow, nil, dir, &startup, &info); err != nil {
		return RunStarted{}, err
	}
	windows.CloseHandle(info.Thread)
	windows.CloseHandle(info.Process)
	return RunStarted{PID: int(info.ProcessId)}, nil
}

// runnerScript is what an item's terminal runs: the item's script in its own
// shell, attached to the terminal itself, so a prompt, a sign-in or cfo
// attach works there; then its exit code written to exit.txt, after which it
// waits for the supervisor to keep what the terminal shows and close it, for
// runnerLinger at most, so nothing waits for a key. A hidden item, which has
// no terminal, keeps its output in output.log as it goes instead and ends
// with its command.
// It returns the script and the arguments that run it, ending with its path.
func runnerScript(l RunLaunch, shell string) ([]byte, []string) {
	output, exit := filepath.Join(l.Dir, "output.log"), filepath.Join(l.Dir, "exit.txt")
	linger := strconv.Itoa(runnerLinger)
	if l.Shell == "bash" {
		quote := func(path string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", `'\''`) + "'" }
		runner := filepath.Join(l.Dir, "runner.sh")
		return []byte(`"$BASH" ` + quote(l.Script) + "\n" +
			"code=$?\n" +
			"printf '%s' \"$code\" > " + quote(exit) + "\n" +
			"sleep " + linger + "\n"), []string{filepath.ToSlash(runner)}
	}
	quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "''") + "'" }
	runner := filepath.Join(l.Dir, "runner.ps1")
	if !l.Hidden {
		return []byte("\xef\xbb\xbf" +
			"& " + quote(shell) + " -NoProfile -ExecutionPolicy Bypass -File " + quote(l.Script) + "\r\n" +
			"$code = if ($null -ne $LASTEXITCODE) { $LASTEXITCODE } else { 1 }\r\n" +
			"[System.IO.File]::WriteAllText(" + quote(exit) + ", \"$code\", [System.Text.UTF8Encoding]::new($false))\r\n" +
			"Start-Sleep -Seconds " + linger + "\r\n"), []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", runner}
	}
	return []byte("\xef\xbb\xbf" +
		"$ErrorActionPreference = 'Stop'\r\n" +
		"$start = [System.Diagnostics.ProcessStartInfo]::new(" + quote(shell) + ", " + quote(`-NoProfile -ExecutionPolicy Bypass -File "`+l.Script+`"`) + ")\r\n" +
		"$start.WorkingDirectory = " + quote(l.Cwd) + "\r\n" +
		"$start.UseShellExecute = $false\r\n" +
		"$start.CreateNoWindow = $true\r\n" +
		"$start.RedirectStandardOutput = $true\r\n" +
		"$start.RedirectStandardError = $true\r\n" +
		"$utf8 = [System.Text.UTF8Encoding]::new($false)\r\n" +
		"$log = [System.IO.StreamWriter]::new(" + quote(output) + ", $false, $utf8)\r\n" +
		"$log.AutoFlush = $true\r\n" +
		"$process = [System.Diagnostics.Process]::Start($start)\r\n" +
		"$readers = @($process.StandardOutput, $process.StandardError)\r\n" +
		"$buffers = @([char[]]::new(4096), [char[]]::new(4096))\r\n" +
		"$reads = @($readers[0].ReadAsync($buffers[0], 0, 4096), $readers[1].ReadAsync($buffers[1], 0, 4096))\r\n" +
		"while ($reads[0] -or $reads[1]) {\r\n" +
		"  foreach ($i in 0, 1) {\r\n" +
		"    if (-not $reads[$i] -or -not $reads[$i].Wait(20)) { continue }\r\n" +
		"    $count = $reads[$i].Result\r\n" +
		"    if ($count -eq 0) { $reads[$i] = $null; continue }\r\n" +
		"    $text = [string]::new($buffers[$i], 0, $count)\r\n" +
		"    [Console]::Write($text)\r\n" +
		"    $log.Write($text)\r\n" +
		"    $reads[$i] = $readers[$i].ReadAsync($buffers[$i], 0, 4096)\r\n" +
		"  }\r\n" +
		"}\r\n" +
		"$process.WaitForExit()\r\n" +
		"$code = $process.ExitCode\r\n" +
		"$log.Dispose()\r\n" +
		"[System.IO.File]::WriteAllText(" + quote(exit) + ", \"$code\", $utf8)\r\n"), []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", runner}
}

// elevateScript asks Windows to start program with args elevated and out of
// sight. A declined prompt leaves its reason in declined; otherwise it waits
// for the elevated process, so the helper lives exactly as long as the run.
func elevateScript(program string, args []string, declined string) []byte {
	quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "''") + "'" }
	return []byte("\xef\xbb\xbf" +
		"try {\r\n" +
		"  $run = Start-Process -FilePath " + quote(program) + " -ArgumentList " + quote(elevatedCommandLine(args)) + " -Verb RunAs -WindowStyle Hidden -PassThru -ErrorAction Stop\r\n" +
		"} catch {\r\n" +
		"  [System.IO.File]::WriteAllText(" + quote(declined) + ", $_.Exception.Message, [System.Text.UTF8Encoding]::new($false))\r\n" +
		"  exit 1\r\n" +
		"}\r\n" +
		"$run.WaitForExit()\r\n")
}

// elevatedCommandLine is args as the one command line Start-Process hands
// Windows as it is, each quoted so the program reads it back exactly.
func elevatedCommandLine(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = windows.EscapeArg(arg)
	}
	return strings.Join(quoted, " ")
}
