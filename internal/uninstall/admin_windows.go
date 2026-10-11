//go:build windows

package uninstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// windowsPowerShell is Windows PowerShell by its own path, never found on
// PATH, where a folder earlier on it could put another program under its
// name.
func windowsPowerShell() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

// RunAsAdministrator runs script as administrator, which Windows asks the
// person for once, and returns what the script found still there: each
// Where it added to $still. The script and what it found go in a folder of
// their own in the temp folder, which goes once it has run.
func RunAsAdministrator(ctx context.Context, script string) ([]string, error) {
	folder, err := os.MkdirTemp("", "code-goblins-uninstall-")
	if err != nil {
		return nil, err
	}
	defer fsx.RemoveAll(folder)
	file, found := filepath.Join(folder, "remove.ps1"), filepath.Join(folder, "still.txt")
	body := "# Code Goblins uninstall: what only an administrator can remove, each read back.\r\n" +
		"$ErrorActionPreference = 'Continue'\r\n" +
		"$still = @()\r\n" +
		script + "\r\n" +
		"Set-Content -LiteralPath " + quote(found) + " -Value $still -Encoding UTF8\r\n"
	if err := fsx.AtomicWriteFile(file, []byte(body)); err != nil {
		return nil, err
	}
	// The elevated run is started by a PowerShell of the person's own, which
	// waits for it and hands back its exit code. A prompt declined is an
	// error PowerShell raises there.
	start := "try { $run = Start-Process -FilePath " + quote(windowsPowerShell()) +
		" -ArgumentList '-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-File'," + quote(`"`+file+`"`) +
		" -Verb RunAs -WindowStyle Hidden -Wait -PassThru -ErrorAction Stop; exit $run.ExitCode } catch { exit 1223 }"
	result, err := (execx.OSRunner{}).Run(ctx, execx.Request{Name: windowsPowerShell(), Args: []string{"-NoProfile", "-NonInteractive", "-Command", start}})
	if err != nil {
		return nil, err
	}
	switch result.ExitCode {
	case 0:
	case 1223:
		return nil, errors.New("Windows' administrator prompt was declined")
	default:
		return nil, fmt.Errorf("the administrator's part ended with code %d", result.ExitCode)
	}
	data, err := fsx.ReadFile(found)
	if err != nil {
		return nil, fmt.Errorf("the administrator's part did not say what it removed: %w", err)
	}
	var still []string
	for _, line := range strings.Split(strings.ReplaceAll(strings.TrimPrefix(string(data), utf8Mark), "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			still = append(still, line)
		}
	}
	return still, nil
}

// RemoveOnceEnded removes folder once the process pid has ended and nothing
// holds it any more, which the uninstall's own program, moved there, does
// while it runs. It starts cmd.exe to do it, alone, which tries once a second
// for a minute and ends.
func RemoveOnceEnded(folder string) error {
	quoted := `"` + folder + `"`
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	loop := "for /l %i in (1,1,60) do (rd /s /q " + quoted + " 2>nul & if not exist " + quoted + " exit /b 0 & ping -n 2 127.0.0.1 >nul)"
	start := func(flags uint32) (*exec.Cmd, error) {
		cmd := execx.Command(shell)
		// cmd.exe reads its command line as it is, not as Go would quote its
		// arguments, so the line is given whole.
		cmd.SysProcAttr.CmdLine = `"` + shell + `" /d /c ` + loop
		cmd.Dir = filepath.Dir(folder)
		cmd.SysProcAttr.CreationFlags |= flags
		return cmd, cmd.Start()
	}
	// It outlives this process, so it starts in a group of its own and,
	// where the job it is in allows, outside that job.
	cmd, err := start(syscall.CREATE_NEW_PROCESS_GROUP | createBreakawayFromJob)
	if err != nil {
		if cmd, err = start(syscall.CREATE_NEW_PROCESS_GROUP); err != nil {
			return fmt.Errorf("start the removal of %s: %w", folder, err)
		}
	}
	return cmd.Process.Release()
}

// utf8Mark is the byte order mark Windows PowerShell's Set-Content writes
// first in a UTF-8 file.
const utf8Mark = "\xef\xbb\xbf"

// createBreakawayFromJob starts a process outside its parent's job.
const createBreakawayFromJob = 0x01000000
