package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// CodexMCPServers names the MCP servers the operator's Codex configuration
// (config.toml in CODEX_HOME, or in .codex under the user's profile) defines:
// each [mcp_servers.<name>] table, each <name> = { ... } or <name>.<key>
// entry of a bare [mcp_servers] table, and each top-level mcp_servers.<name>
// key. A quoted name comes back without its quotes. No configuration defines
// none.
func CodexMCPServers() ([]string, error) {
	home, err := codexHome()
	if err != nil {
		return nil, err
	}
	file, err := fsx.Open(filepath.Join(home, "config.toml"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("harness: read the Codex configuration: %w", err)
	}
	defer file.Close()
	var names []string
	// Keys before the first table header are top-level keys.
	isTopLevel, isInServers := true, false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		var match []string
		switch {
		case strings.HasPrefix(line, "["):
			isTopLevel = false
			isInServers = mcpServersTable.MatchString(line)
			match = mcpServerTable.FindStringSubmatch(line)
		case isTopLevel:
			match = mcpServerTopLevelKey.FindStringSubmatch(line)
		case isInServers:
			match = mcpServerEntry.FindStringSubmatch(line)
		}
		if match != nil {
			names = append(names, unquotedKey(match[1]))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("harness: read the Codex configuration: %w", err)
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// CheckCodexMCPServers refuses a server name Codex's -c override cannot
// address in a dotted key, since a goblin would start that server.
func CheckCodexMCPServers(names []string) error {
	for _, name := range names {
		if !bareKey.MatchString(name) {
			return fmt.Errorf("harness: Codex's MCP server %q cannot be turned off with a -c override, so a goblin would start it", name)
		}
	}
	return nil
}

// codexHome is the folder of the operator's Codex configuration: CODEX_HOME,
// or .codex under the user's profile.
func codexHome() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return home, nil
	}
	profile, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("harness: locate the Codex configuration: %w", err)
	}
	return filepath.Join(profile, ".codex"), nil
}

// CodexHooks lists the command hooks a Codex session started in worktree, a
// worktree of project, loads, one "<event>: <command>" each: the operator's
// hooks.json in the Codex configuration folder, then the project's
// .codex/hooks.json in its main checkout, from which Codex takes a worktree's
// project hooks, then the worktree's own when it differs from the main
// checkout's. A file that is not there lists none; one that cannot be read or
// parsed lists none and is named in the error, while the others are still
// listed. The [hooks] tables of config.toml are not read.
func CodexHooks(project, worktree string) ([]string, error) {
	home, err := codexHome()
	if err != nil {
		return nil, err
	}
	hooks, _, homeErr := codexHooksIn(filepath.Join(home, "hooks.json"))
	projectHooks, projectFile, projectErr := codexHooksIn(filepath.Join(project, ".codex", "hooks.json"))
	hooks = append(hooks, projectHooks...)
	errs := []error{homeErr, projectErr}
	if worktreeHooks, worktreeFile, err := codexHooksIn(filepath.Join(worktree, ".codex", "hooks.json")); !bytes.Equal(worktreeFile, projectFile) {
		hooks = append(hooks, worktreeHooks...)
		errs = append(errs, err)
	}
	return hooks, errors.Join(errs...)
}

// codexHooksIn lists the command hooks of the Codex hooks file at path and
// returns what the file holds; a file that is not there holds nothing.
func codexHooksIn(path string) ([]string, []byte, error) {
	data, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("harness: read Codex hooks: %w", err)
	}
	var file struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, data, fmt.Errorf("harness: read Codex hooks in %s: %w", path, err)
	}
	var hooks []string
	for _, event := range slices.Sorted(maps.Keys(file.Hooks)) {
		for _, group := range file.Hooks[event] {
			for _, hook := range group.Hooks {
				if hook.Type == "command" {
					hooks = append(hooks, event+": "+hook.Command)
				}
			}
		}
	}
	return hooks, data, nil
}

// unquotedKey is a TOML key segment without the quotes of a quoted key.
func unquotedKey(key string) string {
	if strings.HasPrefix(key, `"`) || strings.HasPrefix(key, "'") {
		return key[1 : len(key)-1]
	}
	return key
}

// tomlKeySegment is one segment of a TOML dotted key: quoted or bare.
const tomlKeySegment = `("[^"]*"|'[^']*'|[A-Za-z0-9_-]+)`

var (
	mcpServersTable      = regexp.MustCompile(`^\[\s*mcp_servers\s*\]`)
	mcpServerTable       = regexp.MustCompile(`^\[\s*mcp_servers\s*\.\s*` + tomlKeySegment + `\s*[.\]]`)
	mcpServerEntry       = regexp.MustCompile(`^` + tomlKeySegment + `\s*[.=]`)
	mcpServerTopLevelKey = regexp.MustCompile(`^mcp_servers\s*\.\s*` + tomlKeySegment + `\s*[.=]`)
	// bareKey is a name Codex's -c override can address in a dotted key.
	bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

type codexAdapter struct{}

func (codexAdapter) Kind() Kind {
	return Codex
}

func (codexAdapter) Validate(ctx context.Context, runner execx.Runner) error {
	_, err := validateExecutable(ctx, runner, "codex", "--version")
	return err
}

func (codexAdapter) Build(spec LaunchSpec) (Launch, error) {
	launch, err := buildBase(spec)
	if err != nil {
		return Launch{}, err
	}
	// Codex installs as an npm .cmd shim, which only cmd /c can start.
	launch.Executable = "codex"
	// Codex opens an "Update available!" prompt before its composer whenever
	// a newer release is out, and a spawn's brief typed into that prompt
	// leaves Codex. A goblin never needs the prompt, so it is never checked.
	// Its animations are off: in a terminal that says it is an xterm, idle
	// Codex draws braille dots over every empty cell,
	// the spaces of its composer included, which hides a message it holds.
	launch.Args = []string{"--dangerously-bypass-approvals-and-sandbox", "--no-alt-screen", "-c", "check_for_update_on_startup=false", "-c", "tui.animations=false"}
	// A goblin starts none of the operator's MCP servers, as claude's
	// --strict-mcp-config starts none: each one runs its own processes per
	// session, qdrant's alone about 900 MB.
	if err := CheckCodexMCPServers(spec.CodexMCPServers); err != nil {
		return Launch{}, err
	}
	for _, name := range spec.CodexMCPServers {
		launch.Args = append(launch.Args, "-c", "mcp_servers."+name+".enabled=false")
	}
	if hasValue(spec.Model) {
		launch.Args = append(launch.Args, "--model", spec.Model)
	}
	if hasValue(spec.Effort) {
		switch spec.Effort {
		case "low", "medium", "high", "xhigh", "max":
			// Codex accepts a raw string when a config value is not TOML.
			// Avoid embedded quotes on PowerShell 5.1's native argument path.
			launch.Args = append(launch.Args, "-c", "model_reasoning_effort="+spec.Effort)
		default:
			return Launch{}, fmt.Errorf("harness: Codex does not support effort %q", spec.Effort)
		}
	}
	return launch, nil
}

// Control resumes Codex through its resume subcommand, which is why
// ResumeArgs lead the argument list: `codex resume --last` continues the most
// recent recorded session without the picker.
func (codexAdapter) Control() Control {
	return Control{
		StopKeys:    []string{"escape"},
		StopCommand: "/quit",
		ResumeArgs:  []string{"resume", "--last"},
	}
}
