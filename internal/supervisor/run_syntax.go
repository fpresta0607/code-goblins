package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// runSyntaxWait bounds a shell's look at a command it is asked to parse.
const runSyntaxWait = 30 * time.Second

// powershellParse parses the script the environment names with PowerShell's
// own parser, runs none of it, and prints each error with its line.
const powershellParse = `$errors = $null; [void][System.Management.Automation.Language.Parser]::ParseFile($env:CFO_RUN_SYNTAX, [ref]$null, [ref]$errors); foreach ($e in $errors) { 'line ' + $e.Extent.StartLineNumber + ': ' + $e.Message }; if ($errors.Count) { exit 1 }`

// CheckRunCommand refuses a command its shell cannot parse, with the lines
// the shell names, before it is published for the Overlord to run: such a
// command runs nothing at his click and fails, as repair-home-v0.5.3 did on
// 2026-10-07. The shell parses the command and runs none of it.
func CheckRunCommand(shell, command string) error {
	exists := func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}
	program, err := runShellPath(shell, exec.LookPath, exists, os.Getenv("SystemRoot"))
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "cfo-run-syntax-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	name, script := runScript(Run{Shell: shell, Command: command})
	path := filepath.Join(dir, name)
	if err := fsx.AtomicWriteFile(path, script); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), runSyntaxWait)
	defer cancel()
	var check *exec.Cmd
	if shell == "bash" {
		check = execx.CommandContext(ctx, program, "-n", path)
	} else {
		check = execx.CommandContext(ctx, program, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", powershellParse)
		check.Env = append(os.Environ(), "CFO_RUN_SYNTAX="+path)
	}
	output, err := check.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exit) && ctx.Err() == nil:
		said := strings.Join(strings.Fields(strings.ReplaceAll(string(output), path, name)), " ")
		return fmt.Errorf("the command does not parse in %s, so it would run nothing: %s", shell, bounded(said, 1000))
	}
	return fmt.Errorf("%s could not check the command: %w", shell, err)
}
