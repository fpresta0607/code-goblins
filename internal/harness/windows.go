package harness

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// PowerShellPrefix renders the pane-shell preparation line typed before
// `herdr agent start`: agent start has no environment or working-directory
// support, so the leased worktree and the harness environment are established
// in the shell first. Every value is a single-quoted PowerShell literal;
// Herdr panes run Windows PowerShell 5.1, whose native-argument quoting
// corrupts any argument containing embedded double quotes.
func (launch Launch) PowerShellPrefix() (string, error) {
	if launch.Env == nil || launch.Env["GOTMPDIR"] == "" {
		return "", errors.New("harness: launch GOTMPDIR is required")
	}
	if launch.Dir != "" && !filepath.IsAbs(launch.Dir) {
		return "", errors.New("harness: launch Dir must be absolute")
	}

	keys := make([]string, 0, len(launch.Env))
	for key := range launch.Env {
		if !validEnvironmentName(key) {
			return "", fmt.Errorf("harness: invalid environment name %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys)+1)
	if launch.Dir != "" {
		parts = append(parts, "Set-Location -LiteralPath "+powerShellLiteral(launch.Dir))
	}
	for _, key := range keys {
		parts = append(parts, "$env:"+key+" = "+powerShellLiteral(launch.Env[key]))
	}
	return strings.Join(parts, "; "), nil
}

// PowerShellTypedLine renders the full typed launch for harnesses Herdr cannot
// start natively (npm .cmd shims): the shell prefix plus the harness command
// with the brief instruction as its final positional argument. The instruction
// is a single-quoted literal with no embedded quotes, safe for the Windows
// PowerShell 5.1 native-argument path.
func (launch Launch) PowerShellTypedLine() (string, error) {
	if !launch.TypedLaunch {
		return "", errors.New("harness: typed line requires a typed-launch harness")
	}
	if strings.TrimSpace(launch.Executable) == "" {
		return "", errors.New("harness: typed launch executable is required")
	}
	prefix, err := launch.PowerShellPrefix()
	if err != nil {
		return "", err
	}
	command := "& " + powerShellLiteral(launch.Executable)
	for _, arg := range launch.Args {
		command += " " + powerShellLiteral(arg)
	}
	command += " " + powerShellLiteral(BriefInstruction(launch.PromptFile))
	return prefix + "; " + command, nil
}

func powerShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func validEnvironmentName(name string) bool {
	for index, character := range name {
		if character == '_' || unicode.IsLetter(character) || (index > 0 && unicode.IsDigit(character)) {
			continue
		}
		return false
	}
	return name != ""
}
