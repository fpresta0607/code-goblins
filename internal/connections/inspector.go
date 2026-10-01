package connections

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type Inspector struct {
	DataDir, StateDir string
	Runner            execx.Runner
	OpenStore         func() (auth.Store, error)
	Runtime           func(context.Context, string, state.TaskMeta) ([]string, []string, error)
	Codex             func(context.Context, string, []string, []string) ([]Entry, error)
	Claude            func(context.Context, string, []string, []string) ([]Entry, error)
}

func NewInspector(dataDir, stateDir string) *Inspector {
	return &Inspector{DataDir: dataDir, StateDir: stateDir, Runner: execx.OSRunner{}, OpenStore: auth.OpenStore, Runtime: nativeRuntime, Codex: checkCodex, Claude: checkClaude}
}
func (i *Inspector) Check(ctx context.Context, meta state.TaskMeta) Snapshot {
	result := Snapshot{Entries: []Entry{}}
	manifest, manifestErr := auth.LoadManifest(i.DataDir, meta.Project)
	if manifestErr != nil && !errors.Is(manifestErr, os.ErrNotExist) {
		result.Error = "Repository connections could not be read."
	}
	if manifestErr == nil {
		store, err := i.OpenStore()
		if err != nil {
			result.Error = "Credential store is unavailable."
			for _, service := range manifest.Services {
				result.Entries = append(result.Entries, serviceEntry(service, auth.Status{State: auth.StateUnverified}))
			}
		} else {
			checker := auth.Checker{Store: store, Runner: workspaceRunner{Runner: i.Runner, dir: meta.Worktree}, Project: auth.ProjectName(meta.Project)}
			report, err := checker.Check(ctx, manifest)
			if err != nil {
				result.Error = "Repository checks could not finish."
			} else {
				for index, status := range report.Statuses {
					entry := serviceEntry(manifest.Services[index], status)
					entry.CheckedAt = time.Now().UTC()
					result.Entries = append(result.Entries, entry)
				}
			}
		}
	}
	env, args, runtimeErr := i.Runtime(ctx, i.StateDir, meta)
	if runtimeErr != nil {
		result.Error = runtimeErr.Error()
		for _, name := range manifest.EnvNames() {
			result.Entries = append(result.Entries, Entry{ID: "credential:" + name, Name: name, Kind: "credential", Status: "unverified", Detail: "Goblin runtime unavailable.", CheckedAt: time.Now().UTC()})
		}
		for _, entry := range withheldEntries(meta, nil) {
			entry.Status = "unverified"
			entry.Detail = "Goblin runtime unavailable."
			entry.Actions = nil
			result.Entries = append(result.Entries, entry)
		}
		return result
	}
	values := map[string]string{}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[strings.ToUpper(key)] = value
		}
	}
	credentialNames := manifest.EnvNames()
	for name := range values {
		if auth.ValidEnvName(name) && !auth.IsHarnessBillingKey(name) && credentialName.MatchString(name) {
			credentialNames = append(credentialNames, name)
		}
	}
	slices.Sort(credentialNames)
	for _, name := range slices.Compact(credentialNames) {
		verdict := "missing"
		actions := []string{"store:" + name}
		if values[strings.ToUpper(name)] != "" {
			verdict = "provided"
			actions = nil
		}
		result.Entries = append(result.Entries, Entry{ID: "credential:" + name, Name: name, Kind: "credential", Status: verdict, Source: "Goblin environment", Actions: actions, CheckedAt: time.Now().UTC()})
	}
	var entries []Entry
	var err error
	switch meta.Harness {
	case "claude":
		checkArgs := []string{}
		for index, arg := range args {
			if arg == "--strict-mcp-config" {
				checkArgs = append(checkArgs, arg)
			}
			if arg == "--mcp-config" && index+1 < len(args) {
				checkArgs = append(checkArgs, arg, args[index+1])
				entries = append(entries, configEntries(args[index+1])...)
			}
		}
		probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		var checked []Entry
		checked, err = i.Claude(probeCtx, meta.Worktree, env, checkArgs)
		cancel()
		entries = mergeEntries(entries, checked)
	case "codex":
		overrides := []string{}
		for index, arg := range args {
			if arg == "-c" && index+1 < len(args) && mcpOverride.MatchString(args[index+1]) {
				overrides = append(overrides, "-c", args[index+1])
			}
		}
		probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		entries, err = i.codexInventory(probeCtx, meta.Worktree, env, overrides)
		if err == nil && slices.ContainsFunc(entries, func(entry Entry) bool { return entry.Status != "disabled" }) {
			var checked []Entry
			checked, err = i.Codex(probeCtx, meta.Worktree, env, overrides)
			entries = mergeEntries(entries, checked)
		}
		cancel()
	default:
		err = errors.New("This harness does not report MCP connection health.")
	}
	if err != nil {
		result.Error = "MCP connection check failed or timed out."
	}
	for index := range entries {
		entries[index].CheckedAt = time.Now().UTC()
	}
	result.Entries = append(result.Entries, entries...)
	result.Entries = append(result.Entries, withheldEntries(meta, entries)...)
	for index := range result.Entries {
		entry := &result.Entries[index]
		entry.Actions = slices.DeleteFunc(entry.Actions, func(action string) bool {
			credential, isStore := strings.CutPrefix(action, "store:")
			return isStore && (!slices.Contains(manifest.EnvNames(), credential) || auth.IsHarnessBillingKey(credential))
		})
		if entry.Kind == "mcp" && entry.Status == "unauthorized" && len(entry.Actions) == 0 {
			entry.Detail = "Sign in from your own harness session, or ask the CFO for token access."
		}
	}
	sort.SliceStable(result.Entries, func(a, b int) bool {
		return result.Entries[a].Kind+result.Entries[a].Name < result.Entries[b].Kind+result.Entries[b].Name
	})
	return result
}

var credentialName = regexp.MustCompile(`(?i)(TOKEN|API_KEY|PASSWORD|SECRET|CONNECTION_STRING|DATABASE_URL)$`)
var mcpOverride = regexp.MustCompile(`^mcp_servers\.[A-Za-z0-9_-]+\.enabled=(true|false)$`)

type workspaceRunner struct {
	Runner execx.Runner
	dir    string
}

func (r workspaceRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	request.Dir, request.KillTree = r.dir, true
	return r.Runner.Run(ctx, request)
}

func readPrivate(path string) ([]byte, error) {
	file, err := fsx.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("Connection configuration is unreadable.")
	}
	return data, nil
}

func withheldEntries(meta state.TaskMeta, reported []Entry) []Entry {
	data, err := readPrivate(filepath.Join(meta.Project, ".mcp.json"))
	if err != nil {
		return nil
	}
	var config struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(data, &config) != nil {
		return nil
	}
	entries := []Entry{}
	for name, raw := range config.Servers {
		if !validName(name) || slices.ContainsFunc(reported, func(entry Entry) bool { return entry.Name == name }) {
			continue
		}
		var shape struct {
			URL     string            `json:"url"`
			Command string            `json:"command"`
			Token   string            `json:"bearerTokenEnvVar"`
			Headers map[string]string `json:"headers"`
		}
		if json.Unmarshal(raw, &shape) != nil {
			continue
		}
		entry := Entry{ID: "withheld:" + name, Name: name, Kind: "mcp", Status: "withheld", Source: "Project", Detail: "Not loaded by this goblin's harness.", CheckedAt: time.Now().UTC()}
		hasToken := shape.Token != ""
		for header := range shape.Headers {
			hasToken = hasToken || strings.EqualFold(header, "authorization")
		}
		if shape.URL != "" && shape.Command == "" && !hasToken {
			entry.Detail = "OAuth-only. Use a token or your own session."
		}
		if auth.ValidEnvName(shape.Token) {
			entry.Actions = []string{"store:" + shape.Token}
		}
		entries = append(entries, entry)
	}
	return entries
}

func configEntries(path string) []Entry {
	data, err := readPrivate(path)
	if err != nil {
		return nil
	}
	var config struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(data, &config) != nil {
		return nil
	}
	var entries []Entry
	for name, raw := range config.Servers {
		if validName(name) {
			entry := Entry{ID: "mcp:" + name, Name: name, Kind: "mcp", Status: "unverified", Source: "Claude", Detail: "No successful connection check."}
			var config struct {
				Token   string            `json:"bearerTokenEnvVar"`
				Headers map[string]string `json:"headers"`
				Env     map[string]string `json:"env"`
			}
			if json.Unmarshal(raw, &config) == nil {
				names := []string{config.Token}
				for _, values := range []map[string]string{config.Headers, config.Env} {
					for _, value := range values {
						for _, match := range credentialReference.FindAllStringSubmatch(value, -1) {
							names = append(names, match[1])
						}
					}
				}
				slices.Sort(names)
				for _, credential := range slices.Compact(names) {
					if auth.ValidEnvName(credential) && !auth.IsHarnessBillingKey(credential) {
						entry.Actions = append(entry.Actions, "store:"+credential)
					}
				}
			}
			entries = append(entries, entry)
		}
	}
	return entries
}

func mergeEntries(inventory, checked []Entry) []Entry {
	for _, entry := range checked {
		index := slices.IndexFunc(inventory, func(prior Entry) bool { return prior.ID == entry.ID })
		if index < 0 {
			inventory = append(inventory, entry)
		} else {
			if entry.Status != "connected" && entry.Status != "disabled" {
				entry.Actions = append(entry.Actions, inventory[index].Actions...)
			}
			inventory[index] = entry
		}
	}
	return inventory
}

var credentialReference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func (i *Inspector) codexInventory(ctx context.Context, dir string, env, overrides []string) ([]Entry, error) {
	args := append([]string{"mcp", "list", "--json"}, overrides...)
	program, err := spawn.NativeProgram("codex", args...)
	if err != nil {
		return nil, errors.New("Codex is unavailable.")
	}
	report, err := i.Runner.Run(ctx, execx.Request{Name: program[0], Args: program[1:], Dir: dir, Env: env, KillTree: true})
	if err != nil || report.ExitCode != 0 {
		return nil, errors.New("Codex inventory could not be read.")
	}
	var inventory []struct {
		Name    string `json:"name"`
		Enabled *bool  `json:"enabled"`
	}
	if json.Unmarshal(report.Stdout, &inventory) != nil {
		return nil, errors.New("Codex inventory could not be read.")
	}
	entries := []Entry{}
	for _, server := range inventory {
		if !validName(server.Name) {
			continue
		}
		status := "unverified"
		if server.Enabled != nil && !*server.Enabled {
			status = "disabled"
		}
		entries = append(entries, Entry{ID: "mcp:" + server.Name, Name: server.Name, Kind: "mcp", Status: status, Source: "Codex", Detail: statusDetail(status)})
	}
	return entries, nil
}
