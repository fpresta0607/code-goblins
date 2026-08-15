package harness

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// PowerShellLine renders a structured launch only at the Herdr delivery
// boundary. Every value is a single-quoted PowerShell literal, and the brief
// remains a final Get-Content expression so the full file is one prompt.
func (launch Launch) PowerShellLine() (string, error) {
	if strings.TrimSpace(launch.Executable) == "" {
		return "", errors.New("harness: launch executable is required")
	}
	if launch.PromptFile != "" && !filepath.IsAbs(launch.PromptFile) {
		return "", errors.New("harness: launch PromptFile must be absolute")
	}
	if launch.PromptFile == "" && launch.FollowUpPrompt == "" {
		return "", errors.New("harness: launch requires a PromptFile or FollowUpPrompt")
	}
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

	parts := make([]string, 0, len(keys)+len(launch.Args)+4)
	if launch.Dir != "" {
		parts = append(parts, "Set-Location -LiteralPath "+powerShellLiteral(launch.Dir))
	}
	for _, key := range keys {
		parts = append(parts, "$env:"+key+" = "+powerShellLiteral(launch.Env[key]))
	}
	command := "& " + powerShellLiteral(launch.Executable)
	for _, arg := range launch.Args {
		command += " " + powerShellLiteral(arg)
	}
	if launch.PromptFile != "" {
		command += " (Get-Content -Raw -LiteralPath " + powerShellLiteral(launch.PromptFile) + ")"
	}
	parts = append(parts, command)
	return strings.Join(parts, "; "), nil
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
