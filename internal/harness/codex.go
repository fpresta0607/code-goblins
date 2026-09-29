package harness

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// CodexMCPServers names the MCP servers the operator's Codex configuration
// (config.toml in CODEX_HOME, or in .codex under the user's profile) defines:
// each [mcp_servers.<name>] table, each <name> = { ... } or <name>.<key>
// entry of a bare [mcp_servers] table, and each top-level mcp_servers.<name>
// key. A quoted name comes back without its quotes. No configuration defines
// none.
func CodexMCPServers() ([]string, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		profile, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("harness: locate the Codex configuration: %w", err)
		}
		home = filepath.Join(profile, ".codex")
	}
	file, err := os.Open(filepath.Join(home, "config.toml"))
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
	// Herdr's Windows agent start uses Start-Process -FilePath, which cannot
	// execute the npm .cmd shim codex installs as; codex launches typed instead,
	// the same way pi does.
	launch.TypedLaunch = true
	launch.Executable = "codex"
	// Codex asks to trust a directory it has not seen; the trusting option is
	// highlighted by default, so a bare Enter confirms it.
	launch.ConfirmMarkers = []string{"Do you trust the contents of this directory?"}
	launch.ConfirmKeys = []string{"enter"}
	// Codex opens an "Update available!" prompt before its composer whenever
	// a newer release is out, and a spawn's brief typed into that prompt
	// leaves Codex. A goblin never needs the prompt, so it is never checked.
	// Its animations are off: in a terminal that says it is an xterm, a Herdr
	// pane's among them, idle Codex draws braille dots over every empty cell,
	// the spaces of its composer included, which hides a message it holds.
	launch.Args = []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "check_for_update_on_startup=false", "-c", "tui.animations=false"}
	// A goblin starts none of the operator's MCP servers, as claude's
	// --strict-mcp-config starts none: each one runs its own processes per
	// session, qdrant's alone about 900 MB.
	for _, name := range spec.CodexMCPServers {
		if !bareKey.MatchString(name) {
			return Launch{}, fmt.Errorf("harness: Codex's MCP server %q cannot be turned off with a -c override, so a goblin would start it", name)
		}
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
