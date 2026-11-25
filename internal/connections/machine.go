package connections

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/crawler"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

// statusTimeLimit bounds one sign-in status command.
const statusTimeLimit = 20 * time.Second

// settingsLimit is the largest settings file of a harness read for its names.
const settingsLimit = 64 << 20

// statusCommands are the programs whose own command says whether the user is
// signed in, which is read by its exit code alone.
var statusCommands = map[string][]string{
	"gh":      {"auth", "status"},
	"vercel":  {"whoami"},
	"flyctl":  {"auth", "whoami"},
	"railway": {"whoami"},
}

// harnessStatusCommands are the harnesses that answer the same way.
var harnessStatusCommands = map[string][]string{
	"claude": {"auth", "status"},
	"codex":  {"login", "status"},
}

// credentialLike is a variable name that holds a credential.
var credentialLike = regexp.MustCompile(`(?i)(TOKEN|API_KEY|PASSWORD|SECRET|CONNECTION_STRING|DATABASE_URL)$|^API_KEY_`)

// codexServerHeader is the table a Codex user config declares an MCP server in.
var codexServerHeader = regexp.MustCompile(`^\[mcp_servers\.([A-Za-z0-9_-]+)\]$`)

const strictConfigDetail = "Your own Claude sessions can load it. Goblins start with a strict MCP config."

// CheckMachine checks this PC and its accounts, once: the command line tools
// the projects' manifests name, the harnesses, the connectors the user's own
// accounts hold, and the credential-like variables of the user environment.
func (e Environment) CheckMachine(ctx context.Context) Level {
	var level Level
	var problems []string
	manifests := e.loadManifests()
	level.Items = append(level.Items, e.cliItems(ctx, manifests)...)
	level.Items = append(level.Items, e.harnessItems(ctx)...)
	claude, err := e.claudeSettings()
	if err != nil {
		problems = append(problems, "Claude's own settings could not be read.")
	}
	level.Items = append(level.Items, accountItems(claude)...)
	codex, err := e.codexServers()
	if err != nil {
		problems = append(problems, "Codex's own settings could not be read.")
	}
	level.Items = append(level.Items, codexItems(codex)...)
	names, err := e.UserEnvNames()
	if err != nil {
		problems = append(problems, "This PC's user environment could not be read.")
	}
	level.Items = append(level.Items, environmentItems(names, manifests)...)
	level.Items = append(level.Items, e.builtInItems()...)
	level.Error = strings.Join(problems, " ")
	return level
}

// loadManifests reads every project's manifest, the readable ones in the
// order of their project names.
func (e Environment) loadManifests() []auth.Manifest {
	var manifests []auth.Manifest
	for _, name := range manifestProjects(e.DataDir) {
		if manifest, err := auth.LoadManifest(e.DataDir, name); err == nil {
			manifests = append(manifests, manifest)
		}
	}
	return manifests
}

// cliItems are the programs the manifests name first in a probe, a login or an
// identity command, each once.
func (e Environment) cliItems(ctx context.Context, manifests []auth.Manifest) []Item {
	var programs []string
	for _, manifest := range manifests {
		for _, service := range manifest.Services {
			commands := [][]string{service.Probe, service.Login}
			if service.Identity != nil {
				commands = append(commands, service.Identity.Command)
			}
			for _, command := range commands {
				if len(command) > 0 && validName(command[0]) {
					programs = append(programs, command[0])
				}
			}
		}
	}
	slices.Sort(programs)
	var items []Item
	for _, program := range slices.Compact(programs) {
		item := Item{ID: "cli:" + program, Level: LevelPC, Group: GroupCLI, Kind: "cli", Name: program, Source: "PATH"}
		args, hasStatusCommand := statusCommands[strings.ToLower(program)]
		switch {
		case !e.isInstalled(program):
			item.Status, item.Detail = StatusMissing, "Not installed on this PC."
		case hasStatusCommand:
			item.Status, item.Detail = e.signedIn(ctx, program, args)
			item.Source = program + " " + strings.Join(args, " ")
		default:
			item.Status, item.Detail = StatusSet, "Installed"
		}
		items = append(items, item)
	}
	return items
}

// harnessItems are the harnesses a goblin runs on: Claude and Codex by their
// own sign-in status, pi by being installed.
func (e Environment) harnessItems(ctx context.Context) []Item {
	var items []Item
	for _, harness := range []string{"claude", "codex", "pi"} {
		item := Item{ID: "harness:" + harness, Level: LevelAccount, Group: GroupHarness, Kind: "harness", Name: harness, Source: "PATH"}
		args, hasStatusCommand := harnessStatusCommands[harness]
		switch {
		case !e.isInstalled(harness):
			item.Status, item.Detail = StatusMissing, "Not installed on this PC."
		case hasStatusCommand:
			item.Status, item.Detail = e.signedIn(ctx, harness, args)
			item.Source = harness + " " + strings.Join(args, " ")
		default:
			item.Status, item.Detail = StatusSet, "Installed"
		}
		items = append(items, item)
	}
	return items
}

func (e Environment) isInstalled(program string) bool {
	_, err := e.LookPath(program)
	return err == nil
}

// signedIn runs a program's sign-in status command once and reads its exit
// code alone: what it printed is never read.
func (e Environment) signedIn(ctx context.Context, program string, args []string) (status, detail string) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeLimit)
	defer cancel()
	result, err := e.Runner.Run(ctx, execx.Request{Name: program, Args: args, KillTree: true})
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return StatusNotChecked, "Its status command did not answer in 20 s."
	case err != nil:
		return StatusNotChecked, "Its status command could not run."
	case result.ExitCode == 0:
		return StatusVerified, "Signed in"
	}
	return StatusUnauthorized, "Not signed in"
}

// claudeSettings holds the names Claude's user settings file keeps: the
// connectors the account's own sessions have connected, and the MCP servers
// of its user configuration. A server's own fields are skipped as they are
// read, never decoded or kept.
type claudeSettings struct {
	Connectors []string                 `json:"claudeAiMcpEverConnected"`
	Servers    map[string]skippedFields `json:"mcpServers"`
}

// skippedFields is a value that is read past and kept as nothing.
type skippedFields struct{}

func (*skippedFields) UnmarshalJSON([]byte) error { return nil }

// claudeSettings reads ~/.claude.json, none when the user has no such file.
func (e Environment) claudeSettings() (claudeSettings, error) {
	var settings claudeSettings
	data, err := readBounded(filepath.Join(e.UserHome, ".claude.json"), settingsLimit)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	return settings, json.Unmarshal(data, &settings)
}

// codexServers names the MCP servers of ~/.codex/config.toml, read as the
// lines that declare one, never anything inside them.
func (e Environment) codexServers() ([]string, error) {
	data, err := readBounded(filepath.Join(e.UserHome, ".codex", "config.toml"), settingsLimit)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		if match := codexServerHeader.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			names = append(names, match[1])
		}
	}
	return names, nil
}

func accountItems(settings claudeSettings) []Item {
	var items []Item
	connectors := slices.Clone(settings.Connectors)
	slices.Sort(connectors)
	for _, name := range slices.Compact(connectors) {
		if validName(name) {
			items = append(items, Item{ID: "connector:" + name, Level: LevelAccount, Group: GroupAccountConnector, Kind: "connector", Name: name, Status: StatusNotGiven, Detail: strictConfigDetail, Source: "~/.claude.json"})
		}
	}
	servers := make([]string, 0, len(settings.Servers))
	for name := range settings.Servers {
		servers = append(servers, name)
	}
	slices.Sort(servers)
	for _, name := range servers {
		if validName(name) {
			items = append(items, Item{ID: "mcp:" + name, Level: LevelAccount, Group: GroupUserConfig, Kind: "mcp", Name: name, Status: StatusNotGiven, Detail: strictConfigDetail, Source: "~/.claude.json"})
		}
	}
	return items
}

func codexItems(names []string) []Item {
	names = slices.Clone(names)
	slices.Sort(names)
	var items []Item
	for _, name := range slices.Compact(names) {
		items = append(items, Item{ID: "codex:" + name, Level: LevelAccount, Group: GroupUserConfig, Kind: "mcp", Name: name, Status: StatusSet, Detail: "Codex goblins get it.", Source: "~/.codex/config.toml"})
	}
	return items
}

// environmentItems are the credential-like variables of the user environment,
// each said with the projects whose manifests declare it.
func environmentItems(names []string, manifests []auth.Manifest) []Item {
	names = slices.Clone(names)
	slices.Sort(names)
	var items []Item
	for _, name := range slices.Compact(names) {
		if !auth.ValidEnvName(name) || !credentialLike.MatchString(name) || auth.IsHarnessBillingKey(name) {
			continue
		}
		var declaring []string
		for _, manifest := range manifests {
			if slices.Contains(manifest.EnvNames(), name) {
				declaring = append(declaring, manifest.Project)
			}
		}
		detail := "Every terminal and goblin carries it, with no grant. No project declares it."
		if len(declaring) > 0 {
			detail = "Every terminal and goblin carries it, with no grant. Declared by " + strings.Join(declaring, ", ") + "."
		}
		items = append(items, Item{ID: "credential:" + name, Level: LevelPC, Group: GroupEnvironment, Kind: "credential", Name: name, Status: StatusEverywhere, Detail: detail, Source: "this PC's user environment"})
	}
	return items
}

// builtInItems are the connectors every goblin carries with no setup of its
// project: Claude's own browser connector and the crawler.
func (e Environment) builtInItems() []Item {
	crawlerItem := Item{ID: "connector:" + crawler.Name, Level: LevelPC, Group: GroupBuiltIn, Kind: "connector", Name: crawler.Name, Source: "Code Goblins"}
	if crawler.IsCommandIn(crawler.BinDir(e.HomeRoot)) {
		crawlerItem.Status, crawlerItem.Detail = StatusSet, "Installed"
	} else {
		crawlerItem.Status, crawlerItem.Detail = StatusMissing, crawler.Command+" is not set up. The CEO installs it with goblins crawler setup."
	}
	return []Item{
		{ID: "mcp:claude-in-chrome", Level: LevelPC, Group: GroupBuiltIn, Kind: "mcp", Name: "claude-in-chrome", Status: StatusSet, Detail: "Built in to Claude: every Claude goblin loads it", Source: "Claude"},
		crawlerItem,
	}
}
