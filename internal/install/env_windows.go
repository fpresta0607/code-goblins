//go:build windows

package install

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// registryEnvStore is the user-scope environment on Windows: the values
// under HKCU\Environment.
//
// It never uses `setx`. setx truncates any value it writes at 1024
// characters, which on a machine with a long PATH silently destroys
// everything past the cut, and a destroyed PATH is not something an adopter
// can be asked to forgive.
//
// Reads use DoNotExpandEnvironmentNames, matching what
// [Environment]::GetEnvironmentVariable(name, 'User') returns, and writes
// keep the value's existing registry kind so a REG_EXPAND_SZ PATH full of
// `%USERPROFILE%` entries is not silently downgraded to a literal REG_SZ.
//
// Values cross the PowerShell boundary as base64 in both directions: Windows
// PowerShell 5.1 encodes redirected stdout in the console's OEM code page,
// so writing the value itself would mangle any non-ASCII character on the
// way back, and a rewritten PATH must never lose a character.
type registryEnvStore struct {
	Commands execx.Runner
	key      string
}

// NewEnvStore returns the machine's user-scope environment.
func NewEnvStore(commands execx.Runner) EnvStore {
	return registryEnvStore{Commands: commands, key: envKey}
}

const envKey = `HKCU:\Environment`

func (s registryEnvStore) Get(name string) (string, bool, error) {
	script := fmt.Sprintf(`$item = Get-Item -LiteralPath '%s'
$value = $item.GetValue('%s', $null, 'DoNotExpandEnvironmentNames')
if ($null -eq $value) { exit 3 }
[Console]::Out.Write([Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes([string]$value)))`, s.key, powerShellName(name))
	result, err := s.run(script)
	if err != nil {
		return "", false, err
	}
	if result.ExitCode == 3 {
		return "", false, nil
	}
	if result.ExitCode != 0 {
		return "", false, fmt.Errorf("install: read user %s: %s", name, strings.TrimSpace(string(result.Stderr)))
	}
	value, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(result.Stdout)))
	if err != nil {
		return "", false, fmt.Errorf("install: read user %s: %w", name, err)
	}
	return string(value), true, nil
}

func (s registryEnvStore) Set(name, value string) error {
	script := fmt.Sprintf(`$item = Get-Item -LiteralPath '%[1]s'
$name = '%[2]s'
$value = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%[3]s'))
if ($null -ne $item.GetValue($name, $null, 'DoNotExpandEnvironmentNames')) {
  $kind = $item.GetValueKind($name)
} elseif ($value -like '*%%*') {
  $kind = 'ExpandString'
} else {
  $kind = 'String'
}
Set-ItemProperty -LiteralPath '%[1]s' -Name $name -Value $value -Type $kind`,
		s.key, powerShellName(name), base64UTF8(value))
	result, err := s.run(script)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("install: set user %s: %s", name, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}

func (s registryEnvStore) Unset(name string) error {
	script := fmt.Sprintf(`Remove-ItemProperty -LiteralPath '%s' -Name '%s' -ErrorAction SilentlyContinue`,
		s.key, powerShellName(name))
	result, err := s.run(script)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("install: remove user %s: %s", name, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}

// Broadcast sends WM_SETTINGCHANGE, which is what makes a terminal opened
// after the install see the new values. Without it the registry is correct
// and every already-running shell, Explorer included, keeps handing out the
// old environment until the next sign-in.
func (s registryEnvStore) Broadcast() error {
	const script = `Add-Type -Namespace CfoInstall -Name Win32 -MemberDefinition @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
$result = [UIntPtr]::Zero
[void][CfoInstall.Win32]::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)`
	result, err := s.run(script)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("install: publish the environment change: %s", strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}

func (s registryEnvStore) run(script string) (execx.Result, error) {
	if s.Commands == nil {
		return execx.Result{}, errors.New("install: command runner is required")
	}
	return s.Commands.Run(context.Background(), execx.Request{
		Name: "powershell",
		Args: []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script},
	})
}

// powerShellName escapes a variable name for the single-quoted literal it is
// pasted into.
func powerShellName(name string) string {
	return strings.ReplaceAll(name, "'", "''")
}

// base64UTF8 carries a value into the script without quoting it. A PATH
// holds backslashes, spaces, and `$`, and the one encoding that cannot be
// broken by any of them is not to quote the value at all.
func base64UTF8(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}
