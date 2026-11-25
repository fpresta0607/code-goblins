package connections

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// mcpConfigLimit is the largest .mcp.json read.
const mcpConfigLimit = 1 << 20

// CheckProject checks one project, once however many goblins it has: each
// service its manifest declares, with the probe and identity check the
// manifest names, and each MCP server of its .mcp.json, by how a goblin of
// the project gets it. path is the project's checkout, where the probes run.
func (e Environment) CheckProject(ctx context.Context, name, path string) Level {
	var level Level
	manifest, err := auth.LoadManifest(e.DataDir, name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		level.Error = "This project's auth manifest could not be read."
	}
	services, resolves := e.serviceItems(ctx, name, path, manifest)
	servers, err := mcpItems(name, path, manifest, resolves)
	if err != nil {
		level.Error = strings.TrimSpace(level.Error + " This project's .mcp.json could not be read.")
	}
	level.Items = append(services, servers...)
	return level
}

// serviceItems runs the manifest's check once over every service. resolves
// says whether a variable resolves for the project, which an MCP server's
// token needs; where nothing was checked it says yes, so no server reads as
// missing its token for a check that did not run.
func (e Environment) serviceItems(ctx context.Context, name, path string, manifest auth.Manifest) ([]Item, func(string) bool) {
	if len(manifest.Services) == 0 {
		return nil, func(string) bool { return false }
	}
	notChecked := func(reason string) ([]Item, func(string) bool) {
		items := make([]Item, 0, len(manifest.Services))
		for _, service := range manifest.Services {
			item := serviceItem(name, service, auth.Status{State: auth.StateUnverified})
			item.Status, item.Detail, item.Actions = StatusNotChecked, "Not checked: "+reason, nil
			items = append(items, item)
		}
		return items, func(string) bool { return true }
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return notChecked("its checkout was not found.")
	}
	store, err := e.OpenStore()
	if err != nil {
		return notChecked("the credential store could not be opened.")
	}
	report, err := (auth.Checker{Store: store, Runner: workspaceRunner{runner: e.Runner, dir: path}, Project: name}).Check(ctx, manifest)
	if err != nil {
		return notChecked("its check could not finish.")
	}
	resolved := map[string]bool{}
	items := make([]Item, 0, len(report.Statuses))
	refused := auth.RefusedNames(report)
	for index, status := range report.Statuses {
		items = append(items, serviceItem(name, manifest.Services[index], status))
		if !status.Usable() {
			continue
		}
		for _, variable := range status.Declared {
			if _, isRefused := refused[variable]; !isRefused && !auth.IsHarnessBillingKey(variable) && !slices.Contains(status.Missing, variable) {
				resolved[variable] = true
			}
		}
	}
	return items, func(variable string) bool { return resolved[variable] }
}

// serviceItem is one service's row: verified only when its probe passed, set
// when its credentials resolved and no check could say more, and the state's
// own name for anything else.
func serviceItem(project string, service auth.Service, status auth.Status) Item {
	item := Item{ID: "service:" + service.Name, Level: LevelProject, Group: GroupService, Kind: "service", Name: service.Name, Source: project + "'s auth.json"}
	if service.Identity != nil {
		item.Target = service.Identity.Expect
	}
	isUnresolved := func(variable string) bool {
		return slices.Contains(status.Missing, variable) || status.State == auth.StateMissing && len(status.Missing) == 0
	}
	for _, variable := range service.Env {
		item.Credentials = append(item.Credentials, Credential{Name: variable, IsResolved: !isUnresolved(variable)})
	}
	switch {
	case status.State == auth.StateGreen && len(service.Probe) > 0:
		item.Status, item.Detail = StatusVerified, "Its check passed."
	case status.State == auth.StateGreen && service.Identity != nil:
		item.Status, item.Detail = StatusSet, "Credentials resolved and name this project. It declares no connection check."
	case status.State == auth.StateGreen:
		item.Status, item.Detail = StatusSet, "Credentials resolved. It declares no connection check."
	case status.State == auth.StateUnverified && status.Unchecked != "":
		item.Status, item.Detail = StatusSet, "Not checked on this machine: "+status.Unchecked+"."
	case status.State == auth.StateUnverified:
		item.Status, item.Detail = StatusNotChecked, "No check could confirm it."
	case status.State == auth.StateSkipped:
		item.Status, item.Detail = StatusNotChecked, "It is optional, and not available."
	default:
		item.Status = string(status.State)
		item.Detail = statusDetail[item.Status]
		if status.State == auth.StateWrongTarget && service.Identity != nil {
			item.Detail += " It expects " + service.Identity.Expect + "."
		}
	}
	if item.Status == StatusVerified || item.Status == StatusSet {
		return item
	}
	if safeLoginURL(service.URL) {
		item.Actions = append(item.Actions, "login")
	}
	if len(service.Login) > 0 && len(status.Missing) == 0 {
		item.Actions = append(item.Actions, "cli")
	}
	for _, variable := range status.Missing {
		if auth.ValidEnvName(variable) && !auth.IsHarnessBillingKey(variable) {
			item.Actions = append(item.Actions, "store:"+variable)
		}
	}
	return item
}

// mcpServer is the part of an .mcp.json entry that says what it points at.
type mcpServer struct {
	URL  string   `json:"url"`
	Args []string `json:"args"`
}

// mcpItems are the project's .mcp.json servers by how a goblin of the project
// gets each: every goblin, only one whose brief names it, none because its
// sign-in is a browser's, or none because its token variable does not resolve.
func mcpItems(project, path string, manifest auth.Manifest, resolves func(string) bool) ([]Item, error) {
	data, err := readBounded(filepath.Join(path, ".mcp.json"), mcpConfigLimit)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, kept, dropped, unset, held, err := worktree.FilterMCPServers(data, resolves, func(string) bool { return false })
	if err != nil {
		return nil, err
	}
	var document struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	newItem := func(server, status, detail string) Item {
		var shape mcpServer
		_ = json.Unmarshal(document.Servers[server], &shape)
		return Item{ID: "mcp:" + server, Level: LevelProject, Group: GroupMCP, Kind: "mcp", Name: server, Status: status, Detail: detail, Target: mcpTarget(shape), Source: project + "'s .mcp.json"}
	}
	var items []Item
	for _, server := range kept {
		items = append(items, newItem(server, StatusSet, "Every goblin of this project gets it."))
	}
	for _, server := range held {
		items = append(items, newItem(server, StatusSet, "Given to a goblin whose brief names it."))
	}
	for _, server := range dropped {
		items = append(items, newItem(server, StatusNotGiven, "Sign-in only: a goblin cannot finish its browser sign-in."))
	}
	for _, entry := range unset {
		server, variable, _ := strings.Cut(strings.TrimSuffix(entry, ")"), " (")
		item := newItem(server, StatusMissing, "Its token variable "+variable+" is not stored for this project.")
		if slices.Contains(manifest.EnvNames(), variable) && auth.ValidEnvName(variable) && !auth.IsHarnessBillingKey(variable) {
			item.Actions = []string{"store:" + variable}
		}
		items = append(items, item)
	}
	items = slices.DeleteFunc(items, func(item Item) bool { return !validName(item.Name) })
	slices.SortFunc(items, func(a, b Item) int { return strings.Compare(a.Name, b.Name) })
	return items, nil
}

// mcpTarget is what a server points at: a Supabase project by the last four
// characters of its reference, else its host, else a command on this PC.
func mcpTarget(server mcpServer) string {
	parsed, err := url.Parse(server.URL)
	if err != nil {
		parsed = &url.URL{}
	}
	reference := strings.TrimSpace(parsed.Query().Get("project_ref"))
	for index, arg := range server.Args {
		if value, isJoined := strings.CutPrefix(arg, "--project-ref="); isJoined {
			reference = strings.TrimSpace(value)
		} else if arg == "--project-ref" && index+1 < len(server.Args) {
			reference = strings.TrimSpace(server.Args[index+1])
		}
	}
	if reference != "" {
		runes := []rune(reference)
		return "Supabase project …" + string(runes[max(0, len(runes)-4):])
	}
	if parsed.Host != "" {
		return parsed.Host
	}
	return "local command"
}
