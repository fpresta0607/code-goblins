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
// own parser, runs none of it, and prints each error with its line, then each
// parameter its param block makes mandatory.
const powershellParse = `$errors = $null; $script = [System.Management.Automation.Language.Parser]::ParseFile($env:CFO_RUN_SYNTAX, [ref]$null, [ref]$errors); foreach ($e in $errors) { 'line ' + $e.Extent.StartLineNumber + ': ' + $e.Message }; if ($errors.Count) { exit 1 }; $mandatory = @(foreach ($p in @($script.ParamBlock.Parameters)) { foreach ($a in $p.Attributes) { foreach ($n in @($a.NamedArguments)) { if ($n.ArgumentName -eq 'Mandatory' -and ($n.ExpressionOmitted -or $n.Argument.Extent.Text -ne '$false')) { $p.Name.VariablePath.UserPath } } } }); if ($mandatory.Count) { 'mandatory ' + ($mandatory -join ', '); exit 3 }`

// CheckRunCommand refuses a command its shell cannot parse, with the lines
// the shell names, before it is published for the Overlord to run: such a
// command runs nothing at his click and fails, as repair-home-v0.5.3 did on
// 2026-10-07. It refuses as well a PowerShell script whose param block makes
// a parameter mandatory: Run starts the script with no arguments, so it stops
// at PowerShell's prompt for the value, as the install card for main-6b7e7bc9
// did on 2026-10-18. The shell parses the command and runs none of it.
func CheckRunCommand(shell, command string) error {
	return checkRunCommand(shell, command, runSyntaxWait)
}

func checkRunCommand(shell, command string, wait time.Duration) error {
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
	defer fsx.RemoveAll(dir)
	name, script := runScript(Run{Shell: shell, Command: command})
	path := filepath.Join(dir, name)
	if err := fsx.AtomicWriteFile(path, script); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
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
	case ctx.Err() != nil:
		// The context's end of the shell reads as exit status 1, the exit
		// of a command that does not parse, so a shell cut off is named as
		// one that did not answer.
		return fmt.Errorf("%s did not answer within %s, so the command was not checked: try again", shell, wait)
	case errors.As(err, &exit) && exit.ExitCode() == 3 && shell != "bash":
		named := strings.TrimPrefix(strings.TrimSpace(string(output)), "mandatory ")
		return fmt.Errorf("the command's param block makes %s mandatory, and Run starts it with no arguments, so it would stop at a prompt for the value: put each value in the command itself", bounded(named, 300))
	case errors.As(err, &exit):
		said := strings.Join(strings.Fields(strings.ReplaceAll(string(output), path, name)), " ")
		return fmt.Errorf("the command does not parse in %s, so it would run nothing: %s", shell, bounded(said, 1000))
	}
	return fmt.Errorf("%s could not check the command: %w", shell, err)
}
