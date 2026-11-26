package connections

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

const sessionSource = "its session record"

// chromeServer is the MCP server Claude carries itself, which is on the PC's
// level, not a goblin's.
const chromeServer = "claude-in-chrome"

// ReadGoblin reads one goblin from its own records, starting nothing: what its
// task record gives it, and for a Claude goblin the servers its session record
// says loaded or failed to load. transcript is the session's file, read from
// byte offset from with prior's servers carried forward, so a long transcript
// costs only what was appended; it returns the offset to read from next. A
// transcript shorter than from is read again from the start.
func (e Environment) ReadGoblin(meta state.TaskMeta, transcript string, from int64, prior Goblin) (Goblin, int64) {
	goblin := Goblin{Task: meta.ID, Generation: meta.SpawnGen, Project: projectName(e.DataDir, meta.Project), Harness: meta.Harness}
	goblin.Items, goblin.Error = e.grantItems(meta)
	offset := int64(0)
	switch meta.Harness {
	case "claude":
		session, next, err := readSession(transcript, from, prior)
		offset = next
		if err != nil || !session.sawDelta {
			goblin.Note = "Its session has not recorded its servers yet."
			break
		}
		goblin.IsConfirmed = true
		goblin.Items = append(goblin.Items, session.items()...)
	case "codex":
		goblin.Note = "Codex does not record which servers loaded: it has the Codex user config's servers on the Account level."
	case "pi":
		goblin.Note = "Pi loads no MCP servers."
	default:
		goblin.Note = "Its harness does not record which servers loaded."
	}
	return goblin, offset
}

// grantItems are the services and project MCP servers the task's record gives
// the goblin, and those the CEO has not.
func (e Environment) grantItems(meta state.TaskMeta) (items []Item, problem string) {
	project := auth.ProjectName(meta.Project)
	manifest, err := auth.LoadManifest(e.DataDir, project)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		problem = "This project's auth manifest could not be read."
	}
	for _, service := range manifest.Services {
		// A task an older build started names no services and carries all.
		if !meta.HasCredentials || slices.Contains(meta.Credentials, service.Name) {
			items = append(items, Item{ID: "service:" + service.Name, Level: LevelGoblin, Group: GroupCarried, Kind: "service", Name: service.Name, Status: StatusSet, Detail: "Its terminal carries this service's credentials.", Source: "its task record", Grant: "revoke"})
			continue
		}
		items = append(items, Item{ID: "service:" + service.Name, Level: LevelGoblin, Group: GroupNotGiven, Kind: "service", Name: service.Name, Status: StatusNotGiven, Detail: "The CEO gives it with goblins auth grant " + meta.ID + " " + service.Name + ".", Source: "its task record", Grant: "grant"})
	}
	data, err := readBounded(filepath.Join(meta.Project, ".mcp.json"), mcpConfigLimit)
	if errors.Is(err, os.ErrNotExist) {
		return items, problem
	}
	var held []string
	if err == nil {
		_, _, _, _, held, err = worktree.FilterMCPServers(data, func(string) bool { return true }, func(string) bool { return false })
	}
	if err != nil {
		return items, strings.TrimSpace(problem + " This project's .mcp.json could not be read.")
	}
	for _, server := range held {
		if !validName(server) {
			continue
		}
		if slices.Contains(meta.MCPServers, server) {
			items = append(items, Item{ID: "mcp:" + server, Level: LevelGoblin, Group: GroupCarried, Kind: "mcp", Name: server, Status: StatusSet, Detail: "Its task record gives it this server.", Source: "its task record", Grant: "revoke"})
			continue
		}
		items = append(items, Item{ID: "mcp:" + server, Level: LevelGoblin, Group: GroupNotGiven, Kind: "mcp", Name: server, Status: StatusNotGiven, Detail: "The CEO gives it with goblins auth grant " + meta.ID + " --mcp " + server + ".", Source: "its task record", Grant: "grant"})
	}
	return items, problem
}
