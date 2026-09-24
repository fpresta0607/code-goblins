package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

const (
	createNewConsole = 0x00000010
	createNoWindow   = 0x08000000
)

// OSRunLauncher opens a run item in a visible console window of exactly the
// shell it names. An admin item goes through Windows UAC: a hidden Windows
// PowerShell helper asks Windows to start it elevated, so Windows itself asks
// the Overlord, and then waits for that window, so the item ends when it
// closes.
type OSRunLauncher struct{}

func (OSRunLauncher) Launch(_ context.Context, l RunLaunch) (RunStarted, error) {
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
	cmd := exec.Command(shell, args...)
	flags := uint32(createNewConsole)
	if l.Admin {
		helper, err := runShellPath("powershell", exec.LookPath, exists, systemRoot)
		if err != nil {
			return RunStarted{}, err
		}
		elevate := filepath.Join(l.Dir, "elevate.ps1")
		if err := fsx.AtomicWriteFile(elevate, elevateScript(shell, args, filepath.Join(l.Dir, "declined.txt"))); err != nil {
			return RunStarted{}, err
		}
		cmd = exec.Command(helper, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", elevate)
		flags = createNoWindow
	}
	cmd.Dir = l.Cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	if err := cmd.Start(); err != nil {
		return RunStarted{}, err
	}
	go func() { _ = cmd.Wait() }()
	// A process already gone reports no start time, so the item ends at once
	// unless its run wrote an exit code.
	started := RunStarted{PID: cmd.Process.Pid}
	if entries, err := proc.Ancestry(started.PID, 1); err == nil && len(entries) == 1 {
		started.Start = entries[0].Start
	}
	return started, nil
}

// runnerScript is what the window runs: the item's script in the same shell,
// its output shown and kept in output.log, and its exit code written to
// exit.txt, after which the window stays open until the Overlord closes it.
// It returns the script and the arguments that run it, ending with its path.
func runnerScript(l RunLaunch, shell string) ([]byte, []string) {
	output, exit := filepath.Join(l.Dir, "output.log"), filepath.Join(l.Dir, "exit.txt")
	if l.Shell == "bash" {
		quote := func(path string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", `'\''`) + "'" }
		runner := filepath.Join(l.Dir, "runner.sh")
		return []byte("cd " + quote(l.Cwd) + " || exit 1\n" +
			`"$BASH" ` + quote(l.Script) + " 2>&1 | tee " + quote(output) + "\n" +
			"code=${PIPESTATUS[0]}\n" +
			"printf '%s' \"$code\" > " + quote(exit) + "\n" +
			"printf '\\nFinished with exit code %s. Press Enter to close this window.\\n' \"$code\"\n" +
			"read -r _\n"), []string{filepath.ToSlash(runner)}
	}
	quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "''") + "'" }
	runner := filepath.Join(l.Dir, "runner.ps1")
	return []byte("\xef\xbb\xbf" +
		"$ErrorActionPreference = 'Stop'\r\n" +
		"$start = [System.Diagnostics.ProcessStartInfo]::new(" + quote(shell) + ", " + quote(`-NoProfile -ExecutionPolicy Bypass -File "`+l.Script+`"`) + ")\r\n" +
		"$start.WorkingDirectory = " + quote(l.Cwd) + "\r\n" +
		"$start.UseShellExecute = $false\r\n" +
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
		"[System.IO.File]::WriteAllText(" + quote(exit) + ", \"$code\", $utf8)\r\n" +
		"Write-Host ''\r\n" +
		"Write-Host \"Finished with exit code $code. Press Enter to close this window.\"\r\n" +
		"[void][Console]::ReadLine()\r\n"), []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", runner}
}

// elevateScript asks Windows to start the runner elevated. A declined prompt
// leaves its reason in declined.txt; otherwise it waits for the elevated
// window, so the helper lives exactly as long as the run.
func elevateScript(shell string, args []string, declined string) []byte {
	quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "''") + "'" }
	line := make([]string, len(args))
	for i, arg := range args {
		line[i] = arg
		if strings.ContainsAny(arg, ` \/`) {
			line[i] = `"` + arg + `"`
		}
	}
	return []byte("\xef\xbb\xbf" +
		"try {\r\n" +
		"  $run = Start-Process -FilePath " + quote(shell) + " -ArgumentList " + quote(strings.Join(line, " ")) + " -Verb RunAs -PassThru -ErrorAction Stop\r\n" +
		"} catch {\r\n" +
		"  [System.IO.File]::WriteAllText(" + quote(declined) + ", $_.Exception.Message, [System.Text.UTF8Encoding]::new($false))\r\n" +
		"  exit 1\r\n" +
		"}\r\n" +
		"$run.WaitForExit()\r\n")
}
